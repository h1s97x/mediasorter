package core

// 端到端集成测试: 用真实临时目录跑完整 Run 流程,
// 验证"逻辑是否通、联调是否过、边界防不防得住"。
// 对应 ISSUE #23 的 P0 方案。

import (
	"bytes"
	"context"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// writeJpegWithExif 构造一个带指定 DateTimeOriginal 拍摄时间的 JPEG 文件并落盘。
// 返回文件路径。day 字符串形如 "2026-08-17 10:41:21"。
func writeJpegWithExif(t *testing.T, dir, name, day string) string {
	t.Helper()
	le := binary.LittleEndian
	strData := []byte(day + "\x00")
	// 偏移: TIFF头(8) + IFD0 count(2) + 1*12 + next(4) + ExifIFD count(2) + 1*12 + next(4)
	strOff := uint32(8 + 2 + 12 + 4 + 2 + 12 + 4)
	ifd0Entry := make([]byte, 12)
	le.PutUint16(ifd0Entry[0:2], 0x8769) // ExifIFDPointer
	le.PutUint16(ifd0Entry[2:4], 4)
	le.PutUint32(ifd0Entry[4:8], 1)
	le.PutUint32(ifd0Entry[8:12], uint32(8+2+12+4))
	exifEntry := tiffAsciiEntry(le, 0x9003, day, strOff)
	tiff := buildTiff(t, "II", ifd0Entry, exifEntry, strData)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	data := makeJpegBytes(app1)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// listRelPaths 递归列出 dst 下所有文件的相对路径(按相对 Src 的路径排序),仅文件。
func listRelFiles(t *testing.T, root string) []string {
	t.Helper()
	var rel []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = append(rel, r)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rel)
	return rel
}

// readFileContent 读取文件全部内容。
func readFileContent(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ===== 复制(Copy): 内容逐字节一致,源文件保留 =====
func TestRunE2E_CopyContentPreserved(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 用带 EXIF 的 JPEG,确保时间从 EXIF 提取
	writeJpegWithExif(t, src, "a.jpg", "2026-08-17 10:41:21")
	orig := readFileContent(t, filepath.Join(src, "a.jpg"))

	res := Run(Options{Src: src, Dst: dst}, nil)
	if res.Processed != 1 {
		t.Fatalf("期望处理 1 个,实际 %d", res.Processed)
	}
	if res.Failed != 0 {
		t.Fatalf("期望 0 失败,实际 %d", res.Failed)
	}
	// 源文件仍在
	if _, err := os.Stat(filepath.Join(src, "a.jpg")); err != nil {
		t.Fatalf("复制模式源文件应保留: %v", err)
	}
	// 目标文件内容逐字节一致
	rel := listRelFiles(t, dst)
	if len(rel) != 1 {
		t.Fatalf("期望 1 个目标文件,实际 %d: %v", len(rel), rel)
	}
	got := readFileContent(t, filepath.Join(dst, rel[0]))
	if !bytes.Equal(got, orig) {
		t.Fatal("复制后目标文件内容与源不一致")
	}
	// 目标命名应为规范时间名(EXIF 拍摄时间)
	base := filepath.Base(rel[0])
	if !strings.HasPrefix(base, "2026-08-17_104121_") {
		t.Fatalf("目标文件名应基于 EXIF 拍摄时间,实际 %q", base)
	}
}

// ===== 移动(Move): 源被删除,目标内容正确 =====
func TestRunE2E_MoveRemovesSource(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	p := writeJpegWithExif(t, src, "a.jpg", "2025-01-02 03:04:05")
	orig := readFileContent(t, p)

	res := Run(Options{Src: src, Dst: dst, Move: true}, nil)
	if res.Processed != 1 {
		t.Fatalf("期望处理 1 个,实际 %d", res.Processed)
	}
	// 源被删除
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("移动模式源文件应被删除,实际 err=%v", err)
	}
	// 目标内容正确
	rel := listRelFiles(t, dst)
	if len(rel) != 1 {
		t.Fatalf("期望 1 个目标文件,实际 %d: %v", len(rel), rel)
	}
	got := readFileContent(t, filepath.Join(dst, rel[0]))
	if !bytes.Equal(got, orig) {
		t.Fatal("移动后目标文件内容与源不一致")
	}
	if !strings.HasPrefix(filepath.Base(rel[0]), "2025-01-02_030405_") {
		t.Fatalf("目标文件名应基于拍摄时间,实际 %q", rel[0])
	}
}

// ===== 去重(Dedupe): 相同内容只保留 1 份,不同内容不误删 =====
func TestRunE2E_Dedupe(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 两个内容完全相同、但拍摄时间不同(仅用于文件名展示)的 JPEG
	writeJpegWithExif(t, src, "a.jpg", "2026-01-01 00:00:00")
	// 第二个: 复制同一个文件(内容一致)
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(filepath.Join(src, "a.jpg"), filepath.Join(src, "sub", "b.jpg"), context.Background()); err != nil {
		t.Fatal(err)
	}
	// 第三个: 内容不同的 JPEG
	writeJpegWithExif(t, src, "c.jpg", "2026-02-02 02:02:02")

	res := Run(Options{Src: src, Dst: dst, Dedupe: true}, nil)
	if res.Duplicates != 1 {
		t.Fatalf("期望去重 1 个,实际 %d", res.Duplicates)
	}
	if res.Processed != 2 {
		t.Fatalf("期望处理 2 个(去重后),实际 %d", res.Processed)
	}
	// 目标应有 2 个文件
	rel := listRelFiles(t, dst)
	if len(rel) != 2 {
		t.Fatalf("期望 2 个目标文件,实际 %d: %v", len(rel), rel)
	}
	// 不同内容不被误删: 验证最终两个目标文件内容互不相同
	if bytes.Equal(
		readFileContent(t, filepath.Join(dst, rel[0])),
		readFileContent(t, filepath.Join(dst, rel[1]))) {
		t.Fatal("去重后两个目标文件内容不应相同(不同源文件)")
	}
}

// ===== 目录结构: 默认 年/月 =====
func TestRunE2E_DirStructureDefault(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeJpegWithExif(t, src, "a.jpg", "2026-08-17 10:00:00")

	Run(Options{Src: src, Dst: dst}, nil)
	rel := listRelFiles(t, dst)
	if len(rel) != 1 {
		t.Fatalf("期望 1 个文件,实际 %d", len(rel))
	}
	// 默认结构: 年/月/文件
	parts := strings.Split(rel[0], string(filepath.Separator))
	if len(parts) != 3 {
		t.Fatalf("默认应 年/月/文件 三层,实际 %v", rel)
	}
	if parts[0] != "2026" || parts[1] != "08" {
		t.Fatalf("默认目录应为 2026/08,实际 %v", rel)
	}
}

// ===== 目录结构: Year=true -> 年 =====
func TestRunE2E_DirStructureYear(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeJpegWithExif(t, src, "a.jpg", "2026-08-17 10:00:00")

	Run(Options{Src: src, Dst: dst, Year: true}, nil)
	rel := listRelFiles(t, dst)
	if len(rel) != 1 {
		t.Fatalf("期望 1 个文件,实际 %d", len(rel))
	}
	parts := strings.Split(rel[0], string(filepath.Separator))
	if len(parts) != 2 {
		t.Fatalf("Year 应 年/文件 两层,实际 %v", rel)
	}
	if parts[0] != "2026" {
		t.Fatalf("Year 目录应为 2026,实际 %v", rel)
	}
}

// ===== 目录结构: Day=true -> 年/月/日 =====
func TestRunE2E_DirStructureDay(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeJpegWithExif(t, src, "a.jpg", "2026-08-17 10:00:00")

	Run(Options{Src: src, Dst: dst, Day: true}, nil)
	rel := listRelFiles(t, dst)
	if len(rel) != 1 {
		t.Fatalf("期望 1 个文件,实际 %d", len(rel))
	}
	parts := strings.Split(rel[0], string(filepath.Separator))
	if len(parts) != 4 {
		t.Fatalf("Day 应 年/月/日/文件 四层,实际 %v", rel)
	}
	if parts[0] != "2026" || parts[1] != "08" || parts[2] != "17" {
		t.Fatalf("Day 目录应为 2026/08/17,实际 %v", rel)
	}
}

// ===== 时间偏移 Offset: 命名时间偏移指定秒数 =====
func TestRunE2E_Offset(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeJpegWithExif(t, src, "a.jpg", "2026-08-17 10:00:00")

	// Offset +3600: 命名应偏移到 11:00:00
	Run(Options{Src: src, Dst: dst, Offset: 3600}, nil)
	rel := listRelFiles(t, dst)
	if len(rel) != 1 {
		t.Fatalf("期望 1 个文件,实际 %d", len(rel))
	}
	if !strings.HasPrefix(filepath.Base(rel[0]), "2026-08-17_110000_") {
		t.Fatalf("Offset 后文件名应偏移到 11:00:00,实际 %q", rel[0])
	}
	// 目录也会因偏移不变(同年同月) —— 验证文件名偏移足够
}

// ===== 扫描排除输出目录(防递归): dst 建在 src 内层 =====
func TestRunE2E_ScanExcludesDst(t *testing.T) {
	src := t.TempDir()
	// dst 建在 src 内部,验证不会被扫进来造成递归/自处理
	dst := filepath.Join(src, "output")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	// 源目录里放一个媒体文件
	writeJpegWithExif(t, src, "a.jpg", "2026-08-17 10:00:00")
	// 输出目录里也预置一个媒体文件(若被扫入,会额外多处理)
	writeJpegWithExif(t, dst, "already.bin.jpg", "2026-01-01 00:00:00")

	res := Run(Options{Src: src, Dst: dst}, nil)
	// 只应处理源目录 1 个文件,dst 内的文件不被扫入
	if res.Processed != 1 {
		t.Fatalf("防递归失败: 期望只处理源 1 个,实际 %d", res.Processed)
	}
	// 最终 dst 内文件数 = 预置 1 + 归档 1 = 2
	rel := listRelFiles(t, dst)
	if len(rel) != 2 {
		t.Fatalf("期望 dst 内有 2 个文件(预置+归档),实际 %d: %v", len(rel), rel)
	}
}

// ===== 空目录: 无媒体文件,Processed=0 不 panic =====
func TestRunE2E_EmptyDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst}, nil)
	if res.Processed != 0 {
		t.Fatalf("空目录期望 Processed=0,实际 %d", res.Processed)
	}
	if res.Failed != 0 {
		t.Fatalf("空目录期望 Failed=0,实际 %d", res.Failed)
	}
	// dst 不应产生任何文件
	if rel := listRelFiles(t, dst); len(rel) != 0 {
		t.Fatalf("空目录不应产生目标文件,实际 %v", rel)
	}
}

