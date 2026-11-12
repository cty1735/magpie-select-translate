// Package popover 是常驻的翻译浮窗：无焦点弹窗 + 只读多行编辑框，按钮自己画。
//
// 不用系统 BUTTON 控件：Win11 的经典按钮在无边框弹窗里很突兀，且拿不到 hover 反馈；
// 自绘两小块矩形，hover/pressed 都由我们控制，代码还更短。
package popover

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"magpie-translate/internal/winapi"
)

const (
	widthLogical   = 420
	minBodyLogical = 30
	maxBodyLogical = 340
	maxSrcLogical  = 40

	idCopy  = 1
	idClose = 2

	wmAppUI   = 0x8001
	wmAppQuit = 0x8002

	WM_MOUSELEAVE  = 0x02A3
	WM_PAINT       = 0x000F
	WM_MOUSEMOVE   = 0x0200
	WM_LBUTTONDOWN = 0x0201
	WM_LBUTTONUP   = 0x0202
)

// kbdllhookstruct 对应 Win32 的 KBDLLHOOKSTRUCT（低级键盘钩子的 lParam）。
type kbdllhookstruct struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type Popover struct {
	hwnd   uintptr
	thread uint32
	dpi    uint32

	hBody, hFooter uintptr
	fontBig, fontSmall      uintptr
	bgBrush, hoverBrush     uintptr
	pressBrush, borderBrush uintptr
	fg, dim                 uintptr

	clientRect winapi.Rect
	copyRect   winapi.Rect
	closeRect  winapi.Rect
	headerRect winapi.Rect
	headerText string
	hoverBtn   int
	pressBtn   int
	copyLabel  string
	handCursor uintptr

	mu      sync.Mutex
	queue   []func()
	source  string
	body    string
	visible bool
	lastPt  winapi.Point
	hook    uintptr

	// manual 表示用户手动拖过窗口，后续内容变化只改高度不挪位置；
	// programmatic 用来区分"我们自己摆的位置"和"用户拖动"。
	manual       bool
	programmatic bool

	// Log 可选：nil 时静默。用来确认 UI 线程确实在处理队列。
	Log func(string, ...any)

	OnHotkey func()
}

// New 必须在 LockOSThread 的主线程调用；之后所有窗口操作都在这个消息循环线程上。
func New() (*Popover, error) {
	p := &Popover{thread: winapi.CurrentThreadID(), dpi: winapi.DPI(0), copyLabel: "复制"}
	p.fg, p.dim, p.bgBrush, p.hoverBrush, p.pressBrush, p.borderBrush = theme()

	className := winapi.UTF16("magpie-translate-popover")
	title := winapi.UTF16("translate")
	defer runtime.KeepAlive(className)
	defer runtime.KeepAlive(title)

	cursor, _, _ := winapi.ProcLoadCursorW.Call(0, winapi.IDCArrow)
	p.handCursor, _, _ = winapi.ProcLoadCursorW.Call(0, winapi.IDCHand)
	wc := winapi.WndClassExW{
		CbSize:        uint32(unsafe.Sizeof(winapi.WndClassExW{})),
		LpfnWndProc:   syscall.NewCallback(func(h, m, w, l uintptr) uintptr { return p.wndproc(h, m, w, l) }),
		HInstance:     winapi.ModuleHandle(),
		HCursor:       cursor,
		HbrBackground: p.bgBrush,
		LpszClassName: className,
	}
	if r, _, _ := winapi.ProcRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return nil, errno("RegisterClassExW")
	}

	exStyle := uintptr(winapi.WS_EX_NOACTIVATE | winapi.WS_EX_TOOLWINDOW | winapi.WS_EX_TOPMOST)
	hwnd, _, e := winapi.ProcCreateWindowExW.Call(exStyle,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		uintptr(winapi.WS_POPUP), 0, 0, uintptr(p.px(widthLogical)), uintptr(p.px(120)),
		0, 0, winapi.ModuleHandle(), 0)
	if hwnd == 0 {
		return nil, e
	}
	p.hwnd = hwnd

	p.fontBig = winapi.CreateFontPx(p.px(15), false)
	p.fontSmall = winapi.CreateFontPx(p.px(12), false)

	p.hBody = p.child("EDIT", winapi.WS_CHILD|winapi.WS_VISIBLE|winapi.WS_VSCROLL|
		winapi.ES_MULTILINE|winapi.ES_READONLY|winapi.ES_AUTOVSCROLL|winapi.ES_NOHIDESEL)
	p.hFooter = p.child("STATIC", winapi.WS_CHILD|winapi.WS_VISIBLE|winapi.SS_LEFT)

	winapi.SetFont(p.hBody, p.fontBig)
	winapi.SetFont(p.hFooter, p.fontSmall)

	p.offscreen(hwnd)
	// 先挪到屏幕外再显示：窗口必须是 WS_VISIBLE 才会绘制，隐藏状态下控件不会画。
	winapi.ProcShowWindow.Call(p.hwnd, winapi.SWShowNoActivate)
	return p, nil
}

