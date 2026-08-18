package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestRunConcurrentExistingTarget 验证磁盘已有同名旧文件时,并发处理不会重名覆盖。
// 这是评审指出的竞态场景: guard 递增的最终序号必须回写 counter,否则并发下
// 两个 worker 可能分配到相同的 seq,写同一目标文件而相互覆盖,导致输出文件数减少。
func TestRunConcurrentExistingTarget(t *testing.T) {
	for round := 0; round < 20; round++ {
		src := t.TempDir()
		const n = 120
		for i := 0; i < n; i++ {
			if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("old_%d.jpg", i)),
				makeJpgWithExif("2020:02:02 02:02:02"), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		dst := t.TempDir()
		sub := filepath.Join("2020", "02")
		if err := os.MkdirAll(filepath.Join(dst, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		// 预置磁盘旧文件,占用低序号,迫使 guard 递增(模拟重复运行到同一目录)
		// 预置数量超过并发数,制造 counter 起点与磁盘占用错位的时机
		pre := 30
		for s := 1; s <= pre; s++ {
			if err := os.WriteFile(filepath.Join(dst, sub, fmt.Sprintf("old_%03d.jpg", s)),
				[]byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		res := Run(Options{Src: src, Dst: dst, Concurrency: 16, Dedupe: false}, nil)
		if res.Processed != n {
			t.Fatalf("round %d: 期望处理 %d,实际 %d", round, n, res.Processed)
		}
		if res.Failed != 0 {
			t.Fatalf("round %d: 期望失败 0,实际 %d", round, res.Failed)
		}
		var names []string
		filepathWalkNames(t, dst, &names)
		if len(names) != n+pre {
			t.Fatalf("round %d: 期望 %d 个文件(新增%d+预置%d),实际 %d → 疑似重名覆盖",
				round, n+pre, n, pre, len(names))
		}
		seen := map[string]bool{}
		for _, nm := range names {
			if seen[nm] {
				t.Fatalf("round %d: 出现重名文件: %s", round, nm)
			}
			seen[nm] = true
		}
		// 预置的 old_001..old_030 应保留,新文件应避开已占序号
		for s := 1; s <= pre; s++ {
			if !seen[fmt.Sprintf("old_%03d.jpg", s)] {
				t.Fatalf("round %d: 预置文件被覆盖丢失: old_%03d.jpg", round, s)
			}
		}
	}
}

// TestRunConcurrentExistingTargetSerial 串行对照: 预置旧文件后,新文件序号应从预置之后继续。
func TestRunConcurrentExistingTargetSerial(t *testing.T) {
	src := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("s_%d.jpg", i)),
			makeJpgWithExif("2021:03:03 03:03:03"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dst := t.TempDir()
	sub := filepath.Join("2021", "03")
	if err := os.MkdirAll(filepath.Join(dst, sub), 0o755); err != nil {
		t.Fatal(err)
	}
	// 默认时间命名,所有源文件 baseName 相同(2021-03-03_030303);
	// 预置已占用的目标文件 _001.._003,新文件序号应从 _004 起。
	base := "2021-03-03_030303"
	for _, s := range []string{"001", "002", "003"} {
		if err := os.WriteFile(filepath.Join(dst, sub, base+"_"+s+".jpg"), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res := Run(Options{Src: src, Dst: dst, Concurrency: 1, Dedupe: false}, nil)
	if res.Processed != 10 {
		t.Fatalf("期望处理 10,实际 %d", res.Processed)
	}
	var names []string
	filepathWalkNames(t, dst, &names)
	seen := map[string]bool{}
	for _, nm := range names {
		if seen[nm] {
			t.Fatalf("重名: %s", nm)
		}
		seen[nm] = true
	}
	// 预置的 _001.._003 应保留,新文件序号应从 _004.._013
	for _, s := range []string{"001", "002", "003"} {
		if !seen[base+"_"+s+".jpg"] {
			t.Fatalf("预置文件被覆盖丢失: %s_%s.jpg", base, s)
		}
	}
	for s := 4; s <= 13; s++ {
		if !seen[fmt.Sprintf("%s_%03d.jpg", base, s)] {
			t.Fatalf("缺少新文件: %s_%03d.jpg", base, s)
		}
	}
}
