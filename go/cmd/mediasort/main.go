// MediaSorterGo - 按拍摄时间整理照片/视频(CLI + Windows 拖放)
// 用法:
//
//	把照片文件夹拖到 exe 图标上,松手即可(结果生成在源文件夹同级『MediaSorter』)
//	或命令行: mediasort <源文件夹> [目标文件夹]
//
// 选项(可选,用 -- 开头):
//
//	--move        移动而非复制(默认只复制)
//	--no-dedupe   关闭去重
//	--dry-run     只预览,不复制/移动
//	--offset N    时间偏移 N 秒(修正机内时间)
//	--year        仅按年分目录
//	--day         按 年/月/日 分目录
//	--layout=模板 自定义目录结构(优先级高于 --year/--day)。示例:
//	              YYYY / 2006
//	              YYYY/MM/DD / 2006/01/02
//	              YYYY/YYYY-MM / 2006/2006-01
//	              YYYY-MM-DD / 2006-01-02
//	              按原目录名 / {dir}
//	--name=模板  自定义文件名格式(优先级高于 --keep-original)。占位符:
//	              {ts}=YYYY-MM-DD_HHMMSS {date}=YYYY-MM-DD
//	              {orig}=原始文件名 {seq}=序号(_001,含它则始终带序号)
//	              例如: --name='{date}_{orig}'
//	--keep-original 保留原始文件名(等价于 --name='{orig}')
//	--jobs N      并发 worker 数(默认 CPU 核数;=1 串行)
//	--strict-time 严格时间模式(只认 EXIF/元数据,不把文件名/文件时间当拍摄时间)
//	--conflict=策略 同名文件处理: sequence(默认加序号) | skip | overwrite
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/h1s97x/mediasorter/internal/core"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		waitExit()
		return
	}

	opt := core.Options{Dedupe: true}
	var positional []string
	for _, a := range args {
		switch {
		case a == "--move":
			opt.Move = true
		case a == "--no-dedupe":
			opt.Dedupe = false
		case a == "--dry-run":
			opt.DryRun = true
		case a == "--year":
			opt.Year = true
		case a == "--day":
			opt.Day = true
		case strings.HasPrefix(a, "--offset="):
			if v, err := strconv.Atoi(strings.TrimPrefix(a, "--offset=")); err == nil {
				opt.Offset = v
			}
		case strings.HasPrefix(a, "--layout="):
			opt.DirLayout = strings.TrimPrefix(a, "--layout=")
		case strings.HasPrefix(a, "--name="):
			opt.NameLayout = strings.TrimPrefix(a, "--name=")
		case a == "--keep-original":
			opt.KeepOriginal = true
		case strings.HasPrefix(a, "--jobs="):
			if v, err := strconv.Atoi(strings.TrimPrefix(a, "--jobs=")); err == nil && v > 0 {
				opt.Concurrency = v
			} else {
				fmt.Println("警告: --jobs 需为正整数,忽略该值")
			}
		case a == "--strict-time":
			opt.StrictTime = true
		case strings.HasPrefix(a, "--conflict="):
			switch strings.TrimPrefix(a, "--conflict=") {
			case "skip":
				opt.OnConflict = "skip"
			case "overwrite":
				opt.OnConflict = "overwrite"
			default:
				opt.OnConflict = "sequence"
			}
		case strings.HasPrefix(a, "--"):
			fmt.Println("未知选项:", a)
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		printHelp()
		waitExit()
		return
	}
	opt.Src = positional[0]
	if len(positional) >= 2 {
		opt.Dst = positional[1]
	} else {
		// 留空: 默认输出到源文件夹的父目录下『MediaSorter』,与源文件夹同级
		opt.Dst = filepath.Join(filepath.Dir(opt.Src), "MediaSorter")
	}

	fmt.Printf("输入文件夹: %s\n", opt.Src)
	fmt.Printf("输出文件夹: %s\n", opt.Dst)
	if opt.DryRun {
		fmt.Println("模式: 预览(--dry-run,不会复制/移动任何文件)")
	} else if opt.Move {
		fmt.Println("模式: 移动(源文件将被移动)")
	} else {
		fmt.Println("模式: 复制(源文件保持不变)")
	}
	fmt.Println()

	res := core.Run(opt, func(s string) { fmt.Println(s) })

	fmt.Println()
	if !res.TimeSpanMin.IsZero() {
		fmt.Printf("时间跨度: %s ~ %s\n",
			res.TimeSpanMin.Format("2006-01-02 15:04"), res.TimeSpanMax.Format("2006-01-02 15:04"))
	}
	fmt.Printf("完成: 处理 %d 个, 去重跳过 %d 个, 失败 %d 个, 同名跳过 %d 个\n",
		res.Processed, res.Duplicates, res.Failed, res.Skipped)
	if res.SourceCount["mtime"] > 0 {
		fmt.Printf("提示: %d 个文件时间来自修改时间(视频元数据/文件名均无时间),仅供参考\n",
			res.SourceCount["mtime"])
	}
	waitExit()
}

func printHelp() {
	fmt.Println("MediaSorterGo - 按拍摄时间整理照片/视频")
	fmt.Println("=" + strings.Repeat("=", 52))
	fmt.Println("用法一(最简单): 把照片文件夹拖到本程序图标上,松手")
	fmt.Println("  结果生成在源文件夹的上一级目录『MediaSorter』,按 年/月 排好")
	fmt.Println("用法二(命令行): mediasort <源文件夹> [目标文件夹] [选项]")
	fmt.Println("  选项: --move | --no-dedupe | --dry-run | --offset=秒 | --year | --day | --layout=模板 | --name=文件名模板 | --keep-original | --jobs=并发数 | --strict-time | --conflict=策略")
}

func waitExit() {
	if runtime.GOOS == "windows" {
		fmt.Print("\n按回车键退出...")
		fmt.Scanln()
	}
}
