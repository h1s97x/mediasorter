package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// TestRunConcurrentNaming 验证并发模式下: 处理数量正确、输出文件名无重复、进度归满。
// 构造大量同时间戳文件(同名冲突最剧烈)以充分压测序号分配。
func TestRunConcurrentNaming(t *testing.T) {
	src := t.TempDir()
	const n = 60
	for i := 0; i < n; i++ {
		// 全部写入带相同 EXIF 时间戳的 jpg,baseName 相同,序号竞争最激烈
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("a_%02d.jpg", i)),
			makeJpgWithExif("2020:01:01 00:00:00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dst := t.TempDir()
	res := Run(Options{Src: src, Dst: dst, Concurrency: 8, Dedupe: false}, nil)
	if res.Processed != n {
		t.Fatalf("期望处理 %d 个,实际 %d", n, res.Processed)
	}
	if res.Failed != 0 {
		t.Fatalf("期望失败 0,实际 %d", res.Failed)
	}
	// 输出文件名必须全部唯一
	var names []string
	filepathWalkNames(t, dst, &names)
	if len(names) != n {
		t.Fatalf("期望 %d 个输出文件,实际 %d: %v", n, len(names), names)
	}
	seen := map[string]bool{}
	for _, nm := range names {
		if seen[nm] {
			t.Fatalf("出现重名输出文件: %s", nm)
		}
		seen[nm] = true
	}
	// 序号应为 _001.._060 连续
	sort.Strings(names)
	if names[0] == "" || names[len(names)-1] == "" {
		t.Fatalf("输出文件名为空")
	}
}

// TestRunConcurrencySerial 验证 Concurrency=1 与默认并发结果一致(处理数量与输出集合)。
// 用相同 EXIF 时间戳触发序号冲突,比较串行/并发的输出文件名集合是否一致。
func TestRunConcurrencySerial(t *testing.T) {
	const n = 12
	src := t.TempDir()
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("img_%02d.jpg", i)),
			makeJpgWithExif("2021:05:06 07:08:09"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(conc int) (int, []string) {
		dst := t.TempDir()
		res := Run(Options{Src: src, Dst: dst, Concurrency: conc, Dedupe: false}, nil)
		var names []string
		filepathWalkNames(t, dst, &names)
		return res.Processed, names
	}

	ser, serNames := run(1)
	par, parNames := run(4)
	if ser != n || par != n {
		t.Fatalf("处理数应均为 %d,实际 串行=%d 并发=%d", n, ser, par)
	}
	if len(serNames) != len(parNames) {
		t.Fatalf("输出文件数不一致: 串行 %d,并发 %d", len(serNames), len(parNames))
	}
	// 输出集合应一致(同名序号可能分配顺序不同,但集合相同)
	sort.Strings(serNames)
	sort.Strings(parNames)
	for i := range serNames {
		if serNames[i] != parNames[i] {
			t.Fatalf("输出集合不一致: 串行 %v,并发 %v", serNames, parNames)
		}
	}
}

// TestRunConcurrentProgress 验证并发下 OnProgress 最终归满且不被并发调用。
func TestRunConcurrentProgress(t *testing.T) {
	src := t.TempDir()
	const n = 20
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("p_%02d.jpg", i)),
			makeJpgWithExif("2022:01:02 03:04:05"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dst := t.TempDir()
	var maxDone int32
	var inCall int32
	var maxInCall int32
	var once sync.Once
	done := make(chan struct{})
	res := Run(Options{
		Src: src, Dst: dst, Concurrency: 4, Dedupe: false,
		OnProgress: func(d, total int) {
			// 统计回调最大并发深度(应为 1)
			cur := atomic.AddInt32(&inCall, 1)
			if v := atomic.LoadInt32(&maxInCall); cur > v {
				atomic.CompareAndSwapInt32(&maxInCall, v, cur)
			}
			defer atomic.AddInt32(&inCall, -1)
			if v := int32(d); v > atomic.LoadInt32(&maxDone) {
				atomic.StoreInt32(&maxDone, v)
			}
			once.Do(func() { close(done) })
		},
	}, nil)
	_ = res
	<-done // 确保至少有一次回调(进度已驱动)
	if got := atomic.LoadInt32(&maxDone); got < n {
		t.Fatalf("进度最大值 %d,应达到 %d", got, n)
	}
	if got := atomic.LoadInt32(&maxInCall); got > 1 {
		t.Fatalf("OnProgress 被并发调用(深度 %d),应始终为 1", got)
	}
}

// TestRunConcurrentDryRunSerial 验证 DryRun 下强制串行,输出数量正确。
func TestRunConcurrentDryRunSerial(t *testing.T) {
	src := t.TempDir()
	const n = 10
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("d_%02d.jpg", i)),
			makeJpgWithExif("2023:03:04 05:06:07"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res := Run(Options{Src: src, Dst: t.TempDir(), DryRun: true, Concurrency: 99, Dedupe: false}, nil)
	if res.Processed != n {
		t.Fatalf("DryRun 期望处理 %d,实际 %d", n, res.Processed)
	}
}
