package translate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// magpie 的 SSE 与 OpenAI 官方格式有两处差异：data 冒号后没有空格，且先发 reasoning_content。
func sseServer(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			fmt.Fprintf(w, "%s\n\n", f)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))
}

func TestStreamParsesMagpieFormat(t *testing.T) {
	frames := []string{
		": keepalive",
		`data:{"choices":[{"delta":{"reasoning_content":"thinking..."}}]}`,
		`data:{"choices":[{"delta":{"content":"你好"}}]}`,
		"data: {\"choices\":[{\"delta\":{\"content\":\"，世界\"}}]}", // 标准写法也要认
		"data:[DONE]",
	}
	srv := sseServer(t, frames)
	defer srv.Close()

	var got []string
	full, err := Stream(context.Background(), srv.URL, Request{Model: "m", Target: "zh", Text: "hi"}, func(d string) {
		got = append(got, d)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if full != "你好，世界" {
		t.Fatalf("full = %q", full)
	}
	if strings.Join(got, "|") != "你好|，世界" {
		t.Fatalf("deltas = %v", got)
	}
}

func TestStreamInterruptedKeepsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data:{\"choices\":[{\"delta\":{\"content\":\"半截\"}}]}\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		// 直接掐断：模拟上游断流
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	defer srv.Close()

	full, err := Stream(context.Background(), srv.URL, Request{Model: "m", Target: "zh", Text: "hi"}, nil)
	if full != "半截" {
		t.Fatalf("full = %q", full)
	}
	// 断流后被当成正常结束（scanner 读到 EOF 前无错误），关键是不能丢已收内容
	if err != nil && err != ErrInterrupted {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamStatusMapping(t *testing.T) {
	cases := []struct {
		code int
		want error
	}{
		{404, ErrModelMissing},
		{401, ErrAuth},
		{403, ErrAuth},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.code)
			fmt.Fprint(w, `{"error":{"message":"boom"}}`)
		}))
		_, err := Stream(context.Background(), srv.URL, Request{Model: "m", Text: "hi"}, nil)
		srv.Close()
		if err != c.want {
			t.Errorf("status %d -> %v, want %v", c.code, err, c.want)
		}
	}
}

func TestStreamRespectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Stream(ctx, srv.URL, Request{Model: "m", Text: "hi"}, nil)
	if err == nil {
		t.Fatal("expected context error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("取消没有生效，耗时 %v", time.Since(start))
	}
}

func TestGatewayProbeAndModelDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"group/first"},{"id":"second"}]}`)
	}))
	defer srv.Close()

	g := NewGateway(srv.URL)
	p, err := g.Resolve(context.Background())
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if p.Base != srv.URL || p.FirstModel != "group/first" {
		t.Fatalf("probe = %+v", p)
	}
	if g.Known() != srv.URL {
		t.Fatalf("Known() = %q", g.Known())
	}
}

func TestGatewayDownWhenNothingAnswers(t *testing.T) {
	// 127.0.0.1:1 一般没人监听；锁定候选列表，避免回落到本机真实 magpie 网关。
	g := NewGateway("127.0.0.1:1")
	g.Candidates = []string{"127.0.0.1:1"}
	if _, err := g.Resolve(context.Background()); err != ErrGatewayDown {
		t.Fatalf("err = %v, want ErrGatewayDown", err)
	}
}

func TestCacheLRU(t *testing.T) {
	c := NewCache(2)
	c.Put("a", "1")
	c.Put("b", "2")
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatal("a 命中失败")
	}
	c.Put("c", "3") // 淘汰最久未用的 b
	if _, ok := c.Get("b"); ok {
		t.Fatal("b 应该被淘汰")
	}
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatal("a 不该被淘汰")
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d", c.Len())
	}
	if CacheKey("x", "zh", "m") == CacheKey("x", "en", "m") {
		t.Fatal("不同目标语言必须不同 key")
	}
}
