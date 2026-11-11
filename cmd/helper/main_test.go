package main

import (
	"strings"
	"testing"

	"magpie-translate/internal/winapi"
)

func TestParseHotkey(t *testing.T) {
	cases := []struct {
		in      string
		mods    uint32
		vk      uint32
		wantErr bool
	}{
		{"Alt+D", winapi.ModAlt, 'D', false},
		{"alt+d", winapi.ModAlt, 'D', false},
		{"Ctrl+Shift+T", winapi.ModCtrl | winapi.ModShift, 'T', false},
		{"Win+F12", winapi.ModWin, 0x70 + 11, false},
		{"Ctrl+1", winapi.ModCtrl, '1', false},
		{"Alt", 0, 0, true},
		{"Hyper+D", 0, 0, true},
		{"Alt+F99", 0, 0, true},
	}
	for _, c := range cases {
		mods, vk, err := ParseHotkey(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseHotkey(%q) 应该报错", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseHotkey(%q) err = %v", c.in, err)
			continue
		}
		if mods != c.mods || vk != c.vk {
			t.Errorf("ParseHotkey(%q) = (%#x, %#x), want (%#x, %#x)", c.in, mods, vk, c.mods, c.vk)
		}
	}
}

func TestLabel(t *testing.T) {
	if got := label("zh", false, false); got != "EN → 中文" {
		t.Errorf("got %q", got)
	}
	if got := label("en", true, true); !strings.Contains(got, "缓存") || !strings.Contains(got, "截断") {
		t.Errorf("got %q", got)
	}
}
