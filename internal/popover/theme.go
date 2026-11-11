package popover

import (
	"golang.org/x/sys/windows/registry"

	"magpie-translate/internal/winapi"
)

// colorref 是 Win32 的 COLORREF：0x00BBGGRR。
func colorref(r, g, b uint32) uintptr {
	return uintptr(r | g<<8 | b<<16)
}

func brush(color uintptr) uintptr {
	h, _, _ := winapi.ProcCreateSolidBrush.Call(color)
	return h
}

// theme 跟随系统深/浅色，返回前景色、次要文字色，以及背景/悬停/按下三个画刷。
func theme() (fg, dim, bg, hover, press uintptr) {
	light := true
	if k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("AppsUseLightTheme"); err == nil {
			light = v == 1
		}
		k.Close()
	}
	var bgc, hovc, prsc uintptr
	if light {
		fg, dim = colorref(27, 27, 31), colorref(107, 114, 128)
		bgc, hovc, prsc = colorref(255, 255, 255), colorref(238, 240, 243), colorref(221, 225, 230)
	} else {
		fg, dim = colorref(230, 230, 230), colorref(154, 160, 166)
		bgc, hovc, prsc = colorref(30, 31, 34), colorref(43, 45, 50), colorref(56, 59, 65)
	}
	return fg, dim, brush(bgc), brush(hovc), brush(prsc)
}
