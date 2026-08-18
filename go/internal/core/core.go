// Package core - 按拍摄时间归档照片/视频的跨平台核心逻辑(纯 Go,无 cgo)
package core

import (
	"crypto/md5"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	// OnProgress 进度回调,可空。done=已处理, total=本次总文件数。
	// 注:total 为去重前的扫描总数,去重后实际处理数可能更少。
	OnProgress func(done, total int)
}

// Result 处理结果
type Result struct {
	Processed   int
	Duplicates  int
	Failed      int
	TimeSpanMin time.Time
	TimeSpanMax time.Time
	SourceCount map[string]int
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
	if opt.Offset == 0 {
		opt.Offset = 0
	}
	// 格式选择: opt.Extensions 为空表示全部;非空则仅处理白名单内的扩展名
	var exts map[string]bool
	if len(opt.Extensions) > 0 {
		exts = make(map[string]bool, len(opt.Extensions))
		for _, e := range opt.Extensions {
			exts[strings.ToLower(e)] = true
		}
	}
	files := Scan(opt.Src, opt.Dst, exts)
	if len(files) == 0 {
		emit("未找到任何照片或视频文件")
		return res
	}
	emit(fmt.Sprintf("共发现 %d 个媒体文件", len(files)))

	// ---- 去重:按大小分组,同大小才算 MD5(性能关键) ----
	var kept []string
	if opt.Dedupe {
		bySize := map[int64][]string{}
		for _, f := range files {
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
	progress := func(done int) {
		if opt.OnProgress != nil {
			opt.OnProgress(done, len(files))
		}
	}
	counter := map[string]int{}
	for i, f := range files {
		progress(i)
		t, srcTag, ok := GetCaptureTime(f)
		// 按录制日期筛选: 只处理符合要求来源的文件
		if !matchesTimeFilter(srcTag, ok, opt.TimeFilter) {
			if opt.TimeFilter == "has" {
				emit("[筛选] 无录制日期(EXIF/元数据),跳过: " + f)
			} else if opt.TimeFilter == "none" {
				emit("[筛选] 有录制日期,跳过: " + f)
			}
			continue
		}
		if !ok {
			emit("[跳过] 无法读取任何时间: " + f)
			res.Failed++
			continue
		}
		t = t.Add(time.Duration(opt.Offset) * time.Second)
		res.SourceCount[srcTag]++
		if res.Processed == 0 || t.Before(res.TimeSpanMin) {
			res.TimeSpanMin = t
		}
		if res.Processed == 0 || t.After(res.TimeSpanMax) {
			res.TimeSpanMax = t
		}

		base := baseName(f, t, opt) // 不含扩展名
		ext := strings.ToLower(filepath.Ext(f))
		counter[base]++
		seq := counter[base]
		newName := fmt.Sprintf("%s_%03d%s", base, seq, ext)
		sub := relDir(t, opt)
		target := filepath.Join(opt.Dst, sub, newName)

		// 同名冲突:递增序号,绝不覆盖
		guard := 0
		for _, err := os.Stat(target); err == nil && guard < 999; _, err = os.Stat(target) {
			seq++
			newName = fmt.Sprintf("%s_%03d%s", base, seq, ext)
			target = filepath.Join(opt.Dst, sub, newName)
			guard++
		}

		if !opt.DryRun {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				emit("[失败] " + f + " : " + err.Error())
				res.Failed++
				continue
			}
			var err error
			if opt.Move {
				err = os.Rename(f, target)
			} else {
				err = copyFile(f, target)
			}
			if err != nil {
				emit("[失败] " + f + " : " + err.Error())
				res.Failed++
				continue
			}
		}
		prefix := ""
		if opt.DryRun {
			prefix = "[预览] "
		}
		emit(fmt.Sprintf("%s[%s] %s -> %s", prefix, t.Format("2006-01-02 15:04:05"),
			filepath.Base(f), filepath.Join(sub, newName)))
		res.Processed++
	}
	progress(len(files))
	return res
}

// copyFile 流式复制,大文件不占内存
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
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
