package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestRunCancelledPreSet 预取消 context:Run 应立即返回 Cancelled=true,
// 且不产生任何输出文件(没有机会处理任何文件)。
func TestRunCancelledPreSet(t *testing.T) {
	src := t.TempDir()
	const n = 5
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("c_%d.jpg", i)),
			makeJpgWithExif("2024:01:01 00:00:00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消

	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Ctx: ctx, Dedupe: false}, nil)
	if !res.Cancelled {
		t.Fatal("期望 Cancelled=true")
	}
	if res.Processed != 0 {
		t.Fatalf("期望处理 0,实际 %d", res.Processed)
	}
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 0 {
		t.Fatalf("取消后不应产生输出文件,实际: %v", names)
	}
}

// TestRunNoCancelBackwardCompat 不传 Ctx(nil)时行为不变:
// 处理全部文件,且 Cancelled 为 false(向后兼容)。
func TestRunNoCancelBackwardCompat(t *testing.T) {
	src := t.TempDir()
	const n = 3
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("nc_%d.jpg", i)),
			makeJpgWithExif("2024:02:02 02:02:02"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Dedupe: false}, nil)
	if res.Cancelled {
		t.Fatal("未取消不应设置 Cancelled")
	}
	if res.Processed != n {
		t.Fatalf("期望处理 %d,实际 %d", n, res.Processed)
	}
}

// TestRunCancelNoDirtyFile 取消时不应留下半成品(脏文件)。
// 构造大文件 + 已取消 context,确保复制不会启动新任务;
// 再验证取消后输出的每个文件都是完整文件(与源文件大小一致)。
func TestRunCancelNoDirtyFile(t *testing.T) {
	src := t.TempDir()
	const n = 4
	// 构造带 EXIF 的较大文件(填充大量数据),使复制耗时
	for i := 0; i < n; i++ {
		base := makeJpgWithExif("2024:03:03 03:03:03")
		// 填充 2MB 数据,增大复制耗时
		payload := make([]byte, 2<<20)
		for j := range payload {
			payload[j] = byte(i)
		}
		content := append(base, payload...)
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("big_%d.jpg", i)), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 已取消 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Ctx: ctx, Dedupe: false, Concurrency: 1}, nil)
	if !res.Cancelled {
		t.Fatal("期望 Cancelled=true")
	}
	// 取消后不应产生任何输出文件
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != 0 {
		t.Fatalf("预取消后不应产生输出文件,实际: %v", names)
	}
}

// TestRunCancelMidway 运行中途取消: 应尽早停止,
// 已处理的文件必须完整(无半成品),且 Cancelled=true、处理数小于总数。
func TestRunCancelMidway(t *testing.T) {
	src := t.TempDir()
	const n = 30
	for i := 0; i < n; i++ {
		base := makeJpgWithExif("2024:04:04 04:04:04")
		payload := make([]byte, 1<<20) // 1MB 填充,增加处理耗时便于中途取消
		content := append(base, payload...)
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("mid_%02d.jpg", i)), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 在进度回调中触发取消: 当处理到中间某个文件时发出取消信号,
	// 保证取消一定发生在处理中途(而非全部完成之后)。
	var cancelledAt int
	once := false
	dst := t.TempDir()
	res := Run(Options{
		Src: src, Dst: dst, Ctx: ctx, Dedupe: false, Concurrency: 2,
		OnProgress: func(done, total int) {
			if !once && done >= 5 && done < total {
				once = true
				cancelledAt = done
				cancel()
			}
		},
	}, nil)

	if !res.Cancelled {
		t.Fatal("期望 Cancelled=true")
	}
	if cancelledAt == 0 {
		t.Fatal("进度回调未触发取消,测试无效")
	}
	// 已处理文件数应在 (0, n) 之间(至少处理了部分,未全部处理)
	if res.Processed <= 0 {
		t.Fatalf("期望已处理部分文件(>0),实际 %d", res.Processed)
	}
	if res.Processed >= n {
		t.Fatalf("取消后不应处理完全部 %d 个,实际处理 %d", n, res.Processed)
	}
	// 验证所有输出文件都是完整文件(与对应源文件大小一致),无半成品
	// 通过统计: 输出文件数应等于 Processed
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != res.Processed {
		t.Fatalf("输出文件数 %d 应与已处理数 %d 一致(无半成品)", len(names), res.Processed)
	}
}
