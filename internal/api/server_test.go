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
