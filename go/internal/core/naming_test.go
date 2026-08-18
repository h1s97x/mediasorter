package core

import (
	"os"
	"path/filepath"
	"sort"
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
