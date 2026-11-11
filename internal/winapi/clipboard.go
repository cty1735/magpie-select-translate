package winapi

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procOpenClipboard        = user32.NewProc("OpenClipboard")
	procCloseClipboard       = user32.NewProc("CloseClipboard")
	procEmptyClipboard       = user32.NewProc("EmptyClipboard")
	procEnumClipboardFormats = user32.NewProc("EnumClipboardFormats")
	procGetClipboardData     = user32.NewProc("GetClipboardData")
	procSetClipboardData     = user32.NewProc("SetClipboardData")
	procGetClipboardSequence = user32.NewProc("GetClipboardSequenceNumber")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalSize   = kernel32.NewProc("GlobalSize")
)

const (
	CFUnicodeText = 13
	GMEM_MOVEABLE = 0x0002
)

// ClipItem 是一个剪贴板格式及其字节副本。
type ClipItem struct {
	Format uint32
	Data   []byte
}

// 每个函数都必须 LockOSThread：OpenClipboard/CloseClipboard 是线程亲和的，
// Go 的 goroutine 可能中途换线程，Close 落到别的线程就会永久锁住剪贴板。
func openClipboard() error {
	for i := 0; i < 5; i++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return syscall.Errno(windows.ERROR_ACCESS_DENIED)
}

// SnapshotClipboard 全量保存剪贴板中所有 HGLOBAL 型格式（含文本）。
// shortcut: 位图/图元文件等非 HGLOBAL 格式不还原，用户复制图片后划词会丢图；
// 需要时改为 DuplicateHandle + 全格式还原。
func SnapshotClipboard() ([]ClipItem, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(); err != nil {
		return nil, err
	}
	defer procCloseClipboard.Call()

	var items []ClipItem
	var format uint32
	for len(items) < 64 {
		r, _, _ := procEnumClipboardFormats.Call(uintptr(format))
		format = uint32(r)
		if format == 0 {
			break
		}
		h, _, _ := procGetClipboardData.Call(uintptr(format))
		if h == 0 {
			continue
		}
		size, _, _ := procGlobalSize.Call(h)
		if size == 0 {
			continue
		}
		p, _, _ := procGlobalLock.Call(h)
		if p == 0 {
			continue
		}
		src := unsafe.Slice((*byte)(unsafe.Pointer(p)), int(size))
		items = append(items, ClipItem{Format: format, Data: append([]byte(nil), src...)})
		procGlobalUnlock.Call(h)
	}
	return items, nil
}

// RestoreClipboard 把 SnapshotClipboard 的结果写回。失败静默（尽力而为）。
func RestoreClipboard(items []ClipItem) error {
	if len(items) == 0 {
		return nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	for _, it := range items {
		h, _, _ := procGlobalAlloc.Call(GMEM_MOVEABLE, uintptr(len(it.Data)))
		if h == 0 {
			continue
		}
		p, _, _ := procGlobalLock.Call(h)
		if p == 0 {
			procGlobalFree.Call(h)
			continue
		}
		copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), len(it.Data)), it.Data)
		procGlobalUnlock.Call(h)
		if r, _, _ := procSetClipboardData.Call(uintptr(it.Format), h); r == 0 {
			procGlobalFree.Call(h) // 系统没接管，所有权还在我们手里
		}
	}
	return nil
}

// ReadClipboardText 读 CF_UNICODETEXT。
func ReadClipboardText() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(CFUnicodeText)
	if h == 0 {
		return "", syscall.ENODATA
	}
	size, _, _ := procGlobalSize.Call(h)
	if size < 2 {
		return "", syscall.ENODATA
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", syscall.EINVAL
	}
	defer procGlobalUnlock.Call(h)
	// GlobalSize 给的是分配长度，末尾可能有垃圾；按 UTF-16 截断到第一个 NUL。
	buf := unsafe.Slice((*uint16)(unsafe.Pointer(p)), int(size)/2)
	for i, v := range buf {
		if v == 0 {
			buf = buf[:i]
			break
		}
	}
	return windows.UTF16ToString(buf), nil
}

// WriteClipboardText 覆盖写纯文本剪贴板。
func WriteClipboardText(s string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	utf16, err := windows.UTF16FromString(s)
	if err != nil {
		return err
	}
	if err := openClipboard(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	size := len(utf16) * 2
	h, _, _ := procGlobalAlloc.Call(GMEM_MOVEABLE, uintptr(size))
	if h == 0 {
		return syscall.EINVAL
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return syscall.EINVAL
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), size), unsafe.Slice((*byte)(unsafe.Pointer(&utf16[0])), size))
	procGlobalUnlock.Call(h)
	if r, _, _ := procSetClipboardData.Call(CFUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return syscall.EINVAL
	}
	return nil
}

// ClipboardSeq 返回剪贴板序列号，用来判断内容是否真被改写。
func ClipboardSeq() uint32 {
	r, _, _ := procGetClipboardSequence.Call()
	return uint32(r)
}

// ClipboardClear 清空剪贴板（用于"取词前先清空再等变化"的兜底路径）。
func ClipboardClear() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := openClipboard(); err != nil {
		return
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
}
