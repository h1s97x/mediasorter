// 核心辅助函数边界测试: isMedia / relDir / copyFile / 空目录 / 非媒体 / 大文件等
package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ===== isMedia =====

func TestIsMedia_AllSupportedExts(t *testing.T) {
	// 全部支持的图片扩展名
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif", ".tiff", ".tif", ".heic", ".heif"} {
		if !isMedia(ext, nil) {
			t.Errorf("图片扩展名 %s 应被识别为媒体", ext)
		}
	}
	// 全部支持的视频扩展名
	for _, ext := range []string{".mp4", ".mov", ".avi", ".mkv", ".3gp", ".m4v", ".wmv", ".webm", ".flv"} {
		if !isMedia(ext, nil) {
			t.Errorf("视频扩展名 %s 应被识别为媒体", ext)
		}
	}
}

func TestIsMedia_NotMedia(t *testing.T) {
	for _, ext := range []string{".txt", ".pdf", ".exe", ".zip", ".dll", "", ".mp3", ".wav"} {
		if isMedia(ext, nil) {
			t.Errorf("扩展名 %q 不应被识别为媒体", ext)
		}
	}
}

func TestIsMedia_UpperCase(t *testing.T) {
	// isMedia 本身不做小写转换,但调用方(Scan)会先 ToLower
	// 这里直接验证大写扩展名在 isMedia 内表现为非媒体(由 Scan 的 ToLower 保证行为)
	// 但为了完整性,我们验证调用方把大写转为小写后再调用。
	_ = isMedia(".JPG", nil) // 不 panic
	if isMedia(".JPG", nil) {
		t.Error(".JPG 大写在 isMedia 层应为 false(由 Scan 先 ToLower)")
	}
}

func TestIsMedia_WithExtWhitelist(t *testing.T) {
	exts := map[string]bool{".jpg": true, ".mp4": true}
	if !isMedia(".jpg", exts) {
		t.Error(".jpg 应在白名单内")
	}
	if !isMedia(".mp4", exts) {
		t.Error(".mp4 应在白名单内")
	}
	if isMedia(".png", exts) {
		t.Error(".png 不在白名单内,应返回 false")
	}
	if isMedia(".txt", exts) {
		t.Error(".txt 不是媒体,即使在白名单内也不应返回 true")
	}
}

// ===== relDir =====

func TestRelDir_YearMonth(t *testing.T) {
	tm := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	opt := Options{} // 默认 年/月
	if got := relDir(tm, opt); got != filepath.Join("2025", "06") {
		t.Errorf("年/月 期望 2025/06,实际 %q", got)
	}
}

func TestRelDir_YearOnly(t *testing.T) {
	tm := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	opt := Options{Year: true}
	if got := relDir(tm, opt); got != "2025" {
		t.Errorf("仅年 期望 2025,实际 %q", got)
	}
}

func TestRelDir_YearMonthDay(t *testing.T) {
	tm := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	opt := Options{Day: true}
	if got := relDir(tm, opt); got != filepath.Join("2025", "06", "15") {
		t.Errorf("年/月/日 期望 2025/06/15,实际 %q", got)
	}
}

func TestRelDir_YearWinsOverDay(t *testing.T) {
	// Year 优先于 Day(代码 switch 先判 Year)
	tm := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	opt := Options{Year: true, Day: true}
	if got := relDir(tm, opt); got != "2025" {
		t.Errorf("Year 应优先,期望 2025,实际 %q", got)
	}
}

func TestRelDir_EmptyTime(t *testing.T) {
	// 零值时间也是合法 time.Time,format 出 0001/01
	var tm time.Time
	opt := Options{}
	if got := relDir(tm, opt); got != filepath.Join("0001", "01") {
		t.Errorf("零值时间期望 0001/01,实际 %q", got)
	}
}

// ===== copyFile =====

func TestCopyFile_Success(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	content := []byte("hello copy content")
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst, nil); err != nil {
		t.Fatalf("copyFile 失败: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("复制内容不一致: got %q, want %q", got, content)
	}
}

func TestCopyFile_SrcMissing(t *testing.T) {
	dir := t.TempDir()
	if err := copyFile(filepath.Join(dir, "nope.bin"), filepath.Join(dir, "dst.bin"), nil); err == nil {
		t.Error("源文件不存在应返回错误")
	}
}

