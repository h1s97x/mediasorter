//go:build fyne

// MediaSorterGo - fyne 图形界面版(跨平台)
// 编译(需要 gcc 环境;Windows 装 MSYS2/MinGW 或 TDM-GCC):
//
//	go get fyne.io/fyne/v2@latest
//	go build -tags fyne -o mediasort-gui.exe ./cmd/mediasort-gui
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

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
	w.Resize(fyne.NewSize(820, 720))

	srcEntry := widget.NewEntry()
	srcEntry.SetPlaceHolder("手机导出的照片文件夹(可整个 U 盘,也可直接拖放到窗口)")
	dstEntry := widget.NewEntry()
	dstEntry.SetPlaceHolder("目标文件夹(留空 = 源文件夹上一级『MediaSorter』)")

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

	// ============ 『高级选项』折叠区内容(默认收起) ============
	// 危险/低频/进阶设置全部收进高级区,主界面只留高频安全项,降低误触风险。

	// 危险: 移动文件会删除源文件,一律默认不勾选。
	// 勾选时弹一次确认框,让用户明确知悉"移动=删除源文件"的不可逆后果。
	moveChk := widget.NewCheck("移动文件(整理后删除源文件,默认只复制更安全)", nil)
	moveChk.OnChanged = func(on bool) {
		if !on {
			return
		}
		// 勾选时确认: 移动=复制+删除源文件,不可恢复。
		confirm := dialog.NewConfirm(
			"确认启用移动文件",
			"开启后将删除源文件夹中的原始文件,且不可恢复。确定启用吗?\n\n(可先『预览』确认结果无误后再开启移动)",
			func(ok bool) {
				if !ok {
					// 取消则回退勾选状态,保持"默认只复制"的安全默认。
					moveChk.SetChecked(false)
				}
			},
			w,
		)
		confirm.SetConfirmText("启用移动")
		confirm.SetDismissText("取消")
		confirm.Show()
	}
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

	// 命名选项: 保留原始文件名 + 自定义前缀/后缀
	keepNameChk := widget.NewCheck("保留原始文件名(默认按拍摄时间命名)", nil)
	prefixEntry := widget.NewEntry()
	prefixEntry.SetPlaceHolder("如: 旅行_ → 旅行_2026-01-30_143105.jpg")
	prefixEntry.SetText("")
	suffixEntry := widget.NewEntry()
	suffixEntry.SetPlaceHolder("如: _整理 → 2026-01-30_143105_整理.jpg")
	suffixEntry.SetText("")

	// 同名文件冲突策略(默认: 自动加序号,最安全)
	conflict := "sequence"
	conflictSel := widget.NewSelect([]string{"自动加序号", "跳过", "覆盖"},
		func(s string) {
			switch s {
			case "跳过":
				conflict = "skip"
			case "覆盖":
				conflict = "overwrite"
			default:
				conflict = "sequence"
			}
		})
	conflictSel.SetSelected("自动加序号")

	// 严格时间模式: 只认 EXIF/元数据,避免 mtime/文件名时间被误当拍摄时间
	strictChk := widget.NewCheck("严格时间(只认 EXIF/元数据,不把文件名/文件时间当拍摄时间)", nil)

	// ===== 格式选择: 参考图分组 UI —— 全部格式 / 全部图片 / 全部视频 / 其他格式 =====
	// 图片格式: 显示名 -> 实际扩展名(可含多个,如 JPG 覆盖 .jpg/.jpeg)
	photoFormats := []struct {
		label string
		exts  []string
		sup   string // 支持的元数据说明(参考图 JPG/HEIC 标 *)
	}{
		{"JPG", []string{".jpg", ".jpeg"}, "*"},
		{"HEIC", []string{".heic", ".heif"}, "*"},
		{"TIFF", []string{".tiff", ".tif"}, ""},
		{"PNG", []string{".png"}, ""},
		{"GIF", []string{".gif"}, ""},
		{"BMP", []string{".bmp"}, ""},
		{"WebP", []string{".webp"}, ""},
	}
	// 视频格式
	videoFormats := []struct {
		label string
		exts  []string
		sup   string
	}{
		{"MP4", []string{".mp4", ".m4v"}, "*"},
		{"MOV", []string{".mov"}, "*"},
		{"AVI", []string{".avi"}, ""},
		{"MKV", []string{".mkv"}, ""},
		{"WMV", []string{".wmv"}, ""},
		{"3GP", []string{".3gp"}, ""},
		{"WebM", []string{".webm"}, ""},
		{"FLV", []string{".flv"}, ""},
	}
	// 把所有格式的显示名 -> 复选框、扩展名集合映射出来,便于统一收集
	formatChecks := make(map[string]*widget.Check)       // 扩展名 -> 复选框(用于恢复与收集)
	formatCheckByLabel := make(map[string]*widget.Check) // 显示名 -> 复选框
	// 全部开关(对应参考图「All file formats」)
	formatAllChk := widget.NewCheck("全部格式", nil)
	// 图片组: 全部图片 + 格式行
	formatPhotoAllChk := widget.NewCheck("全部图片", nil)
	photoRow := container.NewHBox()
	for _, f := range photoFormats {
		c := widget.NewCheck(f.label+f.sup, nil)
		c.SetChecked(true)
		formatCheckByLabel[f.label] = c
		for _, e := range f.exts {
			formatChecks[e] = c
		}
		photoRow.Add(c)
	}
	// 视频组: 全部视频 + 格式行
	formatVideoAllChk := widget.NewCheck("全部视频", nil)
	videoRow := container.NewHBox()
	for _, f := range videoFormats {
		c := widget.NewCheck(f.label+f.sup, nil)
		c.SetChecked(true)
		formatCheckByLabel[f.label] = c
		for _, e := range f.exts {
			formatChecks[e] = c
		}
		videoRow.Add(c)
	}
	// 其他格式: 用户自定义,逗号分隔(对应参考图「Remaining file formats」)
	customFormatEntry := widget.NewEntry()
	customFormatEntry.SetPlaceHolder("其他格式,用逗号分隔,如 RAW,DNG,NEF(留空=不额外处理)")

	// 全部图片开关: 勾选则全选并禁用该组格式复选框,取消则取消该组勾选并启用
	formatPhotoAllChk.OnChanged = func(all bool) {
		for _, f := range photoFormats {
			c := formatCheckByLabel[f.label]
			if c == nil {
				continue
			}
			c.SetChecked(all)
			if all {
				c.Disable()
			} else {
				c.Enable()
			}
		}
	}
	// 全部视频开关: 同理
	formatVideoAllChk.OnChanged = func(all bool) {
		for _, f := range videoFormats {
			c := formatCheckByLabel[f.label]
			if c == nil {
				continue
			}
			c.SetChecked(all)
			if all {
				c.Disable()
			} else {
				c.Enable()
			}
		}
	}
	// 全部格式开关: 勾选则全选并禁用图片/视频组所有复选框,取消则启用各组
	formatAllChk.OnChanged = func(all bool) {
		for _, c := range formatCheckByLabel {
			c.SetChecked(all)
			if all {
				c.Disable()
			} else {
				c.Enable()
			}
		}
		if all {
			formatPhotoAllChk.SetChecked(true)
			formatPhotoAllChk.Disable()
			formatVideoAllChk.SetChecked(true)
			formatVideoAllChk.Disable()
		} else {
			formatPhotoAllChk.Enable()
			formatVideoAllChk.Enable()
		}
	}
	formatPhotoAllChk.SetChecked(true)
	formatVideoAllChk.SetChecked(true)
	formatAllChk.SetChecked(true)

	// 『高级选项』: 折叠区,默认收起。危险/低频/进阶设置全部收纳于此,
	// 主界面只保留高频安全项。展开时用 HBox 容器统一 Show,收起则 Hide。
	advancedOpen := false
	// 先声明后赋值: 闭包回调在按钮被点击时才执行,彼时 advancedBtn 已指向按钮实例;
	// 若在同一条 := 语句的初始化闭包内引用自身,会因作用域未生效报 "undefined: advancedBtn"。
	// 同理,advancedContent 需先声明后赋值,否则闭包内引用会报 "undefined: advancedContent"。
	var advancedBtn *widget.Button
	var advancedContent *fyne.Container
	advancedBtn = widget.NewButton("高级选项 ▸", func() {
		advancedOpen = !advancedOpen
		if advancedOpen {
			advancedBtn.SetText("高级选项 ▾")
			advancedContent.Show()
		} else {
			advancedBtn.SetText("高级选项 ▸")
			advancedContent.Hide()
		}
	})

	// 高级区内容: 统一放进一个 VBox,展开/收起整体控制显示。
	// 危险项(移动文件)用醒目标注提醒,避免误触。
	moveLabel := widget.NewLabel("⚠ 危险操作")
	moveLabel.Importance = widget.DangerImportance
	advancedContent = container.NewVBox(
		container.NewVBox(
			moveLabel,
			moveChk,
		),
		dedupeChk,
		container.NewHBox(widget.NewLabel("时间偏移(秒):"), offsetEntry),
		container.NewHBox(widget.NewLabel("处理日期:"), timeFilterSel),
		keepNameChk,
		// 命名选项说明
		widget.NewLabel("文件名前后缀(加在文件名两侧,后缀位于扩展名之前):"),
		container.NewBorder(nil, nil, widget.NewLabel("文件名前缀:"), nil, prefixEntry),
		container.NewBorder(nil, nil, widget.NewLabel("文件名后缀:"), nil, suffixEntry),
		container.NewHBox(widget.NewLabel("同名文件:"), conflictSel),
		strictChk,
	)
	advancedContent.Hide()

	logEntry := widget.NewMultiLineEntry()
	logEntry.Disable()
	logEntry.SetPlaceHolder("运行日志会显示在这里")
	// 日志区: 设置合理的最小尺寸 + 自动换行。
	// 尺寸过小导致内容拥挤;不换行时超长路径会横向溢出,需左右滑动查看。
	logEntry.SetMinSize(fyne.NewSize(760, 280))
	logEntry.Wrapping = fyne.TextWrapWord

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
	// cancelFunc 保存当前运行的取消函数,供取消按钮调用(每次 run 开始时重新赋值)。
	var cancelFunc context.CancelFunc

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
		// 移动文件属危险操作,一律默认不勾选(默认只复制),即使上次保存过也不自动启用
		dedupeChk.SetChecked(s.Dedupe)
		offsetEntry.SetText(strconv.Itoa(s.Offset))
		// 恢复格式选择: 保存了具体扩展名则取消"全部"并按保存值勾选
		if len(s.Extensions) > 0 {
			saved := make(map[string]bool, len(s.Extensions))
			for _, e := range s.Extensions {
				saved[e] = true
			}
			formatAllChk.SetChecked(false) // 触发 OnChanged,启用各组复选框
			// 预设格式按保存值勾选
			var customs []string
			for ext, c := range formatChecks {
				if saved[ext] {
					c.SetChecked(true)
					c.Enable()
				} else {
					c.SetChecked(false)
					c.Enable()
				}
			}
			// 不在预设里的自定义扩展名写入自定义输入框
			for _, e := range s.Extensions {
				if _, known := formatChecks[e]; !known {
					customs = append(customs, strings.ToUpper(strings.TrimPrefix(e, ".")))
				}
			}
			if len(customs) > 0 {
				customFormatEntry.SetText(strings.Join(customs, ","))
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
		// 恢复同名冲突策略
		switch s.OnConflict {
		case "skip":
			conflict = "skip"
			conflictSel.SetSelected("跳过")
		case "overwrite":
			conflict = "overwrite"
			conflictSel.SetSelected("覆盖")
		default:
			conflict = "sequence"
			conflictSel.SetSelected("自动加序号")
		}
		// 恢复严格时间模式
		strictChk.SetChecked(s.StrictTime)
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
			// 留空: 默认输出到源文件夹的父目录下『MediaSorter』,与源文件夹同级
			dst = filepath.Join(filepath.Dir(src), "MediaSorter")
		}
		offset := 0
		if v, err := strconv.Atoi(offsetEntry.Text); err == nil {
			offset = v
		}
		// 源和目标不能互相包含,防递归
		if rel, err := filepath.Rel(src, dst); err == nil && rel != "." && !isRelOutside(rel) {
			return core.Options{}, "目标文件夹不能在源文件夹内部,否则会递归处理已整理的结果!"
		}
		// 计算要处理的扩展名
		// formatAllChk 勾选 = 全部格式(空切片);否则收集勾选的预设格式 + 自定义格式
		exts := []string(nil)
		if !formatAllChk.Checked {
			exts = collectExtensions(formatChecks)
			// 解析自定义格式(逗号分隔,去掉多余空格与点)
			for _, part := range strings.Split(customFormatEntry.Text, ",") {
				p := strings.TrimSpace(part)
				if p == "" {
					continue
				}
				p = strings.TrimPrefix(strings.ToLower(p), ".")
				e := "." + p
				if _, known := formatChecks[e]; known {
					continue // 已在预设格式中,跳过
				}
				exts = append(exts, e)
			}
			// 若取消全部后一个格式都没选(含自定义为空),提示用户至少勾选一种
			if len(exts) == 0 {
				return core.Options{}, "请至少勾选一种处理格式,或勾选『全部格式』"
			}
		}
		// 命名选项: 保留原始文件名 + 前缀/后缀
		keepOriginal := keepNameChk.Checked
		prefix := prefixEntry.Text
		suffix := suffixEntry.Text
		// 同名文件冲突策略与严格时间模式
		onConflict := conflict
		if onConflict == "" {
			onConflict = "sequence"
		}
		strict := strictChk.Checked
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
			OnConflict:   onConflict,
			StrictTime:   strict,
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
			OnConflict:   onConflict,
			StrictTime:   strict,
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

	// 失败清单导出: 运行完成后若存在失败/跳过的文件,启用该按钮导出到 txt 报告。
	// (定义在 run 之前,供 run 结束后更新其状态)
	exportBtn := widget.NewButton("导出失败清单", nil)
	exportBtn.Disable()
	var lastFailedFiles []string // 保存最近一次运行的失败清单
	exportBtn.OnTapped = func() {
		if len(lastFailedFiles) == 0 {
			dialog.ShowInformation("提示", "本次没有失败文件可导出。", w)
			return
		}
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri == nil {
				return
			}
			p := uri.Path()
			out := filepath.Join(p, fmt.Sprintf("mediasorter-failed-%s.txt", time.Now().Format("20060102-150405")))
			var sb strings.Builder
			sb.WriteString("# MediaSorter 失败/跳过文件清单\n")
			sb.WriteString(fmt.Sprintf("# 生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
			sb.WriteString(fmt.Sprintf("# 共 %d 个文件\n\n", len(lastFailedFiles)))
			for _, fpath := range lastFailedFiles {
				sb.WriteString(fpath + "\n")
			}
			if err := os.WriteFile(out, []byte(sb.String()), 0o600); err != nil {
				dialog.ShowError(err, w)
				return
			}
			dialog.ShowInformation("导出成功", "失败清单已导出到:\n"+out, w)
		}, w)
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

		// 创建可取消 context,注入 core.Run 以支持安全中断。
		ctx, cancel := context.WithCancel(context.Background())
		cancelFunc = cancel
		opt.Ctx = ctx
		cancelBtn.SetText("取消")

		ch := make(chan string, 256)
		doneCh := make(chan core.Result, 1)

		go func() {
			// defer 兜底: 即使 core.Run 内部发生 panic,也保证 doneCh 通知与 close(ch)
			// 一定执行,避免消费者 goroutine 永久阻塞导致『预览/开始整理』按钮一直禁用
			// (即用户遇到的:执行一遍后按钮变灰、必须重启)。
			var res core.Result
			defer func() {
				if r := recover(); r != nil {
					res.Cancelled = false
					res.Processed = -1 // 标记异常,供最终状态展示
					fyne.Do(func() {
						logEntry.SetText(logEntry.Text + fmt.Sprintf("\n[错误] 处理过程中发生异常,已中止: %v\n", r))
					})
				}
				select {
				case doneCh <- res:
				default:
				}
				close(ch) // 关闭日志通道,让消费者循环退出,从而恢复按钮并输出最终状态
			}()
			res = core.Run(opt, func(s string) { ch <- s })
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
				total := atomic.LoadInt64(&totalFiles)
				if total <= 0 {
					total = int64(res.Processed)
				}
				if res.Processed < 0 {
					// 处理过程发生 panic: 展示异常信息,按钮照常恢复
					status.SetText("异常中止: 处理过程中发生未捕获错误,详情见日志。按钮已恢复,可重新操作。")
					setButtonsRunning(false)
					cancelFunc = nil
					cancelBtn.SetText("取消")
					logEntry.Disable()
					return
				}
				if res.Cancelled {
					// 取消时不强制进度条置满,保留中断时的实际进度
					if total > 0 {
						progress.SetValue(float64(res.Processed) / float64(total))
					}
					status.SetText(fmt.Sprintf("已取消: 本次已处理 %d / %d 个, 去重 %d, 失败 %d, 跳过 %d",
						res.Processed, total, res.Duplicates, res.Failed, res.Skipped))
				} else if opt.DryRun {
					status.SetText(fmt.Sprintf("预览完成: 将处理 %d / %d 个, 去重 %d, 失败 %d, 跳过 %d",
						res.Processed, total, res.Duplicates, res.Failed, res.Skipped))
					status.SetText(status.Text + "\n以上是预览结果,未做任何复制/移动。确认无误后点『开始整理』正式执行。")
				} else {
					progress.SetValue(1)
					status.SetText(fmt.Sprintf("完成: 处理 %d / %d 个, 去重 %d, 失败 %d, 跳过 %d",
						res.Processed, total, res.Duplicates, res.Failed, res.Skipped))
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
				// 更新失败清单导出: 有失败/跳过的文件时启用导出按钮
				lastFailedFiles = res.FailedFiles
				if len(lastFailedFiles) > 0 {
					exportBtn.Enable()
				} else {
					exportBtn.Disable()
				}
				setButtonsRunning(false)
				cancelFunc = nil
				cancelBtn.SetText("取消")
				logEntry.Disable() // 运行结束,禁止编辑日志区
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

	// 开始整理: 正式执行。若已勾选"移动文件",在正式整理前做第二次确认
	// (勾选时已确认过一次,此处为执行前最后一道闸),避免误点直接删源文件。
	startBtn.OnTapped = func() {
		opt, errMsg := buildOptions()
		if errMsg != "" {
			dialog.ShowInformation("提示", errMsg, w)
			return
		}
		opt.DryRun = false
		if opt.Move {
			confirm := dialog.NewConfirm(
				"二次确认",
				"你已启用『移动文件』。\n正式整理将把源文件夹中的文件移动到目标目录,并删除源文件,不可恢复。\n\n确定开始吗?",
				func(ok bool) {
					if ok {
						run(opt, "正式整理(移动文件)")
					}
				},
				w,
			)
			confirm.SetConfirmText("确定移动")
			confirm.SetDismissText("取消")
			confirm.Show()
			return
		}
		run(opt, "正式整理")
	}

	cancelBtn.OnTapped = func() {
		if cancelFunc == nil {
			return
		}
		// 触发取消信号,core.Run 会在安全点停止后续文件处理。
		cancelFunc()
		cancelBtn.SetText("正在取消…")
		cancelBtn.Disable()
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

	// 日志区: 独立固定高度,避免被挤压成一行
	logBox := container.NewVBox(widget.NewLabel("日志:"), logEntry)
	// 用 Scroll 包裹日志区内容,确保高度可控
	content := container.NewVScroll(
		container.NewVBox(
			container.NewBorder(nil, nil, widget.NewLabel("源文件夹:"), srcBtn, srcEntry),
			container.NewBorder(nil, nil, widget.NewLabel("目标文件夹:"), dstBtn, dstEntry),
			container.NewHBox(widget.NewLabel("目录结构:"), modeGroup),
			// 处理格式分组(参考图): 全部格式 / 全部图片+图片行 / 全部视频+视频行 / 其他格式
			container.NewHBox(widget.NewLabel("处理格式:"), formatAllChk),
			container.NewHBox(formatPhotoAllChk, photoRow),
			container.NewHBox(formatVideoAllChk, videoRow),
			container.NewBorder(nil, nil, widget.NewLabel("其他格式:"), nil, customFormatEntry),
			container.NewHBox(advancedBtn), // 『高级选项』折叠开关
			advancedContent,                // 高级区内容(默认隐藏,展开时才显示)
			container.NewHBox(previewBtn, startBtn, cancelBtn, exportBtn),
			progress,
			scanProgress,
			status,
			logBox,
		),
	)
	w.SetContent(content)
	w.ShowAndRun()
}

// isRelOutside 判断相对路径是否走出目标目录(用于防递归检查)
func isRelOutside(rel string) bool {
	return rel == ".." || len(rel) >= 3 && rel[:3] == "../"
}

// collectExtensions 收集当前勾选的扩展名(不含自定义格式)。
func collectExtensions(checks map[string]*widget.Check) []string {
	var exts []string
	for ext, c := range checks {
		if c.Checked {
			exts = append(exts, ext)
		}
	}
	return exts
}