// ===== 无 EXIF/无文件名时间戳: 走 mtime 兜底被正常处理(而非 Failed) =====
func TestRunE2E_AllUnparseable(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 扩展名是媒体但无 EXIF、无文件名时间戳 —— 只能靠文件 mtime 兜底
	if err := os.WriteFile(filepath.Join(src, "photo.jpg"), []byte("garbage-no-exif"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := Run(Options{Src: src, Dst: dst}, nil)
	// fileFallbackTime 总能拿到文件时间,因此仍应被正常处理(mtime 兜底)
	if res.Processed != 1 {
		t.Fatalf("无元数据文件应靠 mtime 兜底处理,期望 Processed=1,实际 %d", res.Processed)
	}
	if res.Failed != 0 {
		t.Fatalf("期望 Failed=0,实际 %d", res.Failed)
	}
	// 来源应为 mtime
	if res.SourceCount["mtime"] != 1 {
		t.Fatalf("期望来源统计 mtime=1,实际 %v", res.SourceCount)
	}
	// 正常产生 1 个目标文件,不 panic
	if rel := listRelFiles(t, dst); len(rel) != 1 {
		t.Fatalf("期望产生 1 个目标文件,实际 %v", rel)
	}
}

// ===== 时间跨度统计: TimeSpanMin/Max 正确 =====
func TestRunE2E_TimeSpan(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeJpegWithExif(t, src, "a.jpg", "2026-01-01 00:00:00")
	writeJpegWithExif(t, src, "b.jpg", "2026-12-31 23:59:59")

	res := Run(Options{Src: src, Dst: dst}, nil)
	if res.Processed != 2 {
		t.Fatalf("期望处理 2 个,实际 %d", res.Processed)
	}
	wantMin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	wantMax := time.Date(2026, 12, 31, 23, 59, 59, 0, time.Local)
	if !res.TimeSpanMin.Equal(wantMin) {
		t.Fatalf("TimeSpanMin 期望 %v,实际 %v", wantMin, res.TimeSpanMin)
	}
	if !res.TimeSpanMax.Equal(wantMax) {
		t.Fatalf("TimeSpanMax 期望 %v,实际 %v", wantMax, res.TimeSpanMax)
	}
	// 来源统计应为 EXIF
	if res.SourceCount["EXIF"] != 2 {
		t.Fatalf("期望来源统计 EXIF=2,实际 %v", res.SourceCount)
	}
}

// ===== 同名冲突: 相同拍摄时间不覆盖,追加序号 =====
func TestRunE2E_ConflictSequence(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 两个内容不同的文件但拍摄时间完全相同 -> 需要序号区分且不覆盖
	writeJpegWithExif(t, src, "a.jpg", "2026-08-17 10:00:00")
	// 在第二个文件末尾追加不同字节,使其内容与 a.jpg 不同(不影响 EXIF 解析)
	b := writeJpegWithExif(t, src, "b.jpg", "2026-08-17 10:00:00")
	if err := os.WriteFile(b, append(readFileContent(t, b), 0xDE, 0xAD, 0xBE, 0xEF), 0o600); err != nil {
		t.Fatal(err)
	}

	res := Run(Options{Src: src, Dst: dst}, nil)
	if res.Processed != 2 {
		t.Fatalf("期望处理 2 个,实际 %d", res.Processed)
	}
	rel := listRelFiles(t, dst)
	if len(rel) != 2 {
		t.Fatalf("期望 2 个目标文件,实际 %d: %v", len(rel), rel)
	}
	// 两个文件都在同一目录且文件名不同(序号区分),内容不同
	if rel[0] == rel[1] {
		t.Fatalf("两个冲突文件不应同名: %v", rel)
	}
	// 内容不同(未被覆盖)
	c0 := readFileContent(t, filepath.Join(dst, rel[0]))
	c1 := readFileContent(t, filepath.Join(dst, rel[1]))
	if bytes.Equal(c0, c1) {
		t.Fatal("两个冲突文件的源内容不同,目标不应相同")
	}
}
