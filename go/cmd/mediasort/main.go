// MediaSorterGo - 按拍摄时间整理照片/视频(CLI + Windows 拖放)
// 用法:
//
//	把照片文件夹拖到 exe 图标上,松手即可(结果生成在 exe 旁『照片整理』)
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
		exe, _ := os.Executable()
		opt.Dst = filepath.Join(filepath.Dir(exe), "照片整理")
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
	fmt.Printf("完成: 处理 %d 个, 去重跳过 %d 个, 失败 %d 个\n",
		res.Processed, res.Duplicates, res.Failed)
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
	fmt.Println("  结果生成在本程序旁边的『照片整理』文件夹,按 年/月 排好")
	fmt.Println("用法二(命令行): mediasort <源文件夹> [目标文件夹] [选项]")
	fmt.Println("  选项: --move | --no-dedupe | --dry-run | --offset=秒 | --year | --day")
}

func waitExit() {
	if runtime.GOOS == "windows" {
		fmt.Print("\n按回车键退出...")
		fmt.Scanln()
	}
}
