package translate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrGatewayDown = errors.New("magpie 未运行，请启动后重试")

const gatewayTTL = 60 * time.Second

// Gateway 负责找出 magpie 网关地址，并记住 /v1/models 的第一个模型作为默认值。
type Gateway struct {
	mu       sync.Mutex
	override string // 命令行 --gateway 或 settings.gateway
	// Candidates 非空时只探测这些地址（测试和"锁定单个网关"场景用）。
	Candidates []string
	base       string
	first      string
	at         time.Time
}

type Probe struct {
	Base       string
	FirstModel string
}

func NewGateway(override string) *Gateway {
	return &Gateway{override: strings.TrimRight(strings.TrimSpace(override), "/")}
}

// Resolve 依次探测候选地址，结果缓存 60s；探测全失败则返回 ErrGatewayDown。
func (g *Gateway) Resolve(ctx context.Context) (Probe, error) {
	g.mu.Lock()
	if g.base != "" && time.Since(g.at) < gatewayTTL {
		p := Probe{Base: g.base, FirstModel: g.first}
		g.mu.Unlock()
		return p, nil
	}
	g.mu.Unlock()

	for _, cand := range g.candidates() {
		ids, err := listModels(ctx, cand)
		if err != nil {
			continue
		}
		p := Probe{Base: cand}
		if len(ids) > 0 {
			p.FirstModel = ids[0]
		}
		g.mu.Lock()
		g.base, g.first, g.at = p.Base, p.FirstModel, time.Now()
		g.mu.Unlock()
		return p, nil
	}
	g.mu.Lock()
	g.at = time.Time{} // 下次调用重新探测，magpie 重启后无需重启 helper
	g.mu.Unlock()
	return Probe{}, ErrGatewayDown
}

// Known 返回已解析的网关地址（未解析过则返回配置里的 override，仅用于 /api/ping 展示）。
func (g *Gateway) Known() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.base != "" {
		return g.base
	}
	return g.override
}

// Invalidate 在请求失败后调用，让下一次触发重新发现网关。
func (g *Gateway) Invalidate() {
	g.mu.Lock()
	g.at = time.Time{}
	g.mu.Unlock()
}

// candidates 返回按优先级排列的候选 base URL（去重、去尾斜杠）。
func (g *Gateway) candidates() []string {
	var out []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			raw = "http://" + raw
		}
		raw = strings.TrimRight(raw, "/")
		for _, o := range out {
			if o == raw {
				return
			}
		}
		out = append(out, raw)
	}

	add(g.override)
	if len(g.Candidates) > 0 {
		for _, c := range g.Candidates {
			add(c)
		}
		return out
	}
	add(os.Getenv("MAGPIE_ADDR"))
	for _, dir := range ConfigDirs() {
		if port := portFromSettings(dir); port != 0 {
			add("127.0.0.1:" + strconv.Itoa(port))
		}
	}
	add("127.0.0.1:3425")
	return out
}

// ConfigDirs 返回可能存放 magpie 配置的目录（按优先级）。
func ConfigDirs() []string {
	var dirs []string
	if v := os.Getenv("MAGPIE_CONFIG_DIR"); v != "" {
		dirs = append(dirs, v)
	}
	if v := os.Getenv("APPDATA"); v != "" {
		dirs = append(dirs, filepath.Join(v, "magpie"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "magpie"))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "data"))
	}
	return dirs
}

func portFromSettings(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return 0
	}
	var s struct {
		Port int `json:"port"`
	}
	if json.Unmarshal(b, &s) != nil {
		return 0
	}
	return s.Port
}

// APIKey 桌面版通常不需要鉴权；有 key 就带上。
func APIKey() string {
	for _, k := range []string{"MAGPIE_API_KEY", "MAGPIE_GATEWAY_KEY"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	for _, dir := range ConfigDirs() {
		if key := keyFromCallerKeys(filepath.Join(dir, "caller-keys.json")); key != "" {
			return key
		}
	}
	return ""
}

func keyFromCallerKeys(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var keys []map[string]any
	if json.Unmarshal(b, &keys) != nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := k["secret"].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func listModels(ctx context.Context, base string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := SharedClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("bad status " + resp.Status)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// SharedClient 是所有回环请求共用的客户端；Proxy=nil 是因为系统代理会把 127.0.0.1 也劫走。
var SharedClient = &http.Client{Transport: &http.Transport{
	Proxy:                 nil,
	MaxIdleConnsPerHost:   4,
	ResponseHeaderTimeout: 60 * time.Second,
}}
