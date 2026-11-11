package translate

import (
	"regexp"
	"strings"
)

// system prompt 必须逐字节稳定，否则 magpie 侧的真前缀缓存失效会明显变贵变慢。
// 所以这里只有两个常量，禁止拼接时间戳、字数、随机值。
const sysToZH = `You are a translation engine. Translate the user's text into Simplified Chinese.
Output only the translation. Preserve Markdown, code blocks, inline code, URLs, emails,
numbers, units and placeholders ({{}}, %s, {0}) exactly. Keep proper nouns unchanged.
Do not explain, do not add notes, do not wrap the answer in quotes.`

const sysToEN = `You are a translation engine. Translate the user's text into English.
Output only the translation. Preserve Markdown, code blocks, inline code, URLs, emails,
numbers, units and placeholders ({{}}, %s, {0}) exactly. Keep proper nouns unchanged.
Do not explain, do not add notes, do not wrap the answer in quotes.`

var (
	reCJK, _    = regexp.Compile(`[\x{4e00}-\x{9fff}\x{3400}-\x{4dbf}]`)
	reKana, _   = regexp.Compile(`[\x{3040}-\x{30ff}]`)
	reHangul, _ = regexp.Compile(`[\x{ac00}-\x{d7af}]`)
	reCyr, _    = regexp.Compile(`[\x{0400}-\x{04ff}]`)
	reLatin, _  = regexp.Compile(`[A-Za-z]`)
)

// DetectTarget 判断译文语言：汉字占多数（且无假名/谚文/西里尔）→英文，其余→中文。
// 只看"有没有汉字"会把英文里夹的中文专名（荣耀管家、人名）误判成中文原文，
// 于是英文被"翻译"成英文，看起来像没翻译。
func DetectTarget(text string) string {
	if reKana.MatchString(text) || reHangul.MatchString(text) || reCyr.MatchString(text) {
		return "zh"
	}
	cjk := len(reCJK.FindAllString(text, -1))
	latin := len(reLatin.FindAllString(text, -1))
	if cjk == 0 || latin > cjk*2 {
		return "zh"
	}
	return "en"
}

// NormalizeTarget 把界面上的取值收敛成 zh / en。
func NormalizeTarget(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "en", "english":
		return "en"
	case "zh", "zh-cn", "zh-hans", "chinese", "中文":
		return "zh"
	}
	return ""
}

// SystemPrompt 按目标语言返回固定 system 消息。
func SystemPrompt(target string) string {
	if NormalizeTarget(target) == "en" {
		return sysToEN
	}
	return sysToZH
}

// UserPrompt 组装 user 消息：术语表在前、原文在后，顺序固定。
// 术语表只放在这里，绝不进 system，避免破坏前缀缓存。
func UserPrompt(text, glossary string) string {
	var b strings.Builder
	if terms := glossaryLines(glossary); len(terms) > 0 {
		b.WriteString("[term]\n")
		for _, t := range terms {
			b.WriteString(t)
			b.WriteString("\n")
		}
		b.WriteString("[/term]\n")
	}
	b.WriteString(text)
	return b.String()
}

func glossaryLines(glossary string) []string {
	var out []string
	for _, line := range strings.Split(glossary, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		out = append(out, line)
	}
	return out
}
