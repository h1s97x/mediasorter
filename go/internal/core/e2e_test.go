// 端到端集成测试: 构造真实混合媒体目录,验证 Run 全流程(扫描→去重→时间提取→归档→统计)
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_MixedMediaDirectory 构造一个含多种来源文件、子目录、不同格式的混合目录,
// 一次性验证: 扫描(递归+排除dst) → 时间提取四级兜底 → 目录结构(年/月) → 命名 → 去重 → 统计。
func TestE2E_MixedMediaDirectory(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// ---- 准备数据 ----
	// 1. 带 EXIF 的 JPEG(子目录中,验证递归)
	sub1 := filepath.Join(src, "camera")
	if err := os.MkdirAll(sub1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub1, "DSC_001.jpg"),
		makeJpgWithExif("2023:03:15 10:30:00"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 2. 文件名时间戳 PNG(顶层)
	if err := os.WriteFile(filepath.Join(src, "Screenshot_2023-04-20-143322.png"),
		[]byte("png-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 3. 无任何元数据的 JPEG → 走 mtime 兜底
	plain := filepath.Join(src, "plain.jpg")
	if err := os.WriteFile(plain, []byte{0xff, 0xd8, 0xff, 0xd9}, 0o600); err != nil {
		t.Fatal(err)
	}
	plainTime := time.Date(2022, 12, 1, 8, 0, 0, 0, time.Local)
	if err := os.Chtimes(plain, plainTime, plainTime); err != nil {
		t.Fatal(err)
	}

	// 4. 重复文件(与 #1 内容相同,验证去重)
	if err := os.WriteFile(filepath.Join(src, "dup.jpg"),
		makeJpgWithExif("2023:03:15 10:30:00"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 5. 非媒体文件(不应被扫描)
	if err := os.WriteFile(filepath.Join(src, "readme.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 6. 目标目录预置一个文件(dst 已被排除,不应影响扫描)
	if err := os.MkdirAll(filepath.Join(dst, "2023", "03"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "2023", "03", "pre.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// ---- 执行 Run ----
	res := Run(Options{Src: src, Dst: dst, Dedupe: true, KeepOriginal: false}, func(s string) {})

	// ---- 断言 ----
	// 去重后应处理 3 个(DSC_001 / Screenshot / plain),去重跳过 1 个(dup)
	if res.Processed != 3 {
		t.Errorf("期望处理 3,实际 %d", res.Processed)
	}
	if res.Duplicates != 1 {
		t.Errorf("期望去重 1,实际 %d", res.Duplicates)
	}
	if res.Failed != 0 {
		t.Errorf("期望失败 0,实际 %d", res.Failed)
	}

	// 时间跨度
	if res.TimeSpanMin.IsZero() {
		t.Error("TimeSpanMin 不应为零值")
	}
	// 最小时间应为 plain.jpg 的 mtime(容忍几秒误差,避免文件系统精度问题)
	diff := res.TimeSpanMin.Sub(plainTime)
	if diff < -time.Minute || diff > time.Minute {
		t.Errorf("TimeSpanMin 应接近 %v,实际 %v(差 %v)", plainTime, res.TimeSpanMin, diff)
	}

	// 来源统计: EXIF 1 + name 1 + mtime 1
	if res.SourceCount["EXIF"] != 1 {
		t.Errorf("期望 EXIF 来源 1,实际 %d", res.SourceCount["EXIF"])
	}
	if res.SourceCount["name"] != 1 {
		t.Errorf("期望 name 来源 1,实际 %d", res.SourceCount["name"])
	}
	if res.SourceCount["mtime"] != 1 {
		t.Errorf("期望 mtime 来源 1,实际 %d", res.SourceCount["mtime"])
	}

	// 验证输出文件存在且命名正确
	var outNames []string
	filepathWalkNames(t, dst, &outNames)
	// 预置的 pre.txt 应保留 + 3 个新文件
	if len(outNames) != 4 {
		t.Fatalf("期望 4 个输出文件(含 pre.txt),实际 %d: %v", len(outNames), outNames)
	}
	// 预置 pre.txt 保留(通过 contains 检查,不依赖排序顺序)
	if !containsString(outNames, "pre.txt") {
		t.Errorf("预置文件 pre.txt 应保留,实际 %v", outNames)
	}
	// 3 个新文件命名应为规范时间名
	seen := map[string]bool{}
	for _, n := range outNames {
		if n != "pre.txt" {
			seen[n] = true
		}
	}
	// DSC_001 (EXIF 2023-03-15 10:30) → 2023-03-15_103000_001.jpg
	if !seen["2023-03-15_103000_001.jpg"] {
		t.Errorf("缺少 EXIF 来源的输出 2023-03-15_103000_001.jpg,got %v", outNames)
	}
	// Screenshot (文件名 2023-04-20 14:33) → 2023-04-20_143322_001.png
	if !seen["2023-04-20_143322_001.png"] {
		t.Errorf("缺少文件名来源输出 2023-04-20_143322_001.png,got %v", outNames)
	}
	// plain (mtime 2022-12-01) → 2022-12-01_080000_001.jpg
	if !seen["2022-12-01_080000_001.jpg"] {
		t.Errorf("缺少 mtime 来源输出 2022-12-01_080000_001.jpg,got %v", outNames)
	}

	// 目录结构: 2022/12, 2023/03, 2023/04
	for _, sub := range []string{filepath.Join("2022", "12"), filepath.Join("2023", "03"), filepath.Join("2023", "04")} {
		if _, err := os.Stat(filepath.Join(dst, sub)); err != nil {
			t.Errorf("缺少子目录 %s: %v", sub, err)
		}
	}
}

// TestE2E_SameTimestampNaming 同时间戳大量文件: 验证序号递增、命名连续、无重名。
func TestE2E_SameTimestampNaming(t *testing.T) {
	src := t.TempDir()
	const n = 20
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("img_%02d.jpg", i)),
			makeJpgWithExif("2024:05:05 05:05:05"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Dedupe: false}, nil)
	if res.Processed != n {
		t.Fatalf("期望处理 %d,实际 %d", n, res.Processed)
	}
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != n {
		t.Fatalf("期望 %d 个输出,实际 %d: %v", n, len(names), names)
	}
	// 文件名应为 2024-05-05_050505_001.jpg ~ _020.jpg 连续
	for i := 1; i <= n; i++ {
		want := fmt.Sprintf("2024-05-05_050505_%03d.jpg", i)
		if !containsString(names, want) {
			t.Errorf("缺少输出文件 %s,got %v", want, names)
		}
	}
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestE2E_RunTwiceIdempotent 重复运行到同一目录: 不覆盖、不丢失、序号递增。
func TestE2E_RunTwiceIdempotent(t *testing.T) {
	src := t.TempDir()
	// 两批不同时间的文件
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("batch1_%d.jpg", i)),
			makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dst := t.TempDir()

	// 第一次运行
	res1 := Run(Options{Src: src, Dst: dst, Dedupe: false}, nil)
	if res1.Processed != 3 {
		t.Fatalf("第一次运行期望处理 3,实际 %d", res1.Processed)
	}

	// 第二次运行(源仍含这 3 个文件)
	res2 := Run(Options{Src: src, Dst: dst, Dedupe: false}, nil)
	// 第二次运行应成功,且不覆盖已有文件(文件数从 3 变 6,因为同名但序号不同)
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 6 {
		t.Fatalf("两次运行期望共 6 个文件(不覆盖),实际 %d: %v", len(names), names)
	}
	if res2.Failed != 0 {
		t.Errorf("第二次运行不应失败,实际 %d", res2.Failed)
	}
	// 验证没有重名
	seen := map[string]bool{}
	for _, nm := range names {
		if seen[nm] {
			t.Errorf("出现重名: %s", nm)
		}
		seen[nm] = true
	}
}

// TestE2E_LogCallback Invoke log callback during Run.
func TestE2E_LogCallback(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs []string
	res := Run(Options{Src: src, Dst: t.TempDir(), Dedupe: false}, func(s string) {
		logs = append(logs, s)
	})
	if res.Processed != 1 {
		t.Fatalf("期望处理 1,实际 %d", res.Processed)
	}
	if len(logs) == 0 {
		t.Error("log 回调应被调用")
	}
}

// TestE2E_DirLayoutStructures 验证多种目录结构模板(DirLayout)实际落盘目录正确。
// 覆盖参考图的几类典型结构: 根目录平铺 / YYYY-YYYY-MM / 按原目录名。
func TestE2E_DirLayoutStructures(t *testing.T) {
	cases := []struct {
		name      string
		layout    string
		expectRel string // 期望的目标相对路径(不含文件名)
		flatTop   bool   // 根目录平铺(文件直接放 dst 根)
	}{
		{"根目录平铺(带时间戳文件名)", "{flat}", "", true},
		{"YYYY/YYYY-MM", "2006/2006-01", "2023/2023-03", false},
		{"YYYY/YYYY-MM/YYYY-MM-DD", "2006/2006-01/2006-01-02", "2023/2023-03/2023-03-15", false},
		{"YYYY-MM-DD", "2006-01-02", "2023-03-15", false},
		{"按原目录名", "{dir}", "album", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			dst := t.TempDir()
			// 照片放在 album 子目录,验证 {dir} 取源目录名
			album := filepath.Join(src, "album")
			if err := os.MkdirAll(album, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(album, "photo.jpg"),
				makeJpgWithExif("2023:03:15 10:30:00"), 0o600); err != nil {
				t.Fatal(err)
			}

			opt := Options{Src: src, Dst: dst, Dedupe: true, DirLayout: tc.layout}
			res := Run(opt, func(s string) {})
			if res.Processed != 1 {
				t.Fatalf("期望处理 1,实际 %d", res.Processed)
			}

			if tc.flatTop {
				// 平铺: 文件直接在 dst 根目录,且命名带时间戳
				entries, err := os.ReadDir(dst)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 1 {
					t.Fatalf("根目录应只有 1 个文件,实际 %d 个", len(entries))
				}
				if !strings.HasPrefix(entries[0].Name(), "2023-03-15_") {
					t.Errorf("平铺文件名应带时间戳前缀,实际 %q", entries[0].Name())
				}
				return
			}

			full := filepath.Join(append([]string{dst}, strings.Split(tc.expectRel, "/")...)...)
			fi, err := os.Stat(full)
			if err != nil || !fi.IsDir() {
				t.Errorf("期望目录 %q 存在,错误=%v", full, err)
			}
			// 目录内应有整理后的文件
			sub, err := os.ReadDir(full)
			if err != nil || len(sub) != 1 {
				t.Errorf("期望目录内有 1 个文件,实际 len=%d err=%v", len(sub), err)
			}
		})
	}
}
