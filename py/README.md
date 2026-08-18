# MediaSorterPy(已归档)

Python 原型版,按拍摄时间整理照片/视频。**已被 Go 版(media-sorter-go)取代**,保留作为参考/教学。

## 定位

- 第一个可用的原型(2026-08-17),验证了核心思路: 四级时间兜底 + 大小分组去重 + 字典序命名
- Go 版是其进化形态: 体积 33MB → 3.5MB、零依赖、内置 MP4 解析(视频时间更准)、跨平台
- 本目录不再主动演进,如需修改请同步到 Go 版

## 文件

| 文件 | 说明 |
|---|---|
| `sort_media_by_time.py` | 命令行版(需 Python + Pillow + pillow-heif;视频时间推荐 ffprobe) |
| `exe_app.py` | 拖放版打包入口(33MB exe 的源) |
| `dist/HonorPhotoSort.exe` | 已打包的拖放版 exe(领导 U 盘版) |

## 命令版用法(参考)

```bash
python sort_media_by_time.py 输入目录 输出目录 [--dry-run] [--move] [--dedupe]
```

## 独有但已被 Go 版吸收/超越的能力

- 四级时间兜底(EXIF → 文件名 → mtime)✓ Go 版有,且视频升级为 MP4 元数据解析
- `--dry-run` 预览 ✓ Go 版已补(2026-08-18)
- 时区归一、防递归、冲突序号、内容去重 ✓ 全部在 Go 版
