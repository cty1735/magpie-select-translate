// Package api 是 helper 的本地 HTTP 接口，只监听 127.0.0.1。
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"magpie-translate/internal/pick"
	"magpie-translate/internal/store"
	"magpie-translate/internal/translate"
	"magpie-translate/internal/winapi"
)

const Version = "0.3.1"
const maxTextRunes = 5000

var ErrTooShort = errors.New("没选中文字")

//go:embed settings.html settings.js
var assets embed.FS

type Server struct {
	Port     int
	Gateway  *translate.Gateway
	Cache    *translate.Cache
	Picker   *pick.Picker
	Settings func() store.Settings
	Apply    func(store.Settings) error // PUT /api/settings 落地并热应用
	OnHide   func()
	// Token 每次启动随机生成，只注入到设置页里：带 Origin 的请求必须带它才放行。
	Token string

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewToken 生成设置页用的随机令牌。
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// Result 是一次翻译的完整结果。浮窗和 HTTP 接口都走 Translate，不写两套逻辑。
type Result struct {
	Text      string
	Target    string
	Model     string
	Cached    bool
	Truncated bool
	Millis    int64
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", s.handlePing)
	mux.HandleFunc("/api/translate", s.handleTranslate)
	mux.HandleFunc("/api/selection", s.handleSelection)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/clipboard", s.handleClipboard)
	mux.HandleFunc("/api/hide", s.handleHide)
	mux.HandleFunc("/settings.js", s.handleSettingsJS)
	mux.HandleFunc("/", s.handleSettingsPage)
	return s.guardHost(mux)
}

// guardHost 挡住浏览器页面：只认回环 Host。
// 带 Origin 的请求只有本设置页（带 X-Helper-Token）能过，其余一律拒绝——
// 设置页跑在沙箱帧里，是 opaque origin（字面量 null），不靠 token 就分不出它和别人的页面。
// 写请求必须是 application/json，跨站页面就没法用 text/plain 简单请求绕过预检。
func (s *Server) guardHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		if r.Method == http.MethodOptions {
			// 预检带不了自定义头（也就带不了 token），只能照声明放行；
			// 真正的请求仍要 token，跨站页面过得了预检也用不了接口。
			if origin != "" {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Headers", "x-helper-token, content-type")
				h.Set("Access-Control-Allow-Methods", "GET, PUT, POST, OPTIONS")
				h.Set("Access-Control-Max-Age", "600")
				h.Set("Vary", "Origin")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if origin != "" {
			if !s.authorized(r) {
				http.Error(w, "cross-origin request", http.StatusForbidden)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
				http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	got := r.Header.Get("X-Helper-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

// handleSettingsPage 提供设置页本身：插件页面只是个跳板，真正能联网的设置界面在这里。
func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/settings" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, err := assets.ReadFile("settings.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(page)
}

func (s *Server) handleSettingsJS(w http.ResponseWriter, r *http.Request) {
	js, err := assets.ReadFile("settings.js")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// 令牌只出现在这个文件里：别的站点读不到（跨源读被浏览器拦着），猜也猜不到。
	body := strings.Replace(string(js), "__TOKEN__", s.Token, 1)
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

// handleModels 把网关的模型列表交给设置页做下拉。第一个就是网关的默认模型。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models, probe, err := s.Gateway.Models(r.Context())
	if err != nil {
		writeJSON(w, map[string]any{"models": []string{}, "error": err.Error()})
		return
	}
	def := ""
	if len(models) > 0 {
		def = models[0]
	}
	writeJSON(w, map[string]any{
		"models":  models,
		"default": def,
		"gateway": probe.Base,
		"current": strings.TrimSpace(s.Settings().Model),
	})
}

// Translate 是唯一的翻译实现：截断、缓存、网关发现、流式增量都在这。
func (s *Server) Translate(ctx context.Context, text, target, model string, onDelta func(string)) (Result, error) {
	var res Result
	text = pick.Normalize(text)
	if utf8.RuneCountInString(text) < 2 {
		return res, ErrTooShort
	}
	if utf8.RuneCountInString(text) > maxTextRunes {
		text = string([]rune(text)[:maxTextRunes])
		res.Truncated = true
	}

	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel() // 新请求先掐掉旧流，避免两股内容串在一起
	}
	s.cancel = cancel
	s.mu.Unlock()
	defer cancel()

	probe, err := s.Gateway.Resolve(ctx)
	if err != nil {
		s.Gateway.Invalidate()
		return res, err
	}

	cfg := s.Settings()
	res.Model = firstNonEmpty(model, cfg.Model, probe.FirstModel)
	t := translate.NormalizeTarget(cfg.Target)
	if v := translate.NormalizeTarget(target); v != "" {
		t = v
	}
	if t == "" {
		t = translate.DetectTarget(text)
	}
	res.Target = t

	key := translate.CacheKey(text, t, res.Model)
	if full, ok := s.Cache.Get(key); ok {
		res.Text, res.Cached = full, true
		if onDelta != nil {
			onDelta(full)
		}
		return res, nil
	}

	start := time.Now()
	full, err := translate.Stream(ctx, probe.Base, translate.Request{
		Model: res.Model, Target: t, Text: text, Glossary: cfg.Glossary(),
	}, onDelta)
	res.Millis = time.Since(start).Milliseconds()
	res.Text = full

	switch {
	case err == nil:
		s.Cache.Put(key, full)
	case errors.Is(err, translate.ErrInterrupted) && full != "":
		s.Cache.Put(key, full) // 半截结果也留着，重试能秒回
	default:
		s.Gateway.Invalidate()
	}
	return res, err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	cfg := s.Settings()
	writeJSON(w, map[string]any{
		"ok":      true,
		"helper":  true,
		"version": Version,
		"port":    s.Port,
		"gateway": s.Gateway.Known(),
		"hotkey":  cfg.Hotkey,
	})
}

func (s *Server) handleSelection(w http.ResponseWriter, r *http.Request) {
	res, err := s.Picker.Pick()
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "app": res.App, "ms": res.Millis})
		return
	}
	writeJSON(w, map[string]any{"text": res.Text, "via": res.Via, "app": res.App, "ms": res.Millis})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.Settings())
	case http.MethodPut, http.MethodPost:
		next := s.Settings()
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.Apply(next); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, next)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleClipboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := winapi.WriteClipboardText(body.Text); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleHide(w http.ResponseWriter, r *http.Request) {
	if s.OnHide != nil {
		s.OnHide()
	}
	writeJSON(w, map[string]any{"ok": true})
}

