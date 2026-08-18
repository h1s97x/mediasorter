//go:build fyne

// GUI 设置持久化(零依赖): 记住上次的源/目标路径、目录结构、去重/移动、时间偏移,
// 下次启动自动恢复。配置文件存到用户配置目录(Windows: %AppData% / macOS: ~/Library/Application
// Support / Linux: ~/.config)。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// settings 可持久化的 GUI 设置
type settings struct {
	Src          string   `json:"src"`
	Dst          string   `json:"dst"`
	Mode         string   `json:"mode"` // "y" | "ym" | "ymd"
	Move         bool     `json:"move"`
	Dedupe       bool     `json:"dedupe"`
	Offset       int      `json:"offset"`
	Extensions   []string `json:"extensions,omitempty"` // 空 = 全部格式
	KeepOriginal bool     `json:"keep_original,omitempty"`
	NamePrefix   string   `json:"name_prefix,omitempty"`
	NameSuffix   string   `json:"name_suffix,omitempty"`
	TimeFilter   string   `json:"time_filter,omitempty"` // "" | "has" | "none"
}

// settingsPath 返回配置文件路径,并确保其所在目录存在
func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "MediaSorterGo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// loadSettings 读取设置;文件不存在或损坏时返回零值(不视为错误)
func loadSettings() settings {
	var s settings
	p, err := settingsPath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return s
	}
	// 解析失败(如版本升级后结构变更)也静默降级为零值
	_ = json.Unmarshal(data, &s)
	// 兜底: 模式必须合法
	if s.Mode != "y" && s.Mode != "ym" && s.Mode != "ymd" {
		s.Mode = "ym"
	}
	return s
}

// saveSettings 保存设置(原子写:先写临时文件再改名,避免中途崩溃损坏配置)
func saveSettings(s settings) error {
	p, err := settingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
