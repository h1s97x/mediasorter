// 时间提取四级兜底链测试: EXIF -> 视频元数据 -> 文件名 -> 文件时间
package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ===== parseFileNameTime 单测 =====

func TestParseFileNameTime_UnderscoreFormat(t *testing.T) {
	// IMG_20260817_104121
	tm, ok := parseFileNameTime("IMG_20260817_104121.jpg")
	if !ok {
		t.Fatal("IMG_20260817_104121 应能解析")
	}
	want := time.Date(2026, 8, 17, 10, 41, 21, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

func TestParseFileNameTime_HyphenFormat(t *testing.T) {
	// REC_20260817-104121
	tm, ok := parseFileNameTime("REC_20260817-104121.mp4")
	if !ok {
		t.Fatal("REC_20260817-104121 应能解析")
	}
	want := time.Date(2026, 8, 17, 10, 41, 21, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

func TestParseFileNameTime_DashSeparated(t *testing.T) {
	// Screenshot_2026-08-17-104121
	tm, ok := parseFileNameTime("Screenshot_2026-08-17-104121.png")
	if !ok {
		t.Fatal("Screenshot_2026-08-17-104121 应能解析")
	}
	want := time.Date(2026, 8, 17, 10, 41, 21, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

func TestParseFileNameTime_UnixMillis(t *testing.T) {
	// 毫秒时间戳 1722534678123
	tm, ok := parseFileNameTime("mmexport1722534678123.jpg")
	if !ok {
		t.Fatal("毫秒时间戳应能解析")
	}
	want := time.Unix(1722534678, 0).Local()
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

func TestParseFileNameTime_UnixSeconds(t *testing.T) {
	// 秒时间戳 1722534678
	tm, ok := parseFileNameTime("mmexport1722534678.jpg")
	if !ok {
		t.Fatal("秒时间戳应能解析")
	}
	want := time.Unix(1722534678, 0).Local()
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

func TestParseFileNameTime_InvalidDate(t *testing.T) {
	// 2月30日 — Go 的 time.Parse 会自动进位,应返回 false(由反向格式化校验防住)
	tm, ok := parseFileNameTime("IMG_20260230_104121.jpg")
	if ok {
		t.Errorf("非法日期 2月30日 不应解析成功, got %v", tm)
	}
	// 短横线格式非法日期同样拒绝
	if tm2, ok2 := parseFileNameTime("Screenshot_2026-02-30-104121.png"); ok2 {
		t.Errorf("短横线非法日期 2月30日 不应解析成功, got %v", tm2)
	}
	// year 0(0000 年)为非法,应拒绝
	if tm3, ok3 := parseFileNameTime("Screenshot_0000-01-01-000000.png"); ok3 {
		t.Errorf("year 0(0000 年)不应解析成功, got %v", tm3)
	}
}

func TestParseFileNameTime_NoMatch(t *testing.T) {
	if _, ok := parseFileNameTime("DSC_0001.jpg"); ok {
		t.Error("无时间戳文件名不应解析成功")
	}
	if _, ok := parseFileNameTime(""); ok {
		t.Error("空文件名不应解析成功")
	}
	if _, ok := parseFileNameTime("abc.png"); ok {
		t.Error("非时间戳文本不应解析成功")
	}
}

func TestParseFileNameTime_Priority(t *testing.T) {
	// 文件名含 8位+6位 格式优先于 unix 戳
	tm, ok := parseFileNameTime("IMG_20260817_104121_1722534678123.jpg")
	if !ok {
		t.Fatal("应解析成功")
	}
	want := time.Date(2026, 8, 17, 10, 41, 21, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v (应优先取 8+6 格式)", tm, want)
	}
}

// ===== fileFallbackTime 单测 =====

func TestFileFallbackTime_UsesModTime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plain.jpg")
	if err := os.WriteFile(p, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 设置一个已知的 mtime
	known := time.Date(2020, 5, 6, 7, 8, 9, 0, time.Local)
	if err := os.Chtimes(p, known, known); err != nil {
		t.Fatal(err)
	}
	tm, ok := fileFallbackTime(p)
	if !ok {
		t.Fatal("文件存在应能取到时间")
	}
	if tm.Before(known.Add(-time.Second)) || tm.After(known.Add(time.Second)) {
		t.Errorf("mtime 应接近 %v,实际 %v", known, tm)
	}
}

func TestFileFallbackTime_MissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope.jpg")
	if _, ok := fileFallbackTime(p); ok {
		t.Error("文件不存在应返回 false")
	}
}

// ===== GetCaptureTime 端到端(四级兜底优先级) =====

func TestGetCaptureTime_JPEGExifPriority(t *testing.T) {
	// 构造带 EXIF 的 JPEG,文件名含不同时间戳,验证 EXIF 优先
	dir := t.TempDir()
	p := filepath.Join(dir, "IMG_20000101_000000.jpg") // 文件名时间戳与 EXIF 不同
	if err := os.WriteFile(p, makeJpgWithExif("2023:06:15 08:30:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	tm, src, ok := GetCaptureTime(p)
	if !ok {
		t.Fatal("应解析成功")
	}
	if src != "EXIF" {
		t.Errorf("来源应为 EXIF,实际 %q", src)
	}
	want := time.Date(2023, 6, 15, 8, 30, 0, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

func TestGetCaptureTime_MP4Priority(t *testing.T) {
	// 构造带 MP4 元数据的文件,文件名含时间戳,验证 meta 优先
	dir := t.TempDir()
	p := filepath.Join(dir, "IMG_20000101_000000.mp4")
	data := makeMinimalMp4(t, 0)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	tm, src, ok := GetCaptureTime(p)
	if !ok {
		t.Fatal("应解析成功")
	}
	if src != "meta" {
		t.Errorf("来源应为 meta,实际 %q", src)
	}
	want := time.Unix(1700000000, 0).Local()
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

func TestGetCaptureTime_FileNamePriority(t *testing.T) {
	// 无 EXIF/MP4 元数据,但有文件名时间戳
	dir := t.TempDir()
	p := filepath.Join(dir, "IMG_20250101_123456.jpg")
	if err := os.WriteFile(p, []byte{0xff, 0xd8, 0xff, 0xd9}, 0o600); err != nil {
		t.Fatal(err)
	}
	tm, src, ok := GetCaptureTime(p)
	if !ok {
		t.Fatal("应解析成功")
	}
	if src != "name" {
		t.Errorf("来源应为 name,实际 %q", src)
	}
	want := time.Date(2025, 1, 1, 12, 34, 56, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

func TestGetCaptureTime_ModTimeFallback(t *testing.T) {
	// 无 EXIF/文件名时间戳,回退到文件时间
	dir := t.TempDir()
	p := filepath.Join(dir, "plain.jpg")
	if err := os.WriteFile(p, []byte("nothing special"), 0o600); err != nil {
		t.Fatal(err)
	}
	known := time.Date(2022, 3, 4, 5, 6, 7, 0, time.Local)
	if err := os.Chtimes(p, known, known); err != nil {
		t.Fatal(err)
	}
	tm, src, ok := GetCaptureTime(p)
	if !ok {
		t.Fatal("应解析成功")
	}
	if src != "mtime" {
		t.Errorf("来源应为 mtime,实际 %q", src)
	}
	if tm.Before(known.Add(-time.Second)) || tm.After(known.Add(time.Second)) {
		t.Errorf("mtime 应接近 %v,实际 %v", known, tm)
	}
}

func TestGetCaptureTime_NoTimeAnywhere(t *testing.T) {
	// 文件不存在:任何来源都取不到
	p := filepath.Join(t.TempDir(), "not_exists.jpg")
	if _, _, ok := GetCaptureTime(p); ok {
		t.Error("文件不存在应返回 false")
	}
}

func TestGetCaptureTime_MissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ghost.jpg")
	if _, _, ok := GetCaptureTime(p); ok {
		t.Error("不存在的文件应返回 false")
	}
}

func TestGetCaptureTime_UnsupportedExt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(p, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 非媒体扩展名: 文件名时间戳仍应可解析(GetCaptureTime 对扩展名不设限,回退链仍工作)
	tm, src, ok := GetCaptureTime(p)
	if !ok {
		t.Fatal("txt 文件也应能通过回退链取到时间")
	}
	if src != "mtime" {
		t.Errorf("txt 无时间戳应走 mtime,实际 %q", src)
	}
	_ = tm
}

func TestGetCaptureTime_HeicPriority(t *testing.T) {
	// HEIC 文件: 文件名无时间戳时,走 mtime 兜底(最小 HEIC 不含 DateTimeOriginal)
	dir := t.TempDir()
	p := filepath.Join(dir, "IMG_0001.heic")
	data := makeMinimalHeic(t)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	known := time.Date(2024, 1, 2, 3, 4, 5, 0, time.Local)
	if err := os.Chtimes(p, known, known); err != nil {
		t.Fatal(err)
	}
	_, src, ok := GetCaptureTime(p)
	if !ok {
		t.Fatal("HEIC 应能通过 mtime 兜底")
	}
	if src != "mtime" {
		t.Errorf("无 EXIF 的 HEIC 应走 mtime,实际 %q", src)
	}
}
