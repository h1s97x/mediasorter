package core

import (
	"os"
	"path/filepath"
	"testing"
)

// 同名冲突策略测试: sequence(默认)/skip/overwrite

// TestConflictSequenceDefault 默认 sequence 策略: 同名自动加序号,绝不覆盖。
func TestConflictSequenceDefault(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 两个同拍摄时间的文件(会生成相同基础名),默认 sequence 应加序号区分
	writeTestFile(t, src, "IMG_20240101_000000.jpg", "AAA")
	writeTestFile(t, src, "IMG_20240101_000000_b.jpg", "BBB")

	res := Run(Options{Src: src, Dst: dst, Dedupe: false}, nil)
	if res.Processed != 2 {
		t.Fatalf("期望处理 2,实际 %d", res.Processed)
	}
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 2 {
		t.Fatalf("期望 2 个输出(加序号不覆盖),实际 %d: %v", len(names), names)
	}
}

// TestConflictSkip 目标目录预置同名文件时,skip 策略应跳过不覆盖。
func TestConflictSkip(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 源文件
	writeTestFile(t, src, "IMG_20240101_000000.jpg", "src-data")
	// 目标目录预置同名文件(基础名相同,同子目录 2024/01)
	if err := os.MkdirAll(filepath.Join(dst, "2024", "01"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dst, filepath.Join("2024", "01", "2024-01-01_000000.jpg"), "existing")

	res := Run(Options{Src: src, Dst: dst, Dedupe: false, OnConflict: "skip"}, nil)
	if res.Processed != 0 {
		t.Fatalf("skip 策略下应处理 0(目标已存在),实际 %d", res.Processed)
	}
	if res.Skipped != 1 {
		t.Fatalf("期望跳过 1,实际 %d", res.Skipped)
	}
	if len(res.FailedFiles) != 1 {
		t.Fatalf("期望 1 个失败记录,实际 %d: %v", len(res.FailedFiles), res.FailedFiles)
	}
	// 目标文件应保持原内容(未被覆盖)
	data, err := os.ReadFile(filepath.Join(dst, "2024", "01", "2024-01-01_000000.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Fatalf("skip 不应覆盖目标文件,期望 existing,实际 %q", data)
	}
}

// TestConflictSkipCrossSubdir 修复: skip 策略的冲突判定需结合子目录维度。
// KeepOriginal 下两个文件同名但拍摄日期不同(归档到不同 sub 子目录),
// 不应因基础名相同而被误判为"本批次已处理同名"而误跳过。
func TestConflictSkipCrossSubdir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 源目录两个子目录各放一个同名文件(均为 photo.jpg),但 EXIF 拍摄日期不同
	// → 归档到不同目标子目录(2024/01 与 2024/02)。KeepOriginal 下两者基础名相同,
	// 冲突判定若仅以基础名为键,第二个会被误判为"已处理"而误跳过。
	if err := os.MkdirAll(filepath.Join(src, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a", "photo.jpg"), makeJpgWithExif("2024:01:15 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "b", "photo.jpg"), makeJpgWithExif("2024:02:15 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := Run(Options{Src: src, Dst: dst, Dedupe: false, KeepOriginal: true, OnConflict: "skip"}, nil)
	// 两个同名文件归档到不同子目录,目标均不存在,应都被处理(不误跳过)
	if res.Processed != 2 {
		t.Fatalf("跨目录同名应处理 2(不误跳过),实际 %d", res.Processed)
	}
	if res.Skipped != 0 {
		t.Fatalf("跨目录同名不应被跳过,实际 Skipped=%d", res.Skipped)
	}
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 2 {
		t.Fatalf("期望输出 2 个文件,实际 %d: %v", len(names), names)
	}
}

// TestConflictOverwrite 目标目录预置同名文件时,overwrite 策略应覆盖。
func TestConflictOverwrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTestFile(t, src, "IMG_20240101_000000.jpg", "new-data")
	if err := os.MkdirAll(filepath.Join(dst, "2024", "01"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dst, filepath.Join("2024", "01", "2024-01-01_000000.jpg"), "old-data")

	res := Run(Options{Src: src, Dst: dst, Dedupe: false, OnConflict: "overwrite"}, nil)
	if res.Processed != 1 {
		t.Fatalf("overwrite 策略应处理 1,实际 %d", res.Processed)
	}
	data, err := os.ReadFile(filepath.Join(dst, "2024", "01", "2024-01-01_000000.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-data" {
		t.Fatalf("overwrite 应覆盖目标为 new-data,实际 %q", data)
	}
	// 目标文件数应仍为 1(覆盖而非新增)
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 1 {
		t.Fatalf("overwrite 应保持 1 个文件,实际 %d: %v", len(names), names)
	}
}

// TestStrictTimeSkipsMtimeName 严格时间模式: 只认 EXIF/元数据,不把 mtime/文件名时间当拍摄时间。
func TestStrictTimeSkipsMtimeName(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 无 EXIF 的 JPEG(仅有 mtime 兜底) → 严格模式下应跳过
	writeTestFile(t, src, "plain.jpg", "no-exif-data")
	// 文件名带时间戳但无 EXIF 的 PNG → 严格模式下应跳过(name 不作为拍摄时间)
	writeTestFile(t, src, "IMG_20240101_000000.png", "png")
	// 带 EXIF 的 JPEG → 严格模式下应正常处理
	if err := os.WriteFile(filepath.Join(src, "exif.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := Run(Options{Src: src, Dst: dst, Dedupe: false, StrictTime: true}, nil)
	// 带 EXIF 的应处理,其余两个跳过计为失败
	if res.Processed != 1 {
		t.Fatalf("严格模式期望处理 1(仅 EXIF),实际 %d", res.Processed)
	}
	if res.Failed != 2 {
		t.Fatalf("严格模式期望失败 2(mtime/name 被跳过),实际 %d", res.Failed)
	}
	// 失败文件都应记录到 FailedFiles
	if len(res.FailedFiles) != 2 {
		t.Fatalf("期望 2 个失败记录,实际 %d: %v", len(res.FailedFiles), res.FailedFiles)
	}
}

// TestStrictTimeDisabledAllowsFallback 默认(非严格)模式下,四级兜底正常: name/mtime 也被接受。
func TestStrictTimeDisabledAllowsFallback(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 无 EXIF 的文件,仅靠文件名时间戳 → 默认模式下应处理(不跳过)
	writeTestFile(t, src, "IMG_20240101_000000.jpg", "only-name-time")
	res := Run(Options{Src: src, Dst: dst, Dedupe: false}, nil)
	if res.Processed != 1 {
		t.Fatalf("默认模式期望处理 1(文件名时间兜底),实际 %d", res.Processed)
	}
	if res.Failed != 0 {
		t.Fatalf("默认模式期望失败 0,实际 %d", res.Failed)
	}
}
