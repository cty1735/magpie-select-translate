package winapi

import (
	"testing"
	"time"
)

// Chromium 系只认扫描码：这三个键的扫描码必须拿得到，否则合成的 Ctrl+C 在 Chrome 里失效。
func TestScanCode(t *testing.T) {
	for _, vk := range []uintptr{VKAlt, VKCtrl, VKC} {
		if sc := scanCode(vk); sc == 0 {
			t.Fatalf("scanCode(%#x) = 0", vk)
		}
	}
}

// 没按任何修饰键时不能白等 400ms：热键取词必须立刻发 Ctrl+C。
func TestWaitModifiersUpFast(t *testing.T) {
	start := time.Now()
	if !waitModifiersUp(400 * time.Millisecond) {
		t.Fatal("no modifier is down, want true")
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("took %v, want near-instant", d)
	}
}
