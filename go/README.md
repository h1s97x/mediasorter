# MediaSorterGo

按拍摄时间整理照片/视频的跨平台工具(纯 Go,零第三方运行时依赖)。
MediaSorter 功能的 Go 重写,核心逻辑 100% 跨平台(Windows / macOS / Linux)。

## 为什么是 Go

| | MediaSorterCN(C#) | MediaSorterGo(Go) |
|---|---|---|
| 体积 | 150-400KB | CLI 3.5MB / GUI ~15MB |
| 依赖 | 系统 .NET Framework 4.0 | 无(静态链接) |
| 平台 | Windows only | Windows/macOS/Linux |
| 视频时间 | 文件时间兜底 | **解析 MP4 元数据 creation_time**(UTC→本地) |
| 照片 EXIF | System.Drawing | **手写 JPEG APP1 / HEIC ISO-BMFF / TIFF 解析器**,零第三方库 |

## 架构

```
cmd/mediasort/           CLI 入口(支持 Windows 拖放)
cmd/mediasort-gui/       fyne 图形界面(需 gcc 编译,见下)
  main_fyne.go           UI: 预览/执行/进度/统计/拖放
  settings.go            设置持久化(JSON,存用户配置目录)
internal/core/           跨平台核心(纯 Go,无 cgo)
  core.go                扫描/去重/命名/归档/冲突保护/格式与录制日期筛选
  timex.go               时间四级兜底: EXIF -> MP4元数据 -> 文件名 -> 文件时间
  exif.go                JPEG EXIF DateTimeOriginal 解析(手写)
  heif.go                HEIC/HEIF EXIF 解析(手写 ISO BMFF meta/iloc/iinf)
  mp4time.go             MP4 moov/mvhd creation_time(UTC->本地)
  filetime_windows.go    创建时间(Windows Filetime)
  filetime_darwin.go     创建时间(macOS Birthtimespec)
  filetime_other.go      Linux 等: 无创建时间语义,回退修改时间
```

## 构建

```bash
# CLI 版(纯标准库,任意平台,无外部依赖)
go build -o mediasort ./cmd/mediasort

# GUI 版(fyne,需要 cgo/gcc 环境;Windows 装 MSYS2/MinGW 或 TDM-GCC)
# 提示: Releases 已提供各平台成品 exe,普通用户无需自行编译。
go get fyne.io/fyne/v2@latest
go build -tags fyne -o mediasort-gui ./cmd/mediasort-gui

# Windows 下静态链接 MinGW 运行时,产出的 exe 免装 DLL(与 CI 一致)
#   go build -tags fyne -ldflags "-H windowsgui -linkmode external -extldflags -static" -o mediasort-gui.exe ./cmd/mediasort-gui

# 交叉编译示例(在任意机器上出其他平台产物)
GOOS=linux  GOARCH=amd64 go build -o mediasort-linux  ./cmd/mediasort
GOOS=darwin GOARCH=arm64 go build -o mediasort-mac    ./cmd/mediasort

# 运行测试
go test ./internal/core/
```

## 用法

```
GUI 版(mediasort-gui):
    把照片文件夹直接拖到窗口上(或点"选择"),可配置:
      - 目录结构: 年/月 | 仅年 | 年/月/日
      - 时间偏移(秒)
      - 处理格式: 全部 / 指定格式(JPG/HEIC/MP4 等)
      - 录制日期筛选: 全部 / 仅录制日期 / 仅无录制日期
      - 去重、移动/复制
      - 命名: 规范时间名 / 保留原始文件名,可加前缀后缀
    先点"预览"查看效果(不复制/移动),确认后点"开始整理"。
    实时进度条+统计,结果默认在源文件夹上一级『MediaSorter』。设置自动保存。

命令行版(mediasort):
    用法一: 把照片文件夹拖到 exe 图标上,松手
            结果生成在源文件夹上一级『MediaSorter』,按 年/月 排好
    用法二: mediasort <源文件夹> [目标文件夹] [选项]
    选项: --move | --no-dedupe | --dry-run | --offset=秒 | --year | --day
```

## 时间提取优先级(四级兜底)

1. 照片 EXIF `DateTimeOriginal`(JPEG APP1 与 HEIC/HEIF ISO-BMFF 均手写解析)
2. 视频 MP4 `moov/mvhd` `creation_time`(UTC 自动转本地,东八区不差 8 小时)
3. 文件名时间戳(`IMG_20260817_104121` / `Screenshot_2026-08-10-153212` / 微信 unix 秒·毫秒戳)
4. 文件创建/修改时间(取较旧者,输出时标记来源)

## 设计要点

- 去重先按文件大小分组,同大小才 MD5(1G 视频不会全量哈希)
- 流式复制,大文件不占内存
- 输出目录自动排除,防递归;同名冲突自动 `_002/_003` 不覆盖
- 默认只复制不删源;`--dry-run`/GUI 预览可零风险查看结果
- 格式选择: 空=全部,非空=仅白名单内的扩展名
- 录制日期筛选: `has` 仅处理 EXIF/元数据来源,`none` 仅处理兜底来源
- 设置 JSON 存于用户配置目录(Windows `%AppData%` / macOS `~/Library/Application Support` / Linux `~/.config`),原子写入防损坏
