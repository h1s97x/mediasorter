package core

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func filepathWalkNames(t *testing.T, root string, names *[]string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			*names = append(*names, filepath.Base(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(*names)
}

// TestBaseNameDefault 验证默认的规范时间命名
func TestBaseNameDefault(t *testing.T) {
	tm := time.Date(2025, 3, 4, 12, 34, 56, 0, time.UTC)
	opt := Options{}
	got := baseName("/src/IMG_20200101_000000.jpg", tm, opt)
	want := "2025-03-04_123456"
	if got != want {
		t.Fatalf("默认命名期望 %q,实际 %q", want, got)
	}
}

// TestBaseNameKeepOriginal 验证保留原始文件名
func TestBaseNameKeepOriginal(t *testing.T) {
	tm := time.Date(2025, 3, 4, 12, 34, 56, 0, time.UTC)
	opt := Options{KeepOriginal: true}
	got := baseName("/src/IMG_20200101_000000.jpg", tm, opt)
	want := "IMG_20200101_000000"
	if got != want {
		t.Fatalf("保留原命名期望 %q,实际 %q", want, got)
	}
}

// TestBaseNamePrefixSuffix 验证前后缀
func TestBaseNamePrefixSuffix(t *testing.T) {
	tm := time.Date(2025, 3, 4, 12, 34, 56, 0, time.UTC)
	// 默认命名 + 前后缀
	opt := Options{NamePrefix: "旅行_", NameSuffix: "_ok"}
	if got := baseName("/src/a.jpg", tm, opt); got != "旅行_2025-03-04_123456_ok" {
		t.Fatalf("期望 旅行_2025-03-04_123456_ok,实际 %q", got)
	}
	// 保留原命名 + 前后缀
	opt2 := Options{KeepOriginal: true, NamePrefix: "旅行_", NameSuffix: "_ok"}
	if got := baseName("/src/IMG_1.jpg", tm, opt2); got != "旅行_IMG_1_ok" {
		t.Fatalf("期望 旅行_IMG_1_ok,实际 %q", got)
	}
}

// TestBaseNameNameLayout 验证 NameLayout 模板的 5 种 Separator 预设
func TestBaseNameNameLayout(t *testing.T) {
	tm := time.Date(2025, 3, 4, 12, 34, 56, 0, time.UTC)
	src := "/src/IMG_123.jpg"
	cases := []struct {
		name, layout, want string
	}{
		{"{ts}{seq}(带序号)", "{ts}{seq}", "2025-03-04_123456{seq}"},
		{"{ts}(仅时间戳)", "{ts}", "2025-03-04_123456"},
		{"{ts} {orig}(时间+原始名)", "{ts} {orig}", "2025-03-04_123456 IMG_123"},
		{"{date}_{orig}(日期+原始名)", "{date}_{orig}", "2025-03-04_IMG_123"},
		{"{orig}(仅原始名)", "{orig}", "IMG_123"},
	}
	for _, c := range cases {
		opt := Options{NameLayout: c.layout}
		got := baseName(src, tm, opt)
		if got != c.want {
			t.Errorf("%s: 期望 %q,实际 %q", c.name, c.want, got)
		}
	}
}

// TestBaseNameNameLayoutPrefixSuffix 验证 NameLayout 模板与前后缀组合
func TestBaseNameNameLayoutPrefixSuffix(t *testing.T) {
	tm := time.Date(2025, 3, 4, 12, 34, 56, 0, time.UTC)
	opt := Options{NameLayout: "{date}_{orig}", NamePrefix: "旅行_", NameSuffix: "_ok"}
	if got := baseName("/src/IMG_1.jpg", tm, opt); got != "旅行_2025-03-04_IMG_1_ok" {
		t.Fatalf("期望 旅行_2025-03-04_IMG_1_ok,实际 %q", got)
	}
}

// TestBaseNameNameLayoutPrecedence 验证 NameLayout 优先于旧 KeepOriginal
func TestBaseNameNameLayoutPrecedence(t *testing.T) {
	tm := time.Date(2025, 3, 4, 12, 34, 56, 0, time.UTC)
	// 同时设置 NameLayout 与 KeepOriginal,应以 NameLayout 为准
	opt := Options{KeepOriginal: true, NameLayout: "{ts}"}
	if got := baseName("/src/IMG_1.jpg", tm, opt); got != "2025-03-04_123456" {
		t.Fatalf("期望 2025-03-04_123456(以 NameLayout 为准),实际 %q", got)
	}
}

// TestRunSeqLayoutAlwaysNumbered 端到端: 模板含 {seq} 时每个文件都带序号
func TestRunSeqLayoutAlwaysNumbered(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 两个同拍摄时间(不同源目录名)的文件,{ts} 相同,{seq} 保证区分
	writeTestFile(t, src+"/a", "x.jpg", "content1")
	writeTestFile(t, src+"/b", "y.jpg", "content2")

	res := Run(Options{Src: src, Dst: dst, Dedupe: false, NameLayout: "{ts}{seq}"}, nil)
	if res.Processed != 2 {
		t.Fatalf("期望处理 2 个,实际 %d", res.Processed)
	}
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 2 {
		t.Fatalf("期望 2 个输出文件,实际 %d: %v", len(names), names)
	}
	// 两个文件都应带 _001/_002 序号,且不相同
	if names[0] == names[1] {
		t.Fatalf("两个文件不应同名: %v", names)
	}
	for _, n := range names {
		if !strings.Contains(n, "_00") {
			t.Fatalf("模板含 {seq} 应始终带序号,实际 %q", n)
		}
	}
}

// TestRunOriginalNameLayout 端到端: {orig} 模板保留原始名(参考图 Original-Filename)
func TestRunOriginalNameLayout(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTestFile(t, src, "hello.jpg", "content")

	res := Run(Options{Src: src, Dst: dst, Dedupe: false, NameLayout: "{orig}"}, nil)
	if res.Processed != 1 {
		t.Fatalf("期望处理 1 个,实际 %d", res.Processed)
	}
	var names []string
	filepathWalkNames(t, dst, &names)
	// 无冲突时 {orig} 模板应保留原始名,不加序号
	if len(names) != 1 || names[0] != "hello.jpg" {
		t.Fatalf("期望 hello.jpg,实际 %v", names)
	}
}

// TestRunOriginalNameLayoutCollision 端到端: {orig} 模板在同名冲突时才追加序号
func TestRunOriginalNameLayoutCollision(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 两个不同目录的同名文件,{orig} 模板下应冲突追加序号
	writeTestFile(t, src+"/a", "IMG_1.jpg", "content1")
	writeTestFile(t, src+"/b", "IMG_1.jpg", "content2")

	res := Run(Options{Src: src, Dst: dst, Dedupe: false, NameLayout: "{orig}", DirLayout: "{dir}"}, nil)
	if res.Processed != 2 {
		t.Fatalf("期望处理 2 个,实际 %d", res.Processed)
	}
	var names []string
	filepathWalkNames(t, dst, &names)
	// 首个保留原始名,第二个因同名冲突追加序号
	if len(names) != 2 {
		t.Fatalf("期望 2 个输出文件,实际 %v", names)
	}
	if names[0] != "IMG_1.jpg" {
		t.Fatalf("期望首个为 IMG_1.jpg,实际 %q", names[0])
	}
	if names[1] != "IMG_1_002.jpg" {
		t.Fatalf("期望第二个为 IMG_1_002.jpg,实际 %q", names[1])
	}
}

// TestRunKeepOriginalNaming 端到端: 用 Run 验证保留原始名 + 冲突序号
func TestRunKeepOriginalNaming(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 两个同名文件在不同目录,保留原始名时应加序号区分
	writeTestFile(t, src+"/a", "IMG_1.jpg", "content1")
	writeTestFile(t, src+"/b", "IMG_1.jpg", "content2")

	res := Run(Options{Src: src, Dst: dst, KeepOriginal: true, Dedupe: false}, nil)
	if res.Processed != 2 {
		t.Fatalf("期望处理 2 个,实际 %d", res.Processed)
	}
	// 验证目标目录有两个不同文件名的 IMG_1
	// 由于目录结构是 年/月,需递归查找
	var names []string
	filepathWalkNames(t, dst, &names)
	// 应该有两个 IMG_1_001.jpg / IMG_1_002.jpg(或类似)
	if len(names) != 2 {
		t.Fatalf("期望 2 个输出文件,实际 %d: %v", len(names), names)
	}
	if names[0] == names[1] {
		t.Fatalf("两个文件不应同名: %v", names)
	}
}
