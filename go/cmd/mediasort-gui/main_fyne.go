//go:build fyne

// MediaSorterGo - fyne 图形界面版(跨平台)
// 编译(需要 gcc 环境;Windows 装 MSYS2/MinGW 或 TDM-GCC):
//
//	go get fyne.io/fyne/v2@latest
//	go build -tags fyne -o mediasort-gui.exe ./cmd/mediasort-gui
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/h1s97x/mediasorter/internal/core"
)

func main() {
	a := app.New()
	w := a.NewWindow("MediaSorterGo - 按拍摄时间整理照片视频")
	w.Resize(fyne.NewSize(780, 640))

	srcEntry := widget.NewEntry()
	srcEntry.SetPlaceHolder("手机导出的照片文件夹(可整个 U 盘,也可直接拖放到窗口)")
	dstEntry := widget.NewEntry()
	dstEntry.SetPlaceHolder("目标文件夹(留空 = 程序旁『照片整理』)")

	mode := "ym"
	modeGroup := widget.NewRadioGroup([]string{"年/月", "仅年", "年/月/日"}, func(s string) {
		if s != "" {
			switch s {
			case "仅年":
				mode = "y"
			case "年/月/日":
				mode = "ymd"
			default:
				mode = "ym"
			}
		}
	})
	modeGroup.SetSelected("年/月")

	moveChk := widget.NewCheck("移动文件(默认只复制,源文件不动)", nil)
	dedupeChk := widget.NewCheck("去重(相同内容只留一份)", nil)
	dedupeChk.SetChecked(true)
	offsetEntry := widget.NewEntry()
	offsetEntry.SetText("0")

	// 录制日期筛选: 全部 / 仅录制日期 / 仅无录制日期
	timeFilter := "" // "" = 全部
	timeFilterSel := widget.NewSelect([]string{"全部文件", "仅录制日期(EXIF/元数据)", "仅无录制日期(靠文件名/文件时间)"},
		func(s string) {
			switch s {
			case "仅录制日期(EXIF/元数据)":
				timeFilter = "has"
			case "仅无录制日期(靠文件名/文件时间)":
				timeFilter = "none"
			default:
				timeFilter = ""
			}
		})
	timeFilterSel.SetSelected("全部文件")

	// 格式选择: 勾选的扩展名才处理;全选 = 全部格式
	formatExts := []string{".jpg", ".heic", ".heif", ".png", ".webp", ".gif", ".mp4", ".mov"}
	formatChecks := make(map[string]*widget.Check, len(formatExts))
	formatRow1 := container.NewHBox()
	formatRow2 := container.NewHBox()
	formatAllChk := widget.NewCheck("全部", nil)
	for i, ext := range formatExts {
		name := strings.ToUpper(strings.TrimPrefix(ext, "."))
		c := widget.NewCheck(name, nil)
		c.SetChecked(true)
		formatChecks[ext] = c
		if i < 5 {
			formatRow1.Add(c)
		} else {
			formatRow2.Add(c)
		}
	}
	// 全部开关: 勾选则全选并禁用各格式复选框,取消则启用各复选框
	formatAllChk.OnChanged = func(all bool) {
		if all {
			for _, c := range formatChecks {
				c.SetChecked(true)
				c.Disable()
			}
		} else {
			for _, c := range formatChecks {
				c.Enable()
			}
		}
	}
	formatAllChk.SetChecked(true)

	// 命名选项: 保留原始文件名 + 自定义前缀/后缀
	keepNameChk := widget.NewCheck("保留原始文件名(默认按拍摄时间命名)", nil)
	prefixEntry := widget.NewEntry()
	prefixEntry.SetPlaceHolder("文件名前缀(可选)")
	prefixEntry.SetText("")
	suffixEntry := widget.NewEntry()
	suffixEntry.SetPlaceHolder("文件名后缀(可选)")
	suffixEntry.SetText("")

	logEntry := widget.NewMultiLineEntry()
	logEntry.Disable()
	logEntry.SetPlaceHolder("运行日志会显示在这里")

	// 进度条 + 状态
	// progress: 处理阶段的确定进度条; scanProgress: 扫描阶段的无限进度条(动画)
	progress := widget.NewProgressBar()
	progress.SetValue(0)
	scanProgress := widget.NewProgressBarInfinite()
	scanProgress.Hide() // 默认隐藏,扫描阶段才显示
	status := widget.NewLabel("就绪。可先点『预览』查看效果,确认后再『开始整理』。")

	previewBtn := widget.NewButton("预览", nil)
	startBtn := widget.NewButton("开始整理", nil)
	cancelBtn := widget.NewButton("取消", nil)
	cancelBtn.Disable()

	// 记录去重后总文件数,供进度条百分比使用
	var totalFiles int64

	srcBtn := widget.NewButton("选择", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				srcEntry.SetText(uri.Path())
			}
		}, w)
	})
	dstBtn := widget.NewButton("选择", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				dstEntry.SetText(uri.Path())
			}
		}, w)
	})

	// 启动时恢复上次保存的设置
	if s := loadSettings(); s.Src != "" || s.Dst != "" {
		srcEntry.SetText(s.Src)
		dstEntry.SetText(s.Dst)
		switch s.Mode {
		case "y":
			mode = "y"
			modeGroup.SetSelected("仅年")
		case "ymd":
			mode = "ymd"
			modeGroup.SetSelected("年/月/日")
		default:
			mode = "ym"
			modeGroup.SetSelected("年/月")
		}
		moveChk.SetChecked(s.Move)
		dedupeChk.SetChecked(s.Dedupe)
		offsetEntry.SetText(strconv.Itoa(s.Offset))
		// 恢复格式选择: 保存了具体扩展名则取消"全部"并按保存值勾选
		if len(s.Extensions) > 0 {
			saved := make(map[string]bool, len(s.Extensions))
			for _, e := range s.Extensions {
				saved[e] = true
			}
			formatAllChk.SetChecked(false) // 触发 OnChanged,启用各格式复选框
			for ext, c := range formatChecks {
				c.SetChecked(saved[ext])
			}
		}
		// 恢复命名选项
		keepNameChk.SetChecked(s.KeepOriginal)
		prefixEntry.SetText(s.NamePrefix)
		suffixEntry.SetText(s.NameSuffix)
		// 恢复录制日期筛选
		switch s.TimeFilter {
		case "has":
			timeFilter = "has"
			timeFilterSel.SetSelected("仅录制日期(EXIF/元数据)")
		case "none":
			timeFilter = "none"
			timeFilterSel.SetSelected("仅无录制日期(靠文件名/文件时间)")
		default:
			timeFilter = ""
			timeFilterSel.SetSelected("全部文件")
		}
	}

	// setButtonsRunning 统一管理运行期间按钮状态
	setButtonsRunning := func(running bool) {
		if running {
			previewBtn.Disable()
			startBtn.Disable()
			cancelBtn.Enable()
		} else {
			previewBtn.Enable()
			startBtn.Enable()
			cancelBtn.Disable()
		}
	}

	// buildOptions 读取表单并校验,返回 core.Options 或错误消息(非空则弹窗)。
	buildOptions := func() (core.Options, string) {
		src := srcEntry.Text
		if src == "" {
			return core.Options{}, "请先选择源文件夹(也可直接把文件夹拖到窗口上)"
		}
		if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
			return core.Options{}, "源文件夹不存在或不是文件夹: " + src
		}
		dst := dstEntry.Text
		if dst == "" {
			exe, _ := os.Executable()
			dst = filepath.Join(filepath.Dir(exe), "照片整理")
		}
		offset := 0
		if v, err := strconv.Atoi(offsetEntry.Text); err == nil {
			offset = v
		}
		// 源和目标不能互相包含,防递归
		if rel, err := filepath.Rel(src, dst); err == nil && rel != "." && !isRelOutside(rel) {
			return core.Options{}, "目标文件夹不能在源文件夹内部,否则会递归处理已整理的结果!"
		}
		// 计算要处理的扩展名(全选 = 空切片 = 全部格式)
		exts := collectExtensions(formatChecks)
		// 命名选项: 保留原始文件名 + 前缀/后缀
		keepOriginal := keepNameChk.Checked
		prefix := prefixEntry.Text
		suffix := suffixEntry.Text
		// 保存设置,供下次启动恢复(静默失败,不影响整理)
		_ = saveSettings(settings{
			Src:          srcEntry.Text,
			Dst:          dstEntry.Text,
			Mode:         mode,
			Move:         moveChk.Checked,
			Dedupe:       dedupeChk.Checked,
			Offset:       offset,
			Extensions:   exts,
			KeepOriginal: keepOriginal,
			NamePrefix:   prefix,
			NameSuffix:   suffix,
			TimeFilter:   timeFilter,
		})
		return core.Options{
			Src: src, Dst: dst,
			Move:         moveChk.Checked,
			Dedupe:       dedupeChk.Checked,
			Offset:       offset,
			Extensions:   exts,
			KeepOriginal: keepOriginal,
			NamePrefix:   prefix,
			NameSuffix:   suffix,
			TimeFilter:   timeFilter,
			Year:         mode == "y",
			Day:          mode == "ymd",
			OnProgress: func(done, total int) {
				atomic.StoreInt64(&totalFiles, int64(total))
				if total <= 0 {
					return
				}
				done = min(done, total)
				p := float64(done) / float64(total)
				fyne.Do(func() {
					progress.SetValue(p)
				})
			},
			OnScanProgress: func(scanned int) {
				// 扫描阶段: 只报已扫描到的媒体文件数,进度条为无限动画
				fyne.Do(func() {
					status.SetText(fmt.Sprintf("正在扫描…已发现 %d 个媒体文件(大目录可能较慢,请稍候)", scanned))
				})
			},
			OnPhase: func(phase string) {
				fyne.Do(func() {
					switch phase {
					case "scan":
						// 扫描阶段: 显示无限进度条动画,隐藏确定进度条
						scanProgress.Show()
						scanProgress.Start()
						progress.Hide()
						status.SetText("正在扫描…(递归遍历,大目录可能需要一些时间)")
					case "dedupe":
						status.SetText("正在去重…(计算 MD5,可能较慢)")
					case "process":
						// 进入处理阶段: 恢复为确定进度条,隐藏无限进度条
						scanProgress.Stop()
						scanProgress.Hide()
						progress.Show()
						progress.SetValue(0)
						status.SetText("正在整理…")
					case "done":
						scanProgress.Stop()
						scanProgress.Hide()
						progress.Show()
					}
				})
			},
		}, ""
	}

	// run 执行整理(预览或正式),统一驱动进度条/日志/状态。
	run := func(opt core.Options, modeLabel string) {
		setButtonsRunning(true)
		atomic.StoreInt64(&totalFiles, 0)
		// 重置进度条状态(默认显示确定进度条,隐藏无限进度条)
		scanProgress.Stop()
		scanProgress.Hide()
		progress.Show()
		progress.SetValue(0)
		status.SetText("正在扫描…")
		logEntry.SetText("")
		logEntry.Enable()
		logEntry.SetText(fmt.Sprintf("模式: %s\n输入: %s\n输出: %s\n\n", modeLabel, opt.Src, opt.Dst))

		ch := make(chan string, 256)
		doneCh := make(chan core.Result, 1)

		go func() {
			res := core.Run(opt, func(s string) { ch <- s })
			doneCh <- res
		}()

		go func() {
			for s := range ch {
				msg := s
				fyne.Do(func() {
					logEntry.SetText(logEntry.Text + msg + "\n")
				})
			}
			res := <-doneCh
			fyne.Do(func() {
				progress.SetValue(1)
				total := atomic.LoadInt64(&totalFiles)
				if total <= 0 {
					total = int64(res.Processed)
				}
				if opt.DryRun {
					status.SetText(fmt.Sprintf("预览完成: 将处理 %d / %d 个, 去重 %d, 失败 %d",
						res.Processed, total, res.Duplicates, res.Failed))
					status.SetText(status.Text + "\n以上是预览结果,未做任何复制/移动。确认无误后点『开始整理』正式执行。")
				} else {
					status.SetText(fmt.Sprintf("完成: 处理 %d / %d 个, 去重 %d, 失败 %d",
						res.Processed, total, res.Duplicates, res.Failed))
				}
				if !res.TimeSpanMin.IsZero() {
					status.SetText(status.Text + fmt.Sprintf("\n时间跨度: %s ~ %s",
						res.TimeSpanMin.Format("2006-01-02 15:04"),
						res.TimeSpanMax.Format("2006-01-02 15:04")))
				}
				if res.SourceCount["mtime"] > 0 {
					status.SetText(status.Text + fmt.Sprintf("\n提示: %d 个文件时间来自修改时间,仅供参考",
						res.SourceCount["mtime"]))
				}
				setButtonsRunning(false)
			})
		}()
	}

	// 预览: 只查看结果,不复制/移动
	previewBtn.OnTapped = func() {
		opt, errMsg := buildOptions()
		if errMsg != "" {
			dialog.ShowInformation("提示", errMsg, w)
			return
		}
		opt.DryRun = true
		run(opt, "预览(--dry-run,不会复制/移动任何文件)")
	}

	// 开始整理: 正式执行
	startBtn.OnTapped = func() {
		opt, errMsg := buildOptions()
		if errMsg != "" {
			dialog.ShowInformation("提示", errMsg, w)
			return
		}
		opt.DryRun = false
		run(opt, "正式整理")
	}

	cancelBtn.OnTapped = func() {
		dialog.ShowInformation("提示", "Go 版当前不支持安全中断,请等待本次运行结束。\n(如需中断可关闭窗口)", w)
	}

	// 拖放支持:把文件夹拖到窗口上,自动填入源文件夹
	w.SetOnDropped(func(pos fyne.Position, uris []fyne.URI) {
		for _, u := range uris {
			p := u.Path()
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			if fi.IsDir() {
				if srcEntry.Text == "" {
					srcEntry.SetText(p)
					status.SetText("已填入源文件夹: " + p)
				} else {
					dstEntry.SetText(p)
					status.SetText("已填入目标文件夹: " + p)
				}
				return
			}
		}
	})

	content := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("源文件夹:"), srcBtn, srcEntry),
		container.NewBorder(nil, nil, widget.NewLabel("目标文件夹:"), dstBtn, dstEntry),
		container.NewHBox(widget.NewLabel("目录结构:"), modeGroup,
			widget.NewLabel("  时间偏移(秒):"), offsetEntry),
		container.NewHBox(widget.NewLabel("处理日期:"), timeFilterSel),
		container.NewHBox(moveChk, dedupeChk),
		container.NewHBox(widget.NewLabel("处理格式:"), formatAllChk, formatRow1),
		formatRow2,
		container.NewHBox(keepNameChk),
		container.NewHBox(widget.NewLabel("文件名前缀:"), prefixEntry,
			widget.NewLabel("  文件名后缀:"), suffixEntry),
		container.NewHBox(previewBtn, startBtn, cancelBtn),
		progress,
		scanProgress,
		status,
		container.NewVBox(widget.NewLabel("日志:"), logEntry),
	)
	w.SetContent(content)
	w.ShowAndRun()
}

// isRelOutside 判断相对路径是否走出目标目录(用于防递归检查)
func isRelOutside(rel string) bool {
	return rel == ".." || len(rel) >= 3 && rel[:3] == "../"
}

// collectExtensions 收集当前勾选的扩展名。若所有格式复选框都勾选,返回空切片(表示全部)。
func collectExtensions(checks map[string]*widget.Check) []string {
	var all bool = true
	for _, c := range checks {
		if !c.Checked {
			all = false
			break
		}
	}
	if all {
		return nil // 空 = 全部格式
	}
	var exts []string
	for ext, c := range checks {
		if c.Checked {
			exts = append(exts, ext)
		}
	}
	return exts
}
