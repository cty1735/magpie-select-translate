package winapi

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")

	ProcRegisterClassExW = user32.NewProc("RegisterClassExW")
	ProcCreateWindowExW  = user32.NewProc("CreateWindowExW")
	ProcDefWindowProcW   = user32.NewProc("DefWindowProcW")
	ProcDestroyWindow    = user32.NewProc("DestroyWindow")
	ProcSetWindowTextW   = user32.NewProc("SetWindowTextW")
	ProcSendMessageW     = user32.NewProc("SendMessageW")
	ProcGetDC            = user32.NewProc("GetDC")
	ProcReleaseDC        = user32.NewProc("ReleaseDC")
	ProcDrawTextW        = user32.NewProc("DrawTextW")
	ProcMoveWindow       = user32.NewProc("MoveWindow")
	ProcSetWindowRgn     = user32.NewProc("SetWindowRgn")
	ProcGetWindowRect    = user32.NewProc("GetWindowRect")
	ProcScreenToClient   = user32.NewProc("ScreenToClient")
	ProcShowScrollBar    = user32.NewProc("ShowScrollBar")
	ProcFillRect         = user32.NewProc("FillRect")
	ProcBeginPaint       = user32.NewProc("BeginPaint")
	ProcEndPaint         = user32.NewProc("EndPaint")
	ProcInvalidateRect   = user32.NewProc("InvalidateRect")
	ProcTrackMouseEvent  = user32.NewProc("TrackMouseEvent")
	ProcLoadCursorW      = user32.NewProc("LoadCursorW")
	ProcSetCursor        = user32.NewProc("SetCursor")
	ProcGetCursor        = user32.NewProc("GetCursor")
	ProcIsWindowVisible  = user32.NewProc("IsWindowVisible")
	ProcPostQuitMessage  = user32.NewProc("PostQuitMessage")
	ProcGetSystemMetrics = user32.NewProc("GetSystemMetrics")

	ProcCreateFontW        = gdi32.NewProc("CreateFontW")
	ProcSetBkColor         = gdi32.NewProc("SetBkColor")
	ProcCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	ProcSetBkMode          = gdi32.NewProc("SetBkMode")
	ProcSetTextColor       = gdi32.NewProc("SetTextColor")
	ProcCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	ProcDeleteObject       = gdi32.NewProc("DeleteObject")
	ProcSelectObject       = gdi32.NewProc("SelectObject")
)

type WndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

const (
	WS_CHILD       = 0x40000000
	WS_VISIBLE     = 0x10000000
	WS_VSCROLL     = 0x00200000
	WS_BORDER      = 0x00800000
	ES_MULTILINE   = 0x0004
	ES_AUTOVSCROLL = 0x0040
	ES_READONLY    = 0x0800
	ES_NOHIDESEL   = 0x0100
	BS_PUSHBUTTON  = 0x00000000
	BS_OWNERDRAW   = 0x0000000B
	SS_LEFT        = 0x00000000
	SS_RIGHT       = 0x00000002

	IDCArrow = 32512
	IDCHand  = 32649

	DT_LEFT         = 0x0000
	DT_WORDBREAK    = 0x0010
	DT_CALCRECT     = 0x0400
	DT_NOPREFIX     = 0x0800
	DT_EDITCONTROL  = 0x2000
	DT_END_ELLIPSIS = 0x8000

	EM_SETSEL      = 0x00B1
	EM_SCROLLCARET = 0x00B7

	WM_DESTROY        = 0x0002
	WM_MOVE           = 0x0003
	WM_SIZE           = 0x0005
	WM_CLOSE          = 0x0010
	WM_COMMAND        = 0x0111
	WM_NCHITTEST      = 0x0084
	WM_SETCURSOR      = 0x0020
	WM_DRAWITEM       = 0x002B
	WM_CTLCOLORSTATIC = 0x0138
	WM_CTLCOLOREDIT   = 0x0133
	WM_SETFONT        = 0x0030

	HTCAPTION    = 2
	HTCLIENT     = 1
	ODS_SELECTED = 0x0001
	ODS_DISABLED = 0x0004

	DT_CENTER     = 0x0001
	DT_VCENTER    = 0x0004
	DT_SINGLELINE = 0x0020

	SB_VERT = 1
)

// DrawItemStruct 对应 Win32 的 DRAWITEMSTRUCT（x64 布局）。
type DrawItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   uintptr
	HDC        uintptr
	RcItem     Rect
	ItemData   uintptr
}

// ScreenPointToClient 屏幕坐标 → 窗口客户区坐标。
func ScreenPointToClient(hwnd uintptr, p Point) Point {
	ProcScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	return p
}

// WindowRect 取窗口在屏幕上的矩形。
func WindowRect(hwnd uintptr) Rect {
	var r Rect
	ProcGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

// ShowScrollBar 控制滚动条显隐（内容不够长时藏掉，省得右侧一条空槽）。
func ShowScrollBar(hwnd uintptr, show bool) {
	v := uintptr(0)
	if show {
		v = 1
	}
	ProcShowScrollBar.Call(hwnd, SB_VERT, v)
}

// UTF16 返回字符串的 UTF-16 指针。
// 调用方必须把返回值活到 Win32 调用结束（用 runtime.KeepAlive），
// 否则 GC 可能在调用中途回收缓冲区——这会让窗口文字偶发变空或乱码。
func UTF16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString("")
	}
	return p
}

