// Package core - 按拍摄时间归档照片/视频的跨平台核心逻辑(纯 Go,无 cgo)
package core

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options 归档选项
type Options struct {
	Src    string // 源目录
	Dst    string // 目标目录
	Move   bool   // true=移动, false=复制(默认,更安全)
	Dedupe bool   // 内容去重(按大小分组+MD5)
	DryRun bool   // true=只预览,不复制/移动,不创建目录
	Offset int    // 时间偏移(秒),修正机内时间差
	Year   bool   // true=仅按年; Day=true=年/月/日; 默认 年/月
	Day    bool
	// Extensions 要处理的扩展名白名单(含点,小写,如 ".jpg");为空表示处理全部支持的媒体格式。
	Extensions []string
	// KeepOriginal true=保留原始文件名(仍会加入前缀/后缀,冲突时追加序号), false=使用规范的 YYYY-MM-DD_HHMMSS_NNN 命名。
	KeepOriginal bool
	// NamePrefix 追加到文件名开头的自定义文本(如 "旅行_")。
	NamePrefix string
	// NameSuffix 追加到扩展名之前的自定义文本(如 "_已整理")。
	NameSuffix string
	// TimeFilter 按是否有录制日期筛选要处理的文件:
	//   ""      = 全部(默认,时间提取失败的文件跳过并计为失败)
	//   "has"   = 仅处理有录制日期(EXIF/视频元数据)的文件
	//   "none"  = 仅处理无录制日期(只能靠文件名/文件时间兜底)的文件
	TimeFilter string
	// StrictTime true=严格时间模式: 只认 EXIF/视频元数据,不把文件名时间戳或
	// 文件修改时间当作拍摄时间。用于避免把 mtime/文件名时间误当拍摄时间造成误排。
	// 默认 false(四级兜底: EXIF -> 元数据 -> 文件名 -> 文件时间)。
	StrictTime bool
	// OnConflict 目标目录存在同名文件时的处理策略:
	//   "sequence" = 自动加序号(默认,绝不覆盖,最安全)
	//   "skip"     = 跳过该文件(计为跳过,不复制/移动)
	//   "overwrite"= 覆盖目标(危险,会丢失已存在目标文件,不建议默认开启)
	OnConflict string
	// OnProgress 进度回调,可空。done=已处理, total=本次总文件数。
	// 注:total 为本次要处理的总数(去重后;扫描得到原始总数后去重会减少),
	// 进度条应以 OnProgress 的 total 为分母,避免去重前后进度跳变。
	OnProgress func(done, total int)
	// Concurrency 归档阶段并行 worker 数量。<=0 时自动取 runtime.NumCPU();
	// 显式设为 1 可强制串行(保持日志/命名顺序,如 --dry-run)。
	Concurrency int
	// Ctx 可选的取消信号。nil 时等价于 context.Background(),行为与旧版一致。
	// 取消后会在处理循环中尽早停止后续文件处理,已复制/移动的单个文件会等待其完成后安全退出,
	// 保证不产生半成品。取消状态通过 Result.Cancelled 返回。
	Ctx context.Context
	// OnScanProgress 扫描阶段进度回调,可空。scanned=已遍历到的媒体文件数(累计)。
	// 扫描是递归遍历,事前不知道总数,故只上报已扫描数量,用于展示"正在扫描…N 个"。
	OnScanProgress func(scanned int)
	// OnPhase 阶段回调,可空。phase 取值为 "scan" | "dedupe" | "process" | "done"。
	// 便于 GUI/CLI 切换不同阶段的进度展示(如扫描用不确定进度条,处理用确定进度条)。
	OnPhase func(phase string)
	// OnPreview 预览(仅 DryRun)阶段回调,可空。每次处理一个文件时调用一次,
	// 携带源文件绝对路径与目标相对路径(如 "2026/08/IMG_20260817.jpg"),
	// 供 GUI 构建可视化的预览目录树。非 DryRun 或 DryRun 之外的复制/移动阶段不调用。
	OnPreview func(srcPath, relDstPath string)
}

// Result 处理结果
type Result struct {
	Processed   int
	Duplicates  int
	Failed      int
	Skipped     int // 因同名冲突而按"跳过"策略跳过的文件数
	Cancelled   bool // true 表示运行因取消而提前终止(仍可能已处理部分文件)
	TimeSpanMin time.Time
	TimeSpanMax time.Time
	SourceCount map[string]int
	FailedFiles []string // 失败(含跳过)的源文件路径列表,供报告导出
}

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true,
	".gif": true, ".tiff": true, ".tif": true, ".heic": true, ".heif": true,
}

