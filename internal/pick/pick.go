// Package pick 取当前选区文本。
//
// v1 只做剪贴板法（模拟 Ctrl+C），UIA TextPattern 留到 v1.1：
// 取词矩阵实测前不做两种路径，避免同时维护两套 COM 代码。
package pick

import (
	"errors"
	"strings"
	"time"

	"magpie-translate/internal/winapi"
)

var (
	ErrNoSelection   = errors.New("没选中文字")
	ErrBlacklisted   = errors.New("该应用不支持划词取词")
	ErrClipboardBusy = errors.New("剪贴板被占用")
)

// DefaultBlacklist：Ctrl+C 在这些进程里有副作用（远程会话、终端、密码管理器、复制文件）。
var DefaultBlacklist = "mstsc.exe,windowsterminal.exe,conhost.exe,openconsole.exe,keepass.exe,1password.exe,bitwarden.exe,explorer.exe"

type Picker struct {
	Blacklist map[string]bool
	Restore   bool // 取词后还原剪贴板
	Timeout   time.Duration
}

// New 用逗号分隔的进程名构造 Picker。
func New(blacklist string, restore bool) *Picker {
	set := map[string]bool{}
	for _, name := range strings.Split(blacklist, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			set[name] = true
		}
	}
	return &Picker{Blacklist: set, Restore: restore, Timeout: 400 * time.Millisecond}
}

type Result struct {
	Text   string
	Via    string // clipboard
	App    string // 前台进程名
	Millis int64
}

// Pick 取当前选区。返回 Result 的 Text 已 TrimSpace。
func (p *Picker) Pick() (Result, error) {
	start := time.Now()
	res := Result{Via: "clipboard", App: winapi.ForegroundProcessName()}
	if p.Blacklist[res.App] {
		return res, ErrBlacklisted
	}

	snapshot, err := winapi.SnapshotClipboard()
	if err != nil {
		snapshot = nil // 存不下就退化成"只保证文本不丢"，总比不翻译好
	}
	defer func() {
		if p.Restore && snapshot != nil {
			_ = winapi.RestoreClipboard(snapshot)
		}
	}()

	winapi.ReleaseModifiers()
	// 先清空，再等 Ctrl+C 写入：剪贴板是全局的，别的程序可能同时写，
	// 只有"我们清空之后出现的内容"才可能是这次选区。
	winapi.ClipboardClear()
	winapi.SendCtrlC()

	deadline := time.Now().Add(p.Timeout)
	for time.Now().Before(deadline) {
		if txt, err := winapi.ReadClipboardText(); err == nil {
			if txt = Normalize(txt); txt != "" {
				res.Text = txt
				res.Millis = time.Since(start).Milliseconds()
				return res, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	res.Millis = time.Since(start).Milliseconds()
	return res, ErrNoSelection
}

// Normalize 去掉首尾空白和拷贝带来的尾部换行，不合并中间换行（译文要保留段落）。
func Normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	return strings.TrimRight(s, "\n")
}
