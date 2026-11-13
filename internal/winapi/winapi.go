// Package winapi 只封装本工具真正用到的 Win32 调用，避免散落各处的 LazyDLL 声明。
package winapi

import (
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Point struct{ X, Y int32 }

type Rect struct{ Left, Top, Right, Bottom int32 }

type MonitorInfo struct {
	CbSize    uint32
	RcMonitor Rect
	RcWork    Rect
	DwFlags   uint32
}

type Msg struct {
	HWnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       Point
	LPrivate uint32
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	ProcGetCursorPos             = user32.NewProc("GetCursorPos")
	ProcMonitorFromPoint         = user32.NewProc("MonitorFromPoint")
	ProcGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	ProcGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	ProcGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	ProcGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	ProcGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	ProcSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	ProcGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	ProcShowWindow               = user32.NewProc("ShowWindow")
	ProcSetWindowPos             = user32.NewProc("SetWindowPos")
	ProcGetMessageW              = user32.NewProc("GetMessageW")
	ProcTranslateMessage         = user32.NewProc("TranslateMessage")
	ProcDispatchMessageW         = user32.NewProc("DispatchMessageW")
	ProcPostThreadMessageW       = user32.NewProc("PostThreadMessageW")
	ProcRegisterHotKey           = user32.NewProc("RegisterHotKey")
	ProcUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	ProcSetWindowsHookExW        = user32.NewProc("SetWindowsHookExW")
	ProcUnhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	ProcCallNextHookEx           = user32.NewProc("CallNextHookEx")
	ProcKeybdEvent               = user32.NewProc("keybd_event")
	ProcMapVirtualKeyW           = user32.NewProc("MapVirtualKeyW")
	ProcGetKeyState              = user32.NewProc("GetKeyState")
	ProcGetAsyncKeyState         = user32.NewProc("GetAsyncKeyState")
	ProcGetCurrentThreadID       = kernel32.NewProc("GetCurrentThreadId")
	ProcGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
)

const (
	SWHide           = 0
	SWShowNoActivate = 4

	SWPNoSize       = 0x0001
	SWPNoMove       = 0x0002
	SWPNoZOrder     = 0x0004
	SWPNoActivate   = 0x0010
	SWPFrameChanged = 0x0020

	WS_POPUP         = 0x80000000
	WS_EX_TOPMOST    = 0x00000008
	WS_EX_TOOLWINDOW = 0x00000080
	WS_EX_NOACTIVATE = 0x08000000

	GWLPSTYLE   = ^uintptr(15) // -16
	GWL_EXSTYLE = ^uintptr(19) // -20

	HWND_TOPMOST = ^uintptr(0) // -1

	MonitorDefaultToNearest = 2

	ModAlt   = 0x0001
	ModCtrl  = 0x0002
	ModShift = 0x0004
	ModWin   = 0x0008

	WM_HOTKEY      = 0x0312
	WM_KEYDOWN     = 0x0100
	WM_SYSKEYDOWN  = 0x0104
	WM_LBUTTONDOWN = 0x0201
	WM_LBUTTONUP   = 0x0202
	WH_KEYBOARD_LL = 13
	VKEscape       = 0x1B

	KEYEVENTF_KEYUP = 0x0002

	VKShift = 0x10
	VKCtrl  = 0x11
	VKAlt   = 0x12
	VKLWin  = 0x5B
	VKRWin  = 0x5C
	VKEsc   = VKEscape
	VKC     = 0x43

	PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
)

// CursorPos 返回当前鼠标屏幕坐标（物理像素）。
func CursorPos() Point {
	var p Point
	_, _, _ = ProcGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

// WorkArea 返回鼠标所在显示器的工作区（已排除任务栏）。
func WorkArea(pt Point) Rect {
	h, _, _ := ProcMonitorFromPoint.Call(uintptr(int32(pt.X)), uintptr(int32(pt.Y)), MonitorDefaultToNearest)
	mi := MonitorInfo{CbSize: uint32(unsafe.Sizeof(MonitorInfo{}))}
	_, _, _ = ProcGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
	return mi.RcWork
}

// DPI 返回窗口（或系统）的 DPI，失败时当 96 处理。
func DPI(hwnd uintptr) uint32 {
	if hwnd != 0 {
		if d, _, _ := ProcGetDpiForWindow.Call(hwnd); d > 0 {
			return uint32(d)
		}
	}
	if d, _, _ := ProcGetDpiForSystem.Call(); d > 0 {
		return uint32(d)
	}
	return 96
}

// ForegroundProcessName 返回前台窗口所属进程的文件名（小写，如 "chrome.exe"）。
func ForegroundProcessName() string {
	hwnd, _, _ := ProcGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	_, _, _ = ProcGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	full := windows.UTF16ToString(buf[:n])
	if i := strings.LastIndexAny(full, `\/`); i >= 0 {
		full = full[i+1:]
	}
	return strings.ToLower(full)
}

// scanCode 把虚拟键换成硬件扫描码。
// Chromium 系（Chrome/Edge/Electron）按扫描码识别按键：wScan=0 的 keyup
// 清不掉它自己的修饰键状态，合成的 Ctrl+C 会被当成 Ctrl+Alt+C 而失效。
func scanCode(vk uintptr) uintptr {
	sc, _, _ := ProcMapVirtualKeyW.Call(vk, 0) // MAPVK_VK_TO_VSC
	return sc
}

func key(vk uintptr, up bool) {
	flags := uintptr(0)
	if up {
		flags = KEYEVENTF_KEYUP
	}
	_, _, _ = ProcKeybdEvent.Call(vk, scanCode(vk), flags, 0)
}

// ReleaseModifiers 等用户松开热键的修饰键，再兜底补 keyup。
// 热键触发时 Alt 还按着，直接合成 Ctrl+C 会变成 Ctrl+Alt+C，很多程序不认。
// 必须用 GetAsyncKeyState：GetKeyState 只反映调用线程自己的按键状态，
// 而取词跑在新建的 goroutine 线程上，那里永远是"没按"。
// 优先等物理松开：Chrome 收到 Alt 按下后会进菜单态，合成的 keyup 清不掉它，
// 后续 Ctrl+C 被吞（实测），只有真的 keyup 才能退出。
func ReleaseModifiers() {
	if waitModifiersUp(400 * time.Millisecond) {
		return
	}
	for _, vk := range []uintptr{VKAlt, VKCtrl, VKShift, VKLWin, VKRWin} {
		if modifierDown(vk) {
			key(vk, true)
		}
	}
	// 留一点时间让目标窗口处理 keyup，再发 Ctrl+C（否则 Notepad 之类还在菜单态）。
	time.Sleep(40 * time.Millisecond)
}

func modifierDown(vk uintptr) bool {
	state, _, _ := ProcGetAsyncKeyState.Call(vk)
	return int16(state) < 0 // 高位为 1 表示正按下
}

func waitModifiersUp(max time.Duration) bool {
	for deadline := time.Now().Add(max); ; {
		if !modifierDown(VKAlt) && !modifierDown(VKCtrl) && !modifierDown(VKShift) &&
			!modifierDown(VKLWin) && !modifierDown(VKRWin) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// SendCtrlC 合成一次 Ctrl+C。调用前必须 ReleaseModifiers。
func SendCtrlC() {
	key(VKCtrl, false)
	key(VKC, false)
	key(VKC, true)
	key(VKCtrl, true)
}

// SendEsc 合成一次 Esc：把停在 Alt 菜单态的窗口踢回正常状态，不影响选区。
func SendEsc() {
	key(VKEsc, false)
	key(VKEsc, true)
}

// CurrentThreadID 供消息循环投递 WM_APP 用。
func CurrentThreadID() uint32 {
	r, _, _ := ProcGetCurrentThreadID.Call()
	return uint32(r)
}

// PostThreadMsg 向指定线程投递自定义消息。
func PostThreadMsg(threadID uint32, msg uintptr) {
	_, _, _ = ProcPostThreadMessageW.Call(uintptr(threadID), msg, 0, 0)
}
