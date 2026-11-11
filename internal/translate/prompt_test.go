package translate

import "testing"

func TestDetectTarget(t *testing.T) {
	cases := map[string]string{
		"Hello world":   "zh",
		"你好世界":          "en",
		"这是一个 mixed 句子": "en",
		"こんにちは":         "zh",
		"안녕하세요":         "zh",
		"Привет мир":    "zh",
		"12345 !!!":     "zh",
		// 英文为主、夹一个中文专名 → 仍然翻成中文
		"Done. Verify with powercfg. 荣耀管家 may override.": "zh",
		// 中文为主、夹英文术语 → 翻成英文
		"把这个 file 里的 function 改成 async，别忘了用 荣耀管家 跑一遍": "en",
	}
	for in, want := range cases {
		if got := DetectTarget(in); got != want {
			t.Errorf("DetectTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeTarget(t *testing.T) {
	cases := map[string]string{
		"en": "en", "EN": "en", "English": "en",
		"zh": "zh", "zh-CN": "zh", "中文": "zh",
		"auto": "", "": "", "klingon": "",
	}
	for in, want := range cases {
		if got := NormalizeTarget(in); got != want {
			t.Errorf("NormalizeTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

// system prompt 必须逐字节稳定，否则前缀缓存失效。
func TestSystemPromptStable(t *testing.T) {
	a := SystemPrompt("zh")
	b := SystemPrompt("zh-CN")
	if a != b {
		t.Fatal("同一目标语言的 system prompt 不一致")
	}
	if SystemPrompt("en") == a {
		t.Fatal("中英两个 system prompt 应该不同")
	}
	if len(a) == 0 {
		t.Fatal("system prompt 为空")
	}
}

func TestUserPromptGlossary(t *testing.T) {
	if got := UserPrompt("hello", ""); got != "hello" {
		t.Fatalf("没有术语表时不应有任何前缀，got %q", got)
	}
	got := UserPrompt("hello", "# comment\nAPI=接口\n\nbad-line\n")
	want := "[term]\nAPI=接口\n[/term]\nhello"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