var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".3gp": true,
	".m4v": true, ".wmv": true, ".webm": true, ".flv": true,
}

// isMedia 判断扩展名是否属于支持的媒体格式。
// 若 exts 非 nil,则仅当扩展名在 exts 白名单内才算媒体(用于"格式选择")。
func isMedia(ext string, exts map[string]bool) bool {
	if !imageExts[ext] && !videoExts[ext] {
		return false
	}
	if exts != nil {
		return exts[ext]
	}
	return true
}

// Scan 递归扫描 src 下的媒体文件,排除 dst 自身(防递归)。
// exts 为 nil 表示处理全部支持的媒体格式;非 nil 时仅处理白名单内的扩展名。
func Scan(src, dst string, exts map[string]bool) []string {
	return ScanWithProgress(src, dst, exts, nil)
}

// ScanWithProgress 同 Scan,但会在遍历过程中通过 onScan 回调已扫描到的媒体文件数(累计)。
// onScan 可空。用于扫描阶段提供进度反馈(递归遍历事前未知总数,故只报已扫描数)。
func ScanWithProgress(src, dst string, exts map[string]bool, onScan func(scanned int)) []string {
	dstAbs, _ := filepath.Abs(dst)
	var files []string
	filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 无权限等,跳过
		}
		if d.IsDir() {
			abs, _ := filepath.Abs(p)
			if abs == dstAbs {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if isMedia(ext, exts) {
			files = append(files, p)
			if onScan != nil {
				onScan(len(files))
			}
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i] < files[j] })
	return files
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// Run 执行归档,返回统计。log 回调可空。
func Run(opt Options, log func(string)) Result {
	res := Result{SourceCount: map[string]int{}}
	emit := func(s string) {
		if log != nil {
			log(s)
		}
	}
	// 取消信号: nil 时使用 Background,保持向后兼容(等价旧版不中断行为)。
	ctx := opt.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if opt.Offset == 0 {
		opt.Offset = 0
	}
	phase := func(p string) {
		if opt.OnPhase != nil {
			opt.OnPhase(p)
		}
	}
	// 格式选择: opt.Extensions 为空表示全部;非空则仅处理白名单内的扩展名
	var exts map[string]bool
	if len(opt.Extensions) > 0 {
		exts = make(map[string]bool, len(opt.Extensions))
		for _, e := range opt.Extensions {
			exts[strings.ToLower(e)] = true
		}
	}
	// ---- 扫描:递归遍历并提供进度反馈 ----
	phase("scan")
	files := ScanWithProgress(opt.Src, opt.Dst, exts, func(scanned int) {
		if opt.OnScanProgress != nil {
			opt.OnScanProgress(scanned)
		}
	})
	if len(files) == 0 {
		emit("未找到任何照片或视频文件")
		phase("done")
		return res
	}
	emit(fmt.Sprintf("共发现 %d 个媒体文件", len(files)))

	// ---- 去重:按大小分组,同大小才算 MD5(性能关键) ----
	var kept []string
	if opt.Dedupe {
		phase("dedupe")
		bySize := map[int64][]string{}
		for _, f := range files {
			if ctx.Err() != nil {
				res.Cancelled = true
				emit("已取消")
				return res
			}
			if fi, err := os.Stat(f); err == nil {
				bySize[fi.Size()] = append(bySize[fi.Size()], f)
			}
		}
		seenHash := map[string]string{}
		for _, group := range bySize {
			for _, f := range group {
				h, err := fileMD5(f)
				if err != nil {
					continue
				}
				if keep, ok := seenHash[h]; ok {
					emit(fmt.Sprintf("[去重] 跳过重复: %s (已保留 %s)", f, keep))
					res.Duplicates++
					continue
				}
				seenHash[h] = f
				kept = append(kept, f)
			}
		}
		files = kept
		if res.Duplicates > 0 {
			emit(fmt.Sprintf("去重: 跳过 %d 个重复文件", res.Duplicates))
		}
	}

	// ---- 归档 ----
	phase("process")
	// 并发模型: 固定数量的 worker 并行处理文件。命名序号(counter/base)、Result 统计
	// 等共享状态通过互斥锁保护; GetCaptureTime 与 copyFile/MkdirAll 这类 IO/CPU 混合
	// 负载在锁外并行执行,充分利用多核。Concurrency<=0 取 runtime.NumCPU();
	// DryRun 无实际 IO,强制串行以保持日志/预览顺序。
	concurrency := opt.Concurrency
	if opt.DryRun {
		concurrency = 1
	} else if concurrency <= 0 {
		concurrency = runtime.NumCPU()
	}
	if concurrency < 1 {
		concurrency = 1
	}
	// 上限保护: 避免 --jobs= 传入过大值创建海量 goroutine。实际并发以文件数为准。
	const maxConcurrency = 64
	if concurrency > maxConcurrency {
		concurrency = maxConcurrency
	}

	var mu sync.Mutex
	var progressMu sync.Mutex // 独立进度锁: 慢 OnProgress 回调不阻塞 worker 临界区
	var done int
	var hasTimeSpan bool
	counter := map[string]int{}

	// progress 用独立进度锁调用,保证 OnProgress 回调不被并发触发(如 GUI 的 fyne.Do 需单线程),
	// 同时慢回调不会阻塞 worker 进入命名/统计临界区。
	progress := func() {
		if opt.OnProgress != nil {
			progressMu.Lock()
			opt.OnProgress(done, len(files))
			progressMu.Unlock()
		}
	}

	// 处理单个文件。共享状态(命名序号/统计/进度)在 mu 保护下访问;
	// GetCaptureTime、HeifExifStatus 与 copyFile/MkdirAll 这类 IO/CPU 混合负载在锁外并行。
	process := func(f string) {
		t, srcTag, ok := GetCaptureTime(f) // 只读 CPU 计算,可并行

		// HEIC/HEIF 的 EXIF 提取失败降级到文件名/mtime 时,输出明确降级提示便于排查。
		// HeifExifStatus 解析 HEIF box(IO/CPU 密集),在锁外并行执行。
		var degradeMsg string
		extLower := strings.ToLower(filepath.Ext(f))
		if (extLower == ".heic" || extLower == ".heif") && srcTag != "EXIF" {
			if kind, desc := HeifExifStatus(f); kind != "" {
				degradeMsg = fmt.Sprintf("[降级] %s,已降级为%s来源(%s): %s", desc, srcTag, kind, f)
			}
		}

		mu.Lock()
		if degradeMsg != "" {
			emit(degradeMsg)
		}
		// 严格时间模式: 只认 EXIF/视频元数据,不把文件名时间戳或文件修改时间
		// 当作拍摄时间(避免 mtime/文件名时间被误当拍摄时间造成误排)。
		if opt.StrictTime && ok && srcTag != "EXIF" && srcTag != "meta" {
			// 严格时间模式: 只认 EXIF/视频元数据。此类文件虽能兜底取到时间,
			// 但来源不是 EXIF/meta,无法通过严格校验,故无法归档。统计口径计为
			// 失败(与"无法读取任何时间"分支一致),文案明示"跳过计为失败"以免
			// 与真正的同名跳过(res.Skipped)语义混淆。
			emit("[失败] 严格时间模式下无法从 EXIF/元数据读取拍摄时间,已跳过: " + f)
			res.Failed++
			res.FailedFiles = append(res.FailedFiles, f)
			done++
			progress()
			mu.Unlock()
			return
		}
		// 按录制日期筛选: 只处理符合要求来源的文件
		if !matchesTimeFilter(srcTag, ok, opt.TimeFilter) {
			if opt.TimeFilter == "has" {
				emit("[筛选] 无录制日期(EXIF/元数据),跳过: " + f)
			} else if opt.TimeFilter == "none" {
				emit("[筛选] 有录制日期,跳过: " + f)
			}
			done++
			progress()
			mu.Unlock()
			return
		}
		if !ok {
			emit("[跳过] 无法读取任何时间: " + f)
			res.Failed++
			res.FailedFiles = append(res.FailedFiles, f)
			done++
			progress()
			mu.Unlock()
			return
		}
		t = t.Add(time.Duration(opt.Offset) * time.Second)
		res.SourceCount[srcTag]++
		if !hasTimeSpan || t.Before(res.TimeSpanMin) {
			res.TimeSpanMin = t
		}
		if !hasTimeSpan || t.After(res.TimeSpanMax) {
			res.TimeSpanMax = t
		}
		hasTimeSpan = true

		base := baseName(f, t, opt) // 不含扩展名
		ext := strings.ToLower(filepath.Ext(f))
		sub := relDir(t, opt)
		// 按冲突策略决定目标文件名(锁内完成命名与存在性判断,确保并发下一致)。
		conflict := opt.OnConflict
		if conflict == "" {
			conflict = "sequence" // 默认: 自动加序号,绝不覆盖
		}
		var newName string
		var target string
		if conflict == "skip" {
			// 跳过策略: 目标已存在或本批次已有同名文件被处理过,则跳过该文件。
			newName = base + ext
			target = filepath.Join(opt.Dst, sub, newName)
			// 冲突键需结合子目录维度,避免 KeepOriginal 下跨目录同名(拍摄日期不同,
			// 归档到不同 sub 子目录)被误判为"本批次已处理"而误跳过。
			conflictKey := sub + "/" + base
			if counter[conflictKey] > 0 {
				// 本批次已处理过同子目录同名文件(skip 语义:只保留第一个)
				emit("[跳过] 同名文件已被处理,跳过: " + f)
				res.Skipped++
				res.FailedFiles = append(res.FailedFiles, f)
				done++
				progress()
				mu.Unlock()
				return
			}
			if _, err := os.Stat(target); err == nil {
				emit("[跳过] 同名文件已存在,跳过: " + f)
				res.Skipped++
				res.FailedFiles = append(res.FailedFiles, f)
				done++
				progress()
				mu.Unlock()
				return
			}
			// 目标不存在: 正常处理(不加序号,因为 skip 场景默认预期无冲突)
			counter[conflictKey] = 1
			mu.Unlock()
		} else if conflict == "overwrite" {
			// 覆盖策略: 固定文件名,目标已存在则覆盖(危险)。
			newName = base + ext
			target = filepath.Join(opt.Dst, sub, newName)
			counter[base] = 1
			mu.Unlock()
		} else {
			// sequence 策略(默认): 递增序号,绝不覆盖。在锁内完成序号保留,确保并发下不重名。
			counter[base]++
			seq := counter[base]
			newName = fmt.Sprintf("%s_%03d%s", base, seq, ext)
			target = filepath.Join(opt.Dst, sub, newName)
			guard := 0
			for _, err := os.Stat(target); err == nil && guard < 999; _, err = os.Stat(target) {
				seq++
				newName = fmt.Sprintf("%s_%03d%s", base, seq, ext)
				target = filepath.Join(opt.Dst, sub, newName)
				guard++
			}
			// 关键: 将最终保留的序号回写 counter,保证后续 counter 分配从当前磁盘已用序号继续,
			// 避免并发下另一 worker 分配到与本次已保留 target 相同的 seq 而相互覆盖。
			counter[base] = seq
			mu.Unlock()
		}

		// ---- 锁外: 实际 IO,多 worker 并行 ----
		if !opt.DryRun {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				mu.Lock()
				emit("[失败] " + f + " : " + err.Error())
				res.Failed++
				res.FailedFiles = append(res.FailedFiles, f)
				done++
				progress()
				mu.Unlock()
				return
			}
			var err error
			// overwrite 策略下目标已存在时 os.Rename 在 Windows 会失败,
			// 故统一走复制(天然覆盖目标);move 时复制后校验大小一致再删除源文件。
			if opt.Move && conflict != "overwrite" {
				// 优先同盘重命名;跨磁盘时 os.Rename 会失败,降级为复制+删除源文件
				err = os.Rename(f, target)
				if err != nil {
					if cerr := copyFile(f, target, ctx); cerr == nil {
						// 删除源文件是破坏性操作,先校验目标与源大小一致,防止复制不完整导致照片永久丢失
						si, _ := os.Stat(f)
						ti, _ := os.Stat(target)
						if si != nil && ti != nil && si.Size() == ti.Size() {
							err = os.Remove(f)
						} else {
							os.Remove(target) // 复制不完整,清理半成品,保留源文件
							err = errors.New("复制不完整,已保留源文件")
						}
					} else {
						err = cerr
					}
				}
			} else {
				err = copyFile(f, target, ctx)
				// move + overwrite: 复制已覆盖目标,再删除源文件(校验大小防止半成品)
				if err == nil && opt.Move && conflict == "overwrite" {
					si, _ := os.Stat(f)
					ti, _ := os.Stat(target)
					if si != nil && ti != nil && si.Size() == ti.Size() {
						err = os.Remove(f)
					} else {
						// 覆盖场景目标原为已存在文件,已写入新内容;仅当校验不一致时
						// 保留已写入的目标文件(不删除),避免覆盖后目标侧旧数据也丢失,
						// 只保留源文件并报错,交由用户人工核对。
						err = errors.New("复制校验不一致,目标已保留(未删除源文件)")
					}
				}
			}
			if err != nil {
				// 取消导致的错误不算失败: 取消状态已在主循环中标记,半成品已删除。
				// 用 ctx.Err()!=nil 判断,兼容 WithCancel(返回 Canceled) 与
				// WithDeadline/WithTimeout(返回 DeadlineExceeded) 两种取消来源。
				if ctx.Err() != nil {
					return
				}
				mu.Lock()
				emit("[失败] " + f + " : " + err.Error())
				res.Failed++
				res.FailedFiles = append(res.FailedFiles, f)
				done++
				progress()
				mu.Unlock()
				return
			}
		}

		mu.Lock()
		prefix := ""
		if opt.DryRun {
			prefix = "[预览] "
		}
		emit(fmt.Sprintf("%s[%s] %s -> %s", prefix, t.Format("2006-01-02 15:04:05"),
			filepath.Base(f), filepath.Join(sub, newName)))
		if opt.DryRun && opt.OnPreview != nil {
			opt.OnPreview(f, filepath.Join(sub, newName))
		}
		res.Processed++
		done++
		progress()
		mu.Unlock()
	}

	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				if ctx.Err() != nil {
					// 取消: 不再处理新文件;已取到的当前文件由 process 内部安全完成。
					return
				}
				process(f)
			}
		}()
	}
	// 分发: 用 select 使主 goroutine 能在取消时提前停止发送,避免死锁。
	for _, f := range files {
		select {
		case <-ctx.Done():
			res.Cancelled = true
			emit("已取消")
			goto drain
		case jobs <- f:
		}
	}
