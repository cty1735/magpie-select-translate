package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"magpie-translate/internal/pick"
	"magpie-translate/internal/store"
	"magpie-translate/internal/translate"
)

// fakeGateway 模拟 magpie：/v1/models + 流式 /v1/chat/completions。
func fakeGateway(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"group/default"}]}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			for _, chunk := range strings.Split(answer, "|") {
				fmt.Fprintf(w, "data:{\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", chunk)
			}
			fmt.Fprint(w, "data:[DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestServer(t *testing.T, gateway *httptest.Server, cfg store.Settings) *Server {
	t.Helper()
	gw := translate.NewGateway(gateway.URL)
	gw.Candidates = []string{gateway.URL} // 只认这台假网关，别回落到本机真实 magpie
	return &Server{
		Port:     3477,
		Gateway:  gw,
		Cache:    translate.NewCache(16),
		Picker:   pick.New("", true),
		Settings: func() store.Settings { return cfg },
		Apply:    func(next store.Settings) error { cfg = next; return nil },
	}
}

func TestTranslateCachesAndDetectsTarget(t *testing.T) {
	gw := fakeGateway(t, "你好，世界")
	defer gw.Close()
	cfg := store.Defaults()
	s := newTestServer(t, gw, cfg)

	res, err := s.Translate(context.Background(), "Hello world", "", "", nil)
	if err != nil {
		t.Fatalf("translate failed: %v", err)
	}
	if res.Text != "你好，世界" || res.Target != "zh" || res.Cached {
		t.Fatalf("res = %+v", res)
	}
	if res.Model != "group/default" {
		t.Fatalf("默认模型应取 /v1/models 第一个，got %q", res.Model)
	}

	again, err := s.Translate(context.Background(), "Hello world", "", "", nil)
	if err != nil {
		t.Fatalf("second translate failed: %v", err)
	}
	if !again.Cached {
		t.Fatal("第二次应命中缓存")
	}
}

func TestTranslateRejectsTooShort(t *testing.T) {
	gw := fakeGateway(t, "x")
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	if _, err := s.Translate(context.Background(), " a ", "", "", nil); err != ErrTooShort {
		t.Fatalf("err = %v, want ErrTooShort", err)
	}
}

func TestTranslateGatewayDown(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	_, err := s.Translate(context.Background(), "hello there", "", "", nil)
	if err != translate.ErrGatewayDown {
		t.Fatalf("err = %v, want ErrGatewayDown", err)
	}
	if msg := Message(err); !strings.Contains(msg, "magpie 未运行") {
		t.Fatalf("message = %q", msg)
	}
}

func TestTranslateSSEEndpoint(t *testing.T) {
	gw := fakeGateway(t, "你好")
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"text":"Hello there","target":"","model":""}`
	resp, err := http.Post(srv.URL+"/api/translate", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := string(raw)
	if !strings.Contains(out, `"delta"`) || !strings.Contains(out, `"done"`) {
		t.Fatalf("sse output = %q", out)
	}
}

func TestGuardHostBlocksForeignHost(t *testing.T) {
	gw := fakeGateway(t, "x")
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/ping", nil)
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	gw := fakeGateway(t, "x")
	defer gw.Close()
	cfg := store.Defaults()
	s := newTestServer(t, gw, cfg)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"port":3477,"target":"en","hotkey":"Ctrl+Shift+T","blacklist":"a.exe","restoreClipboard":false}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := s.Settings() // 用服务端自己的副本校验，参数是按值传进去的
	if got.Target != "en" || got.Hotkey != "Ctrl+Shift+T" || got.RestoreClipboard {
		t.Fatalf("cfg not applied: %+v", got)
	}
}

func TestGuardRejectsBrowserOrigin(t *testing.T) {
	gw := fakeGateway(t, "x")
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/ping", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestWritesRequireJSON(t *testing.T) {
	gw := fakeGateway(t, "x")
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/settings", "text/plain", strings.NewReader(`{"target":"en"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", resp.StatusCode)
	}
}

func TestSettingsIgnoreGlossaryPath(t *testing.T) {
	gw := fakeGateway(t, "x")
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// 跨站页面曾用这个字段把任意本地文件内容送进 prompt；现在只认正文，路径直接忽略。
	body := `{"glossaryFile":"C:\\Windows\\win.ini"}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := s.Settings().Glossary(); got != "" {
		t.Fatalf("glossary = %q, want empty", got)
	}
}

// 设置页跑在沙箱帧里（Origin: null），只有带令牌的请求能过；别的页面照旧 403。
func TestGuardAllowsOnlyOwnSettingsPage(t *testing.T) {
	gw := fakeGateway(t, "x")
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	s.Token = "tok"
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	do := func(method, path, token string, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Origin", "null")
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Helper-Token", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	if resp := do(http.MethodPut, "/api/settings", "", `{"target":"en"}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无令牌 status = %d, want 403", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	resp := do(http.MethodPut, "/api/settings", "tok", `{"target":"en"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("带令牌 status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "null" {
		t.Fatalf("ACAO = %q, want null（沙箱页是 opaque origin）", got)
	}

	// 预检带不了令牌，但只声明允许的头，真正的请求仍要令牌。
	pre := do(http.MethodOptions, "/api/settings", "", "")
	defer pre.Body.Close()
	if pre.StatusCode != http.StatusNoContent || !strings.Contains(pre.Header.Get("Access-Control-Allow-Headers"), "x-helper-token") {
		t.Fatalf("预检 status = %d headers = %v", pre.StatusCode, pre.Header)
	}
}

func TestModelsEndpointAndSettingsPage(t *testing.T) {
	gw := fakeGateway(t, "x")
	defer gw.Close()
	s := newTestServer(t, gw, store.Defaults())
	s.Token = "tok"
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	out := string(raw)
	if !strings.Contains(out, `"group/default"`) || !strings.Contains(out, `"default":"group/default"`) {
		t.Fatalf("models = %s", out)
	}

	page, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(html), "/settings.js") {
		t.Fatalf("设置页应该引用 /settings.js")
	}

	js, err := http.Get(srv.URL + "/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(js.Body)
	js.Body.Close()
	if !strings.Contains(string(body), "tok") || strings.Contains(string(body), "__TOKEN__") {
		t.Fatal("settings.js 里的令牌没有被替换")
	}
}
