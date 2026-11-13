package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"magpie-translate/internal/api"
	"magpie-translate/internal/pick"
	"magpie-translate/internal/popover"
	"magpie-translate/internal/store"
	"magpie-translate/internal/translate"
	"magpie-translate/internal/winapi"
)

const hotkeyID = 0x4D50

func main() {
	// 浮窗是 Win32 窗口，整个进程只有这一个 UI 线程。
	runtime.LockOSThread()

	var (
		portFlag     = flag.Int("port", 0, "本地 HTTP 端口（默认 3477）")
		gatewayFlag  = flag.String("gateway", "", "magpie 网关地址，如 127.0.0.1:3425")
		modelFlag    = flag.String("model", "", "模型 id，留空用网关第一个模型")
		targetFlag   = flag.String("target", "", "强制目标语言：zh / en（默认自动判定）")
		hotkeyFlag   = flag.String("hotkey", "", "全局热键，默认 Alt+D")
		blacklistFlg = flag.String("blacklist", "", "逗号分隔的进程黑名单，覆盖默认值")
		glossaryFlag = flag.String("glossary-file", "", "术语表文件，每行 原文=译文")
		glossaryText = flag.String("glossary-text", "", "术语表正文（插件设置页传进来，优先于 --glossary-file）")
		noRestore    = flag.Bool("no-restore-clipboard", false, "取词后不还原剪贴板")
		debugFlag    = flag.Bool("debug", false, "输出带微秒的调试日志")
		versionFlag  = flag.Bool("version", false, "打印版本号")
	)
	flag.Parse()

	if *versionFlag {
		fmt.Println("magpie-translate-helper", api.Version)
		return
	}

	cfg := store.Load()
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["port"] {
		cfg.Port = *portFlag
	}
	if set["gateway"] {
		cfg.Gateway = *gatewayFlag
	}
	if set["model"] {
		cfg.Model = *modelFlag
	}
	if set["target"] {
		cfg.Target = *targetFlag
	}
	if set["hotkey"] {
		cfg.Hotkey = *hotkeyFlag
	}
	if set["blacklist"] {
		cfg.Blacklist = *blacklistFlg
	}
	if set["glossary-file"] {
		// 只在启动时由本机用户读一次，接口永远只接受正文，不接受路径。
		if b, err := os.ReadFile(*glossaryFlag); err == nil {
			cfg.GlossaryText = string(b)
		}
	}
	if set["glossary-text"] {
		cfg.GlossaryText = *glossaryText
	}
	if *noRestore {
		cfg.RestoreClipboard = false
	}
	if cfg.Port == 0 {
		cfg.Port = 3477
	}

	logger, logFile := newLogger(*debugFlag)
	defer logFile.Close()
	event := logger.Printf

	// 单实例：端口占不上就说明已经有一个 helper 在跑，直接安静退出。
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		event("start skipped: port %d busy (%v)", cfg.Port, err)
		return
	}

	picker := pick.New(cfg.Blacklist, cfg.RestoreClipboard)

	var cfgMu sync.RWMutex
	current := cfg
	settings := func() store.Settings {
		cfgMu.RLock()
		defer cfgMu.RUnlock()
		return current
	}
	// 热键只能在 UI 线程重注册（WM_HOTKEY 投给注册它的线程队列），浮窗建好后才有实现。
	var regHotkey = func(string) error { return errors.New("helper 还在启动，稍后再试") }
	apply := func(next store.Settings) error {
		if next.Port == 0 {
			next.Port = cfg.Port
		}
		cfgMu.RLock()
		prev := current
		cfgMu.RUnlock()
		hotkeyChanged := next.Hotkey != prev.Hotkey
		if hotkeyChanged {
			if err := regHotkey(next.Hotkey); err != nil {
				return err // 注册不上就不落盘，页面上的值和实际行为保持一致
			}
		}
		if err := next.Save(); err != nil {
			if hotkeyChanged {
				_ = regHotkey(prev.Hotkey)
			}
			return err
		}
		cfgMu.Lock()
		current = next
		cfgMu.Unlock()
		fresh := pick.New(next.Blacklist, next.RestoreClipboard)
		picker.Blacklist = fresh.Blacklist
		picker.Restore = fresh.Restore
		event("settings applied: hotkey=%s model=%q target=%s", next.Hotkey, next.Model, next.Target)
		return nil
	}

	gateway := translate.NewGateway(cfg.Gateway)
	cache := translate.NewCache(512)

	srv := &api.Server{
		Port:     cfg.Port,
		Gateway:  gateway,
		Cache:    cache,
		Picker:   picker,
		Settings: settings,
		Apply:    apply,
		Token:    api.NewToken(),
	}
	go func() {
		event("http listening on 127.0.0.1:%d", cfg.Port)
		if err := http.Serve(ln, srv.Handler()); err != nil {
			event("http server stopped: %v", err)
		}
	}()

	pop, err := popover.New()
	if err != nil {
		event("popover init failed: %v", err)
		os.Exit(1)
	}
	srv.OnHide = pop.Hide
	pop.Log = event

	mods, vk, err := ParseHotkey(settings().Hotkey)
	if err != nil {
		event("bad hotkey %q: %v", settings().Hotkey, err)
		os.Exit(1)
	}
	if r, _, e := winapi.ProcRegisterHotKey.Call(0, hotkeyID, uintptr(mods), uintptr(vk)); r == 0 {
		event("RegisterHotKey(%s) failed: %v（可能被别的程序占用）", settings().Hotkey, e)
		os.Exit(1)
	}
	event("hotkey %s registered", settings().Hotkey)

	// 设置页改了热键就地换，不用重启 helper：Post 把活派给 UI 线程。
	regHotkey = func(hk string) error {
		prev := settings().Hotkey
		done := make(chan error, 1)
		pop.Post(func() { done <- setHotkey(hk, prev) })
		select {
		case err := <-done:
			return err
		case <-time.After(3 * time.Second):
			return errors.New("界面线程无响应，热键没有改")
		}
	}

	// 取词在后台 goroutine 跑，消息循环里绝不做 IO。
	pop.OnHotkey = func() { translateSelection(pop, srv, picker, event) }
	pop.Loop()
	event("exit")
}