func (p *Popover) child(class string, style uintptr) uintptr {
	name := winapi.UTF16(class)
	defer runtime.KeepAlive(name)
	h, _, _ := winapi.ProcCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), 0, style,
		0, 0, 0, 0, p.hwnd, 0, winapi.ModuleHandle(), 0)
	return h
}

// Loop 跑主线程消息循环，直到窗口销毁。
func (p *Popover) Loop() {
	var msg winapi.Msg
	for {
		r, _, _ := winapi.ProcGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		switch msg.Message {
		case winapi.WM_HOTKEY:
			if p.OnHotkey != nil {
				go p.OnHotkey()
			}
		case wmAppUI:
			p.drain()
		case wmAppQuit:
			winapi.ProcDestroyWindow.Call(p.hwnd)
		default:
			winapi.ProcTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			winapi.ProcDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}
}

func (p *Popover) Post(f func()) {
	p.mu.Lock()
	p.queue = append(p.queue, f)
	p.mu.Unlock()
	winapi.PostThreadMsg(p.thread, wmAppUI)
}

func (p *Popover) drain() {
	p.mu.Lock()
	q := p.queue
	p.queue = nil
	p.mu.Unlock()
	for _, f := range q {
		f()
	}
}

// Show 在鼠标附近弹出浮窗，先显示"翻译中…"。
func (p *Popover) Show(pt winapi.Point, source string) {
	p.Post(func() {
		p.lastPt = pt
		p.source = source
		p.body = ""
		p.visible = true
		p.manual = false // 新的一次翻译重新跟随鼠标
		p.copyLabel = "复制"
		p.hoverBtn, p.pressBtn = 0, 0
		winapi.SetWindowText(p.hFooter, source)
		winapi.SetWindowText(p.hBody, "翻译中…")
		p.setHeader("翻译中…")
		p.layout()
		p.hookKeyboard()
	})
}

// Append 追加一段译文（流式），并让编辑框滚到底。
func (p *Popover) Append(delta string) {
	p.Post(func() {
		p.body += delta
		winapi.SetWindowText(p.hBody, p.body)
		p.layout()
		winapi.ScrollToEnd(p.hBody)
	})
}

// Done 收尾：更新标题（语言对 / 缓存 / 中断）。
func (p *Popover) Done(label string) {
	p.Post(func() { p.setHeader(label) })
}

// Progress 在还没有任何增量时更新标题（上游慢的时候，别让用户以为卡死了）。
func (p *Popover) Progress(text string) {
	p.Post(func() {
		if p.body == "" {
			p.setHeader(text)
		}
	})
}

// Fail 显示错误文案，正文就是错误信息（不再追加流式内容）。
func (p *Popover) Fail(message string) {
	p.Post(func() {
		p.body = message
		winapi.SetWindowText(p.hBody, message)
		p.setHeader("—")
		p.layout()
	})
}

// FailShow 先在鼠标处弹出浮窗再显示错误（取词阶段失败时窗口还没显示）。
func (p *Popover) FailShow(pt winapi.Point, message string) {
	p.Show(pt, "")
	p.Fail(message)
}

// Hide 隐藏浮窗并摘掉键盘钩子。
func (p *Popover) Hide() {
	p.Post(func() { p.hide() })
}

func (p *Popover) hide() {
	if p.hook != 0 {
		winapi.ProcUnhookWindowsHookEx.Call(p.hook)
		p.hook = 0
	}
	p.visible = false
	p.hoverBtn, p.pressBtn = 0, 0
	p.offscreen(p.hwnd)
}

// hookKeyboard 只在浮窗可见时挂 LL 键盘钩子，只吞 Esc。
// shortcut: 全局键盘钩子会被部分安全软件盯上；要更"干净"可改成点击浮窗外关闭。
func (p *Popover) hookKeyboard() {
	if p.hook != 0 {
		return
	}
	cb := syscall.NewCallback(func(code, wparam, lparam uintptr) uintptr {
		if code >= 0 && (wparam == winapi.WM_KEYDOWN || wparam == winapi.WM_SYSKEYDOWN) {
			// lparam 指向 KBDLLHOOKSTRUCT，早先按位掩码读指针低字节是错的：
			// 任何键只要地址低字节碰巧是 0x1B 就会把浮窗关掉。
			kb := (*kbdllhookstruct)(unsafe.Pointer(lparam))
			if kb.VkCode == winapi.VKEscape {
				p.Post(func() { p.hide() })
				return 1
			}
		}
		r, _, _ := winapi.ProcCallNextHookEx.Call(0, code, wparam, lparam)
		return r
	})
	h, _, _ := winapi.ProcSetWindowsHookExW.Call(winapi.WH_KEYBOARD_LL, cb, 0, 0)
	p.hook = h
}

func (p *Popover) setHeader(s string) {
	p.headerText = s
	winapi.Invalidate(p.hwnd)
}

// layout 按内容量算高度并摆放所有控件（全部用物理像素）。
func (p *Popover) layout() {
	pad := p.px(12)
	headerH := p.px(22)
	btnH := p.px(22)
	copyW := p.px(48)
	closeW := p.px(28)
	gap := p.px(2)
	width := p.px(widthLogical)
	inner := width - 2*pad

	// 按钮自己画，只算矩形；右上角两块。
	p.closeRect = winapi.Rect{
		Left: int32(width - pad - closeW), Top: int32(p.px(6)),
		Right: int32(width - pad), Bottom: int32(p.px(6) + btnH),
	}
	p.copyRect = winapi.Rect{
		Left: p.closeRect.Left - int32(copyW) - int32(gap), Top: p.closeRect.Top,
		Right: p.closeRect.Left - int32(gap), Bottom: p.closeRect.Bottom,
	}

	// 按钮和标题都在父窗口自绘：STATIC 子控件会挡住拖动命中测试，
	// 而且设了 WS_EX_TRANSPARENT 后标题经常整块不刷（残影、字消失）。
	p.headerRect = winapi.Rect{
		Left: int32(pad), Top: int32(p.px(6)),
		Right: p.copyRect.Left - int32(gap), Bottom: p.copyRect.Bottom,
	}

	bodyH := p.px(minBodyLogical)
	if p.body != "" {
		h := winapi.MeasureText(p.hwnd, p.body, p.fontBig, inner-p.px(10), p.px(maxBodyLogical)) + p.px(6)
		// 高度按整行取整：半行会被窗口下边缘切掉，看着像内容丢了。
		if line := winapi.MeasureText(p.hwnd, "测", p.fontBig, 1000, 1000); line > 1 {
			h = h / line * line
		}
		if h < p.px(minBodyLogical) {
			h = p.px(minBodyLogical)
		}
		if h > p.px(maxBodyLogical) {
			h = p.px(maxBodyLogical)
		}
		bodyH = h
	}
	bodyY := p.px(9) + headerH
	winapi.ProcMoveWindow.Call(p.hBody, uintptr(pad), uintptr(bodyY), uintptr(inner), uintptr(bodyH), 1)
	// WS_VSCROLL 常驻：短文本时滚动条是灰的，长文本可以拖，用户不用猜有没有更多内容。

	y := bodyY + bodyH + p.px(4)
	if p.source != "" {
		srcH := winapi.MeasureText(p.hwnd, p.source, p.fontSmall, inner-p.px(10), p.px(maxSrcLogical)) + p.px(4)
		if srcH > p.px(maxSrcLogical) {
			srcH = p.px(maxSrcLogical)
		}
		winapi.ProcMoveWindow.Call(p.hFooter, uintptr(pad), uintptr(y), uintptr(inner), uintptr(srcH), 1)
		y += srcH
	} else {
		winapi.ProcMoveWindow.Call(p.hFooter, 0, 0, 0, 0, 1)
	}

	height := y + pad - 4
	p.clientRect = winapi.Rect{Right: int32(width), Bottom: int32(height)}
	p.place(width, height)
	// 整窗擦除重绘：只靠 MoveWindow 的重绘位，高度变小/内容滚动时会留下上一次的残影。
	winapi.InvalidateErase(p.hwnd)
}

// place 按鼠标位置摆放主窗口，超出工作区就翻到另一侧。
func (p *Popover) place(width, height int) {
	x, y := 0, 0
	if p.manual {
		// 用户自己拖过：只改高度，左上角原地不动。
		r := winapi.WindowRect(p.hwnd)
		x, y = int(r.Left), int(r.Top)
	} else {
		work := winapi.WorkArea(p.lastPt)
		padX, padY := p.px(16), p.px(20)
		x = int(p.lastPt.X) + padX
		y = int(p.lastPt.Y) + padY
		if x+width > int(work.Right) {
			x = int(p.lastPt.X) - width - padX
		}
		if y+height > int(work.Bottom) {
			y = int(p.lastPt.Y) - height - padY
		}
		if x < int(work.Left) {
			x = int(work.Left) + 8
		}
		if y < int(work.Top) {
			y = int(work.Top) + 8
		}
	}
	p.programmatic = true
	winapi.ProcMoveWindow.Call(p.hwnd, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
	p.programmatic = false
	winapi.SetRoundedRegion(p.hwnd, width, height, p.px(12))
}

func (p *Popover) offscreen(hwnd uintptr) {
	const off = 0xFFFFB1E0 // -20000 的 32 位补码
	winapi.ProcMoveWindow.Call(hwnd, off, off, uintptr(p.px(widthLogical)), uintptr(p.px(120)), 1)
}

// wndproc 处理自绘按钮、标题栏拖动、颜色和销毁。
func (p *Popover) wndproc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case WM_PAINT:
		hdc, ps := winapi.BeginPaint(hwnd)
		winapi.FillRect(hdc, &p.clientRect, p.bgBrush)
		winapi.FrameRect(hdc, &p.clientRect, p.borderBrush)
		winapi.TextLeft(hdc, p.headerText, &p.headerRect, p.fontSmall, p.dim)
		p.paintButton(hdc, p.copyRect, idCopy, p.copyLabel)
		p.paintButton(hdc, p.closeRect, idClose, "✕")
		winapi.EndPaint(hwnd, ps)
		return 0

	case WM_MOUSEMOVE:
		if !p.visible {
			break
		}
		winapi.TrackLeave(hwnd)
		if b := p.buttonAt(clientPoint(lp)); b != p.hoverBtn {
			p.hoverBtn = b
			winapi.Invalidate(hwnd)
		}
		return 0

	case winapi.WM_SETCURSOR:
		// 鼠标在按钮上时换成小手，让人一眼知道能点。
		if p.visible && p.buttonAt(winapi.ScreenPointToClient(hwnd, winapi.CursorPos())) != 0 {
			winapi.ProcSetCursor.Call(p.handCursor)
			return 1
		}
		break

	case WM_MOUSELEAVE:
		if p.hoverBtn != 0 {
			p.hoverBtn = 0
			winapi.Invalidate(hwnd)
		}
		return 0

	case WM_LBUTTONDOWN:
		if b := p.buttonAt(clientPoint(lp)); b != 0 {
			p.pressBtn = b
			winapi.Invalidate(hwnd)
			return 0
		}
		break

	case WM_LBUTTONUP:
		b := p.buttonAt(clientPoint(lp))
		pressed := p.pressBtn
		p.pressBtn = 0
		if pressed != 0 {
			winapi.Invalidate(hwnd)
			if b == pressed {
				p.activate(b)
			}
			return 0
		}
		break

	case winapi.WM_NCHITTEST:
		// 顶部一条当标题栏，按住就能拖动；按钮位置除外。
		if !p.visible {
			break
		}
		// WM_NCHITTEST 的 lParam 是屏幕坐标（不是客户区坐标），早先直接当客户区用，
	// 于是 pt.Y 永远是个很大的数，顶栏永远命中不到 HTCAPTION，窗口就拖不动了。
	pt := winapi.ScreenPointToClient(hwnd, clientPoint(lp))
		if p.buttonAt(pt) != 0 {
			return winapi.HTCLIENT
		}
		if pt.Y >= 0 && int(pt.Y) < p.px(34) {
			return winapi.HTCAPTION
		}
		return winapi.HTCLIENT

	case winapi.WM_MOVE:
		if p.visible && !p.programmatic {
			p.manual = true // 用户拖过窗口，别在下次追加内容时又跳回鼠标旁边
		}
		return 0

	case winapi.WM_CTLCOLORSTATIC, winapi.WM_CTLCOLOREDIT:
		hdc := wp
		winapi.ProcSetBkMode.Call(hdc, 1) // TRANSPARENT
		color := p.fg
		if lp != p.hBody {
			color = p.dim
		}
		winapi.ProcSetTextColor.Call(hdc, color)
		return p.bgBrush

	case winapi.WM_COMMAND:
		return 0

	case winapi.WM_CLOSE:
		winapi.ProcDestroyWindow.Call(hwnd)
		return 0

	case winapi.WM_DESTROY:
		winapi.ProcPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := winapi.ProcDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func (p *Popover) paintButton(hdc uintptr, r winapi.Rect, id int, label string) {
	bg := p.bgBrush
	color := p.dim
	switch {
	case p.pressBtn == id:
		bg, color = p.pressBrush, p.fg
	case p.hoverBtn == id:
		bg, color = p.hoverBrush, p.fg
	}
	winapi.FillRect(hdc, &r, bg)
	winapi.TextOut(hdc, label, &r, p.fontSmall, color)
}

func (p *Popover) buttonAt(pt winapi.Point) int {
	switch {
	case inRect(p.copyRect, pt):
		return idCopy
	case inRect(p.closeRect, pt):
		return idClose
	}
	return 0
}

func (p *Popover) activate(id int) {
	switch id {
	case idCopy:
		text := p.body
		if text == "" {
			return
		}
		// 必须开 goroutine：剪贴板函数会 LockOSThread/UnlockOSThread，
		// 在 UI 线程上 Unlock 会把主 goroutine 甩到别的线程，消息循环就废了。
		go func() {
			if err := winapi.WriteClipboardText(text); err == nil {
				p.Post(func() {
					p.copyLabel = "已复制"
					winapi.Invalidate(p.hwnd)
				})
			}
		}()
	case idClose:
		p.hide()
	}
}

func inRect(r winapi.Rect, pt winapi.Point) bool {
	return pt.X >= r.Left && pt.X < r.Right && pt.Y >= r.Top && pt.Y < r.Bottom
}

// clientPoint 从鼠标消息的 lParam 取客户区坐标。
func clientPoint(lp uintptr) winapi.Point {
	return winapi.Point{X: int32(int16(lp & 0xFFFF)), Y: int32(int16((lp >> 16) & 0xFFFF))}
}

func (p *Popover) px(logical int) int {
	return int(float64(logical)*float64(p.dpi)/96.0 + 0.5)
}

func errno(what string) error {
	return &opError{what: what}
}

type opError struct{ what string }

func (e *opError) Error() string { return "win32 调用失败：" + e.what }