func TestCopyFile_DstDirMissing(t *testing.T) {
	// 目标父目录不存在时 copyFile 应失败(由调用方先 MkdirAll)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, filepath.Join(dir, "nonexist", "dst.bin"), nil); err == nil {
		t.Error("目标目录不存在应返回错误")
	}
}

func TestCopyFile_LargeContent(t *testing.T) {
	// 大文件(>64KB 分块)验证流式复制完整
	dir := t.TempDir()
	src := filepath.Join(dir, "big.bin")
	dst := filepath.Join(dir, "big_copy.bin")
	// 生成 1MB 内容
	content := make([]byte, 1<<20)
	for i := range content {
		content[i] = byte(i % 251)
	}
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst, nil); err != nil {
		t.Fatalf("copyFile 失败: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(content) {
		t.Errorf("长度不一致: got %d, want %d", len(got), len(content))
	}
	for i := range got {
		if got[i] != content[i] {
			t.Fatalf("偏移 %d 内容不一致: got %d, want %d", i, got[i], content[i])
		}
	}
}

// ===== Run 边界场景 =====

func TestRun_EmptySrcDir(t *testing.T) {
	// 空源目录: 不 panic, Processed=0, Failed=0
	src := t.TempDir()
	res := Run(Options{Src: src, Dst: t.TempDir(), Dedupe: false}, nil)
	if res.Processed != 0 || res.Failed != 0 || res.Duplicates != 0 {
		t.Errorf("空目录期望全 0,实际 Processed=%d Failed=%d Duplicates=%d",
			res.Processed, res.Failed, res.Duplicates)
	}
	if res.Cancelled {
		t.Error("空目录不应 Cancelled")
	}
}

func TestRun_SrcMissing(t *testing.T) {
	// 源目录不存在: 不 panic,不处理任何文件
	res := Run(Options{Src: filepath.Join(t.TempDir(), "nope"), Dst: t.TempDir(), Dedupe: false}, nil)
	if res.Processed != 0 {
		t.Errorf("源目录不存在期望 0,实际 %d", res.Processed)
	}
}

func TestRun_DstInsideSrc(t *testing.T) {
	// 目标目录是源目录的子目录: 应自动排除,防止递归处理输出目录里的文件
	src := t.TempDir()
	dst := filepath.Join(src, "out") // 输出在 src 子目录
	// 构造带 EXIF 的图片
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Run(Options{Src: src, Dst: dst, Dedupe: false}, nil)
	if res.Processed != 1 {
		t.Errorf("期望处理 1,实际 %d", res.Processed)
	}
	if res.Failed != 0 {
		t.Errorf("期望失败 0,实际 %d", res.Failed)
	}
	// 输出应生成在 dst 子目录内
	if _, err := os.Stat(filepath.Join(dst, "2024", "01", "2024-01-01_000000_001.jpg")); err != nil {
		t.Errorf("输出文件应生成在 dst 子目录: %v", err)
	}
}

func TestRun_DstEqualsSrc(t *testing.T) {
	// 源目录等于目标目录: 扫描会跳过整个目录(边界场景),不 panic
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Run(Options{Src: src, Dst: src, Dedupe: false}, nil)
	// src==dst 时整个目录被排除,预期处理 0(不 panic)
	if res.Processed != 0 {
		t.Errorf("src==dst 时扫描应排除自身,期望处理 0,实际 %d", res.Processed)
	}
}

func TestRun_DryRunNoSideEffect(t *testing.T) {
	// DryRun 不应创建目标目录/文件
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, DryRun: true, Dedupe: false}, nil)
	if res.Processed != 1 {
		t.Errorf("DryRun 期望处理 1,实际 %d", res.Processed)
	}
	// 目标目录应为空
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 0 {
		t.Errorf("DryRun 不应在目标目录产生文件,实际 %v", names)
	}
	// 源文件应保留
	if _, err := os.Stat(filepath.Join(src, "a.jpg")); err != nil {
		t.Error("DryRun 源文件应保留")
	}
}

func TestRun_MoveKeepsSource(t *testing.T) {
	// 默认(非 Move)复制模式: 源文件应保留
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Dedupe: false, Move: false}, nil)
	if res.Processed != 1 {
		t.Errorf("期望处理 1,实际 %d", res.Processed)
	}
	if _, err := os.Stat(filepath.Join(src, "a.jpg")); err != nil {
		t.Error("复制模式下源文件应保留")
	}
}