func translateSelection(pop *popover.Popover, srv *api.Server, picker *pick.Picker, event func(string, ...any)) {
	pt := winapi.CursorPos()
	res, err := picker.Pick()
	if err != nil {
		event("pick failed app=%s ms=%d err=%v", res.App, res.Millis, err)
		pop.FailShow(pt, err.Error())
		return
	}
	event("pick ok app=%s via=%s ms=%d chars=%d", res.App, res.Via, res.Millis, utf8.RuneCountInString(res.Text))

	pop.Show(pt, res.Text)
	event("translate start chars=%d", utf8.RuneCountInString(res.Text))

	// 看门狗：上游慢/挂住时每 5s 记一笔，并把标题改成"等待上游"，用户能区分"卡死"和"在等"。
	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for i := 1; ; i++ {
			select {
			case <-done:
				return
			case <-tick.C:
				event("translate waiting %ds", i*5)
				pop.Progress(fmt.Sprintf("等待上游… %ds", i*5))
			}
		}
	}()
	r, err := srv.Translate(context.Background(), res.Text, "", "", pop.Append)
	close(done)
	switch {
	case err == nil:
		event("translate done ms=%d chars=%d cached=%v", r.Millis, utf8.RuneCountInString(r.Text), r.Cached)
		pop.Done(label(r.Target, r.Cached, r.Truncated))
	case errors.Is(err, api.ErrTooShort):
		event("translate skipped: too short")
		pop.Fail("没选中文字")
	case errors.Is(err, translate.ErrInterrupted):
		event("translate interrupted ms=%d chars=%d", r.Millis, utf8.RuneCountInString(r.Text))
		pop.Done(label(r.Target, false, false) + " · 连接中断")
	default:
		event("translate failed ms=%d err=%v", r.Millis, err)
		pop.Fail(api.Message(err))
	}
}

func label(target string, cached, truncated bool) string {
	s := "EN → 中文"
	if target == "en" {
		s = "中文 → EN"
	}
	if cached {
		s += " · 缓存"
	}
	if truncated {
		s += " · 已截断"
	}
	return s
}

// setHotkey 换生效的热键，只能在 UI 线程调用（RegisterHotKey(hWnd=0) 把 WM_HOTKEY
// 投给调用它的那个线程的队列）。新的注册不上就把旧的装回去，别让用户连旧的也没了。
func setHotkey(next, prev string) error {
	mods, vk, err := ParseHotkey(next)
	if err != nil {
		return err
	}
	winapi.ProcUnregisterHotKey.Call(0, hotkeyID)
	r, _, callErr := winapi.ProcRegisterHotKey.Call(0, hotkeyID, uintptr(mods), uintptr(vk))
	if r != 0 {
		return nil
	}
	if pm, pv, e := ParseHotkey(prev); e == nil && prev != next {
		winapi.ProcRegisterHotKey.Call(0, hotkeyID, uintptr(pm), uintptr(pv))
	}
	return fmt.Errorf("%s 注册失败（可能被别的程序占用）：%v", next, callErr)
}

// ParseHotkey 把 "Alt+D" 之类的字符串转成 RegisterHotKey 的 (modifiers, vk)。
func ParseHotkey(s string) (mods uint32, vk uint32, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "+")
	if len(parts) == 0 || strings.TrimSpace(parts[len(parts)-1]) == "" {
		return 0, 0, errors.New("空的快捷键")
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	for _, p := range parts[:len(parts)-1] {
		switch strings.TrimSpace(p) {
		case "alt":
			mods |= winapi.ModAlt
		case "ctrl", "control":
			mods |= winapi.ModCtrl
		case "shift":
			mods |= winapi.ModShift
		case "win":
			mods |= winapi.ModWin
		case "":
		default:
			return 0, 0, fmt.Errorf("未知修饰键 %q", p)
		}
	}
	switch {
	case len(key) == 1 && key[0] >= 'a' && key[0] <= 'z':
		vk = uint32(key[0] - 'a' + 'A')
	case len(key) == 1 && key[0] >= '0' && key[0] <= '9':
		vk = uint32(key[0])
	case len(key) >= 2 && key[0] == 'f':
		var n int
		if _, err := fmt.Sscanf(key, "f%d", &n); err != nil || n < 1 || n > 24 {
			return 0, 0, fmt.Errorf("未知按键 %q", key)
		}
		vk = uint32(0x70 + n - 1)
	default:
		return 0, 0, fmt.Errorf("未知按键 %q", key)
	}
	return mods, vk, nil
}

// newLogger 写 %LOCALAPPDATA%\magpie-translate\helper.log。
// 只记时间/事件/耗时/字数，绝不记原文。
func newLogger(debug bool) (*log.Logger, io.Closer) {
	dir := store.Dir()
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "helper.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		if debug {
			return log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds), io.NopCloser(nil)
		}
		return log.New(io.Discard, "", 0), io.NopCloser(nil)
	}
	flags := log.LstdFlags
	if debug {
		flags |= log.Lmicroseconds
	}
	return log.New(f, "", flags), f
}