// ModuleHandle 拿当前 exe 的 HINSTANCE。
func ModuleHandle() uintptr {
	h, _, _ := ProcGetModuleHandleW.Call(0)
	return h
}

// CreateFontPx 创建指定像素字号、指定粗体的字体（比磅值直观，省掉 DPI 换算误差）。
func CreateFontPx(px int, bold bool) uintptr {
	weight := uintptr(400)
	if bold {
		weight = 600
	}
	face := UTF16("Microsoft YaHei UI")
	defer runtime.KeepAlive(face)
	h, _, _ := ProcCreateFontW.Call(
		uintptr(-px), 0, 0, 0, weight,
		0, 0, 0,
		uintptr(1), // DEFAULT_CHARSET
		0, 0, 0, 0,
		uintptr(unsafe.Pointer(face)),
	)
	return h
}

// MeasureText 用给定字体测量折行后需要的高度（像素）。
func MeasureText(hwnd uintptr, text string, font uintptr, width, maxHeight int) int {
	if text == "" {
		return 0
	}
	hdc, _, _ := ProcGetDC.Call(hwnd)
	if hdc == 0 {
		return 0
	}
	defer ProcReleaseDC.Call(hwnd, hdc)
	old, _, _ := ProcSelectObject.Call(hdc, font)
	defer ProcSelectObject.Call(hdc, old)

	ptr := UTF16(text)
	defer runtime.KeepAlive(ptr)
	r := Rect{Right: int32(width), Bottom: int32(maxHeight)}
	// DT_CALCRECT 直接把需要的高度写回 rect.Bottom。
	ProcDrawTextW.Call(hdc, uintptr(unsafe.Pointer(ptr)), ^uintptr(0),
		uintptr(unsafe.Pointer(&r)),
		DT_CALCRECT|DT_WORDBREAK|DT_EDITCONTROL|DT_NOPREFIX)
	return int(r.Bottom)
}

// SetWindowText 更新控件文本。
func SetWindowText(hwnd uintptr, s string) {
	ptr := UTF16(s)
	defer runtime.KeepAlive(ptr)
	ProcSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(ptr)))
}

// ScrollToEnd 让只读多行编辑框滚到底部。
func ScrollToEnd(hwnd uintptr) {
	ProcSendMessageW.Call(hwnd, EM_SETSEL, ^uintptr(0), ^uintptr(0))
	ProcSendMessageW.Call(hwnd, EM_SCROLLCARET, 0, 0)
}

// SetFont 设置控件字体。
func SetFont(hwnd, font uintptr) {
	ProcSendMessageW.Call(hwnd, WM_SETFONT, font, 1)
}

// SetRoundedRegion 给窗口加圆角（重复调用会替换旧区域）。
func SetRoundedRegion(hwnd uintptr, w, h, radius int) {
	if w <= 0 || h <= 0 {
		return
	}
	rgn, _, _ := ProcCreateRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(h+1), uintptr(radius), uintptr(radius))
	if rgn != 0 {
		ProcSetWindowRgn.Call(hwnd, rgn, 1)
	}
}

// PaintStruct 对应 Win32 的 PAINTSTRUCT。
type PaintStruct struct {
	HDC         uintptr
	FErase      int32
	RcPaint     Rect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type trackMouseEvent struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   uintptr
	DwHoverTime uint32
}

const TME_LEAVE = 0x0002

// BeginPaint 开始一次绘制，返回 HDC。
func BeginPaint(hwnd uintptr) (uintptr, *PaintStruct) {
	ps := &PaintStruct{}
	hdc, _, _ := ProcBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(ps)))
	return hdc, ps
}

// EndPaint 结束绘制。
func EndPaint(hwnd uintptr, ps *PaintStruct) {
	ProcEndPaint.Call(hwnd, uintptr(unsafe.Pointer(ps)))
}

// Invalidate 让整个窗口重画（按钮 hover 状态变化时用）。
func Invalidate(hwnd uintptr) {
	ProcInvalidateRect.Call(hwnd, 0, 0)
}

// TrackLeave 让窗口在鼠标离开时收到 WM_MOUSELEAVE（默认收不到）。
func TrackLeave(hwnd uintptr) {
	t := trackMouseEvent{CbSize: uint32(unsafe.Sizeof(trackMouseEvent{})), DwFlags: TME_LEAVE, HwndTrack: hwnd}
	ProcTrackMouseEvent.Call(uintptr(unsafe.Pointer(&t)))
}

// FillRect 用画刷填一个矩形。
func FillRect(hdc uintptr, r *Rect, brush uintptr) {
	ProcFillRect.Call(hdc, uintptr(unsafe.Pointer(r)), brush)
}

// TextOut 在矩形里居中画一行文字。
func TextOut(hdc uintptr, text string, r *Rect, font, color uintptr) {
	winapiSetTextColor(hdc, color)
	ptr := UTF16(text)
	defer runtime.KeepAlive(ptr)
	old, _, _ := ProcSelectObject.Call(hdc, font)
	defer ProcSelectObject.Call(hdc, old)
	ProcDrawTextW.Call(hdc, uintptr(unsafe.Pointer(ptr)), ^uintptr(0),
		uintptr(unsafe.Pointer(r)),
		DT_CENTER|DT_VCENTER|DT_SINGLELINE|DT_NOPREFIX)
}

func winapiSetTextColor(hdc, color uintptr) {
	ProcSetBkMode.Call(hdc, 1) // TRANSPARENT
	ProcSetTextColor.Call(hdc, color)
}
