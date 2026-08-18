# MediaSorterCN(已归档)

> **本实现已不再维护。** Go 版(`go/`)是唯一持续演进的主力实现,功能上已覆盖并超越本版本
> (HEIC 支持、MP4 视频时间解析、跨平台、预览、格式选择、录制日期筛选等)。
> 本目录仅作**参考**: 展示了用 .NET Framework 4.0 / C# WinForms 追求 ~150KB 极致小体积的实验思路。

按拍摄时间整理照片/视频的 Windows 原生小工具(.NET Framework 4.0 / C# WinForms)。
复现 MediaSorter 核心功能,中文界面,目标: 150KB 级别的极致小体积。

## 功能

- 递归扫描源文件夹(支持整个 U 盘/导出目录)
- 时间提取三级兜底:
  1. 照片 EXIF 拍摄时间(DateTimeOriginal)
  2. 文件名时间戳(`IMG_20260817_104121` / `Screenshot_2026-08-10-153212` / 微信 unix 戳)
  3. 创建/修改时间取较旧者
- 统一命名 `YYYY-MM-DD_HHMMSS_001.jpg`,字典序 = 时间序
- 目录结构可选: 年/月 | 仅年 | 年/月/日
- 内容去重(按大小分组 + MD5,大文件不吃性能)
- 复制(默认)/移动;时间偏移修正;冲突自动加序号不覆盖;防递归

## 构建(免安装 Visual Studio)

用 Windows 自带的 .NET Framework 4.0 编译器(csc.exe)编译两个 `.cs` 文件即可,
产物 `MediaSorterCN.exe` 约 150-400KB,Win7 ~ Win11 直接运行。

> 注: 仓库当前未附带 `build.bat` 构建脚本(README 早期版本的引用已移除),可直接用
> 下面命令在 VS 开发者命令行或带 .NET 的 PowerShell 中编译:
>
> ```bat
> csc.exe /nologo /target:winexe /out:MediaSorterCN.exe Program.cs MediaScanner.cs
> ```

## 用法

1. 运行 MediaSorterCN.exe
2. 选源文件夹(手机导出的照片目录/整个 U 盘)和目标文件夹
3. 选目录结构,勾选去重,必要时填时间偏移(秒)
4. 点"开始整理",日志实时显示,完成自动统计

## 说明

- 默认只复制,源文件不动;勾选"移动文件"才移动
- 视频时间优先读文件创建/修改时间(较旧者);如需更精确可用 Go 版(解析 MP4 元数据)
- HEIC 照片无 EXIF 支持时走文件名/mtime 兜底