drain:
	close(jobs)
	wg.Wait()
	if done < len(files) { // 兜底: 确保进度归满
		done = len(files)
	}
	progress()
	phase("done")
	return res
}

// copyFile 流式复制,大文件不占内存。复制过程中检查 ctx 取消:若取消,
// 返回取消错误。任何出错路径(写失败/读失败/取消)都通过 defer 统一
// 关闭句柄并删除半成品,保证不残留不完整的脏数据。
func copyFile(src, dst string, ctx context.Context) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			// 出错或取消: 关闭句柄并删除半成品,避免残留脏文件
			out.Close()
			os.Remove(dst)
		}
	}()
	// 64KB 分块复制,便于在复制过程中检查取消信号。
	// 传入 nil context 时视为不可取消(供测试/无取消需求场景调用)。
	if ctx == nil {
		ctx = context.Background()
	}
	buf := make([]byte, 64*1024)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func relDir(t time.Time, opt Options) string {
	switch {
	case opt.Year:
		return t.Format("2006")
	case opt.Day:
		return filepath.Join(t.Format("2006"), t.Format("01"), t.Format("02"))
	default:
		return filepath.Join(t.Format("2006"), t.Format("01"))
	}
}

// baseName 生成目标文件的基础名(不含扩展名与冲突序号)。
// 默认用规范时间名 YYYY-MM-DD_HHMMSS;KeepOriginal 时用原始文件名;
// 前缀/后缀会追加在基础名两侧(后缀在扩展名之前)。
func baseName(f string, t time.Time, opt Options) string {
	coreName := t.Format("2006-01-02_150405")
	if opt.KeepOriginal {
		coreName = strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
	}
	return opt.NamePrefix + coreName + opt.NameSuffix
}

// matchesTimeFilter 判断一个文件是否通过录制日期筛选。
// EXIF/meta 来源视为"有录制日期";name/mtime 来源视为"无录制日期"(仅靠兜底)。
func matchesTimeFilter(srcTag string, ok bool, filter string) bool {
	switch filter {
	case "has": // 仅处理有录制日期
		return ok && (srcTag == "EXIF" || srcTag == "meta")
	case "none": // 仅处理无录制日期
		return ok && srcTag != "EXIF" && srcTag != "meta"
	default: // "" = 全部
		return true
	}
}
