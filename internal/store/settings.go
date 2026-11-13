package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"magpie-translate/internal/pick"
)

// Settings 是 settings.json 的形状；字段与应用内可变项一一对应，不做多余抽象。
type Settings struct {
	Port             int    `json:"port"`
	Gateway          string `json:"gateway"`
	Model            string `json:"model"`
	Target           string `json:"target"` // auto / zh / en
	Hotkey           string `json:"hotkey"`
	Trigger          string `json:"trigger"` // hotkey / select
	Blacklist        string `json:"blacklist"`
	GlossaryText     string `json:"glossary"` // 术语表正文；不存路径，接口给什么就只用什么，绝不按请求去读本地文件
	RestoreClipboard bool   `json:"restoreClipboard"`
}

func Defaults() Settings {
	return Settings{
		Port:             3477,
		Target:           "auto",
		Hotkey:           "Alt+D",
		Trigger:          "hotkey",
		Blacklist:        pick.DefaultBlacklist,
		RestoreClipboard: true,
	}
}

// Dir 返回 %LOCALAPPDATA%\magpie-translate。
func Dir() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return filepath.Join(v, "magpie-translate")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "AppData", "Local", "magpie-translate")
	}
	return "."
}

func Path() string { return filepath.Join(Dir(), "settings.json") }

// Load 读配置；文件不存在或损坏都退回默认值（缺配置不该拦住用户）。
func Load() Settings {
	s := Defaults()
	b, err := os.ReadFile(Path())
	if err != nil {
		return s
	}
	if json.Unmarshal(b, &s) != nil {
		return Defaults()
	}
	if s.Port == 0 {
		s.Port = 3477
	}
	if strings.TrimSpace(s.Hotkey) == "" {
		s.Hotkey = "Alt+D"
	}
	return s
}

func (s Settings) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), append(b, '\n'), 0o644)
}

// Glossary 返回术语表正文（每行 原文=译文，# 开头是注释）。
func (s Settings) Glossary() string { return s.GlossaryText }
