package pick

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  hello\r\nworld\r\n": "hello\nworld",
		"\n\n中文\n":             "中文",
		"a  b":                 "a  b",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// 黑名单是安全边界：命中就绝不合成 Ctrl+C（终端里会打断正在跑的命令）。
func TestBlacklistMatch(t *testing.T) {
	p := New(" mstsc.exe , WindowsTerminal.exe ,, ", true)
	for _, name := range []string{"mstsc.exe", "windowsterminal.exe"} {
		if !p.Blacklist[name] {
			t.Errorf("%q 应在黑名单里", name)
		}
	}
	if p.Blacklist["chrome.exe"] {
		t.Error("chrome.exe 不该在黑名单里")
	}
	if !p.Restore {
		t.Error("Restore 应透传")
	}
	if len(New(DefaultBlacklist, true).Blacklist) != 8 {
		t.Errorf("默认黑名单应有 8 项，got %d", len(New(DefaultBlacklist, true).Blacklist))
	}
}
