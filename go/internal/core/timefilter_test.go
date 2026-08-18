package core

import "testing"

// TestMatchesTimeFilter 验证录制日期筛选逻辑
func TestMatchesTimeFilter(t *testing.T) {
	// 各来源 + ok 组合
	cases := []struct {
		srcTag string
		ok     bool
		filter string
		want   bool
	}{
		// 全部模式: 任何来源都处理
		{"EXIF", true, "", true},
		{"meta", true, "", true},
		{"name", true, "", true},
		{"mtime", true, "", true},
		{"", false, "", true}, // 完全无时间,全部模式下仍进入(由调用方计 Failed)

		// 仅录制日期: 只保留 EXIF/meta
		{"EXIF", true, "has", true},
		{"meta", true, "has", true},
		{"name", true, "has", false},
		{"mtime", true, "has", false},
		{"", false, "has", false}, // 完全无时间不算有录制日期

		// 仅无录制日期: 只保留 name/mtime
		{"EXIF", true, "none", false},
		{"meta", true, "none", false},
		{"name", true, "none", true},
		{"mtime", true, "none", true},
		{"", false, "none", false}, // 完全无时间无法归档,不处理
	}
	for _, c := range cases {
		if got := matchesTimeFilter(c.srcTag, c.ok, c.filter); got != c.want {
			t.Errorf("matchesTimeFilter(%q,%v,%q) = %v,期望 %v",
				c.srcTag, c.ok, c.filter, got, c.want)
		}
	}
}