type translateRequest struct {
	Text   string `json:"text"`
	Target string `json:"target"`
	Model  string `json:"model"`
}

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req translateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	emit := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
		flusher.Flush()
	}

	res, err := s.Translate(r.Context(), req.Text, req.Target, req.Model, func(delta string) {
		emit(map[string]any{"delta": delta})
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return // 被新请求或页面关闭取消，不用报错
		}
		code, msg := classify(err)
		emit(map[string]any{"error": map[string]string{"code": code, "message": msg}})
		return
	}
	emit(map[string]any{"done": map[string]any{
		"cached": res.Cached, "ms": res.Millis, "model": res.Model, "target": res.Target,
	}})
}

func classify(err error) (string, string) {
	switch {
	case errors.Is(err, ErrTooShort):
		return "short", err.Error()
	case errors.Is(err, translate.ErrGatewayDown):
		return "gateway_down", translate.ErrGatewayDown.Error()
	case errors.Is(err, translate.ErrModelMissing):
		return "model", translate.ErrModelMissing.Error()
	case errors.Is(err, translate.ErrAuth):
		return "auth", translate.ErrAuth.Error()
	case errors.Is(err, translate.ErrInterrupted):
		return "interrupted", "连接中断，点击重试"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", "翻译超时，点击重试"
	case errors.Is(err, translate.ErrUpstream):
		return "upstream", err.Error()
	}
	return "error", err.Error()
}

// Message 把错误换成给用户看的一句话（浮窗用）。
func Message(err error) string {
	_, msg := classify(err)
	return msg
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