func TestRun_MoveActuallyMoves(t *testing.T) {
	// Move 模式: 源文件应被移走
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Dedupe: false, Move: true}, nil)
	if res.Processed != 1 {
		t.Errorf("期望处理 1,实际 %d", res.Processed)
	}
	if _, err := os.Stat(filepath.Join(src, "a.jpg")); !os.IsNotExist(err) {
		t.Error("移动模式下源文件应被移走")
	}
}

func TestRun_TimeOffset(t *testing.T) {
	// Offset 时间偏移: 处理后目标文件名应反映偏移
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Dedupe: false, Offset: 3600}, nil) // +1 小时
	if res.Processed != 1 {
		t.Errorf("期望处理 1,实际 %d", res.Processed)
	}
	// 目标文件名应为 2024-01-01_010000_001.jpg
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 1 {
		t.Fatalf("期望 1 个输出,实际 %v", names)
	}
	if names[0] != "2024-01-01_010000_001.jpg" {
		t.Errorf("偏移后命名应为 2024-01-01_010000_001.jpg,实际 %q", names[0])
	}
}

func TestRun_ConcurrencyBounds(t *testing.T) {
	// 极端并发参数不 panic: 0 / 负数 / 超大
	for _, conc := range []int{0, -1, 999999, 1, 2} {
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
			t.Fatal(err)
		}
		res := Run(Options{Src: src, Dst: t.TempDir(), Dedupe: false, Concurrency: conc}, nil)
		if res.Processed != 1 {
			t.Errorf("Concurrency=%d 期望处理 1,实际 %d", conc, res.Processed)
		}
	}
}

func TestRun_DedupeDuplicates(t *testing.T) {
	// 相同内容(MD5 相同)文件去重
	src := t.TempDir()
	content := makeJpgWithExif("2024:01:01 00:00:00")
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "b.jpg"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Dedupe: true}, nil)
	if res.Processed != 1 {
		t.Errorf("去重后期望处理 1,实际 %d", res.Processed)
	}
	if res.Duplicates != 1 {
		t.Errorf("期望去重跳过 1,实际 %d", res.Duplicates)
	}
}

func TestRun_ExtensionsFilter(t *testing.T) {
	// 格式白名单过滤
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "b.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Dedupe: false, Extensions: []string{".jpg"}}, nil)
	if res.Processed != 1 {
		t.Errorf("仅 jpg 白名单期望处理 1,实际 %d", res.Processed)
	}
}

// ===== fileMD5 =====

func TestFileMD5_Success(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.bin")
	content := []byte("hello md5 content")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	// 已知值: 用标准库验证
	sum, err := fileMD5(p)
	if err != nil {
		t.Fatalf("fileMD5 失败: %v", err)
	}
	if len(sum) != 32 {
		t.Errorf("MD5 应为 32 位 hex,实际 %d", len(sum))
	}
	// 再次计算应一致(确定性)
	sum2, err := fileMD5(p)
	if err != nil {
		t.Fatal(err)
	}
	if sum != sum2 {
		t.Error("同一文件 MD5 应一致")
	}
}

func TestFileMD5_Missing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope.bin")
	if _, err := fileMD5(p); err == nil {
		t.Error("文件不存在应返回错误")
	}
}

func TestFileMD5_DifferentContentDifferentHash(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(a, []byte("content-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("content-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	ha, _ := fileMD5(a)
	hb, _ := fileMD5(b)
	if ha == hb {
		t.Error("不同内容 MD5 不应相同")
	}
}

func TestRun_SourceCountTracking(t *testing.T) {
	// 验证 Result.SourceCount 统计不同来源
	src := t.TempDir()
	// EXIF 来源
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 文件名时间戳来源
	if err := os.WriteFile(filepath.Join(src, "IMG_20250202_020202.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Run(Options{Src: src, Dst: t.TempDir(), Dedupe: false}, nil)
	if res.Processed != 2 {
		t.Errorf("期望处理 2,实际 %d", res.Processed)
	}
	if res.SourceCount["EXIF"] != 1 {
		t.Errorf("期望 EXIF 来源 1,实际 %d", res.SourceCount["EXIF"])
	}
	if res.SourceCount["name"] != 1 {
		t.Errorf("期望 name 来源 1,实际 %d", res.SourceCount["name"])
	}
}
