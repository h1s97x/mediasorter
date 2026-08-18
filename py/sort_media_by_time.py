#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
按拍摄时间统一归档 荣耀/华为手机导出的照片视频 (v2)
====================================================
为什么"直接导出会乱":
- 相册 App 是聚合视图,文件系统里照片散在 DCIM/Camera、截屏、微信、QQ 等目录
- 文件名前缀混杂:IMG_/REC_/VID_/mmexport/Screenshot_,无法按名排序

本脚本如何"保证"按拍摄时间排好(核心设计,缺一不可):
1. 时间必须从【文件内部元数据】读,不能信文件系统时间
   - 照片:EXIF DateTimeOriginal(荣耀相机写入的本地拍摄时间)
   - 视频:MP4 元数据 creation_time(ffprobe 读取,UTC 需转本地,否则差 8 小时)
   - 兜底1:从文件名解析时间(IMG_20260817_104121 / mmexport+Unix时间戳)
   - 兜底2:文件修改时间(最不可靠,MTP/微信传输会改它,仅保证"有个时间"并打标记)
2. 命名格式 YYYY-MM-DD_HHMMSS_序号,保证【字典序 = 时间序】
   - 年月日时分秒全部补零,年/月目录名也补零(07 不是 7),否则排序会错位
3. 安全:默认只复制不删源、--dry-run 先预览、同名冲突自动递增序号绝不覆盖
4. 可选 --dedupe:按内容哈希去重(微信/相机可能重复保存同一张照片)

用法:
    python sort_media_by_time.py 输入目录 输出目录 [--dry-run] [--move] [--dedupe]

依赖:
    pip install Pillow pillow-heif      # HEIC 照片需要;不装则 HEIC 走兜底
    ffmpeg(ffprobe) 建议安装             # 视频时间更准;没有则走文件名/mtime 兜底
"""

import argparse
import hashlib
import re
import shutil
import subprocess
import sys
from collections import defaultdict
from datetime import datetime
from pathlib import Path

IMAGE_EXTS = {".jpg", ".jpeg", ".png", ".webp", ".heic", ".heif", ".bmp", ".gif"}
VIDEO_EXTS = {".mp4", ".mov", ".avi", ".mkv", ".3gp", ".m4v", ".wmv"}
ALL_EXTS = IMAGE_EXTS | VIDEO_EXTS

try:
    from pillow_heif import register_heif_opener
    register_heif_opener()
    HAVE_HEIF = True
except ImportError:
    HAVE_HEIF = False

try:
    from PIL import Image
    HAVE_PIL = True
except ImportError:
    HAVE_PIL = False

EXIF_TIME_TAGS = (36867, 36868, 306)  # DateTimeOriginal / DateTimeDigitized / DateTime

# ---- 文件名时间戳解析(兜底1)----
PHOTO_NAME_RE = re.compile(r"(\d{8})[_-](\d{6})")          # IMG_20260817_104121 / REC_20260817_104121
DASH_NAME_RE = re.compile(r"(\d{4})-(\d{2})-(\d{2})[_-](\d{6})")  # Screenshot_2026-08-17-104121
UNIX_MS_RE = re.compile(r"(\d{13})")                        # 毫秒时间戳(部分 App)
UNIX_S_RE = re.compile(r"(\d{10})")                         # 秒时间戳(mmexport1722534678)


def parse_name_time(path: Path):
    """从文件名提取时间,失败返回 None。顺序:日期时间 → 年月日 → Unix 秒/毫秒"""
    name = path.name
    m = PHOTO_NAME_RE.search(name)
    if m:
        try:
            return datetime.strptime(m.group(1) + m.group(2), "%Y%m%d%H%M%S")
        except ValueError:
            pass
    m = DASH_NAME_RE.search(name)
    if m:
        try:
            return datetime(int(m.group(1)), int(m.group(2)), int(m.group(3)),
                            int(m.group(4)[:2]), int(m.group(4)[2:4]), int(m.group(4)[4:6]))
        except ValueError:
            pass
    m = UNIX_MS_RE.search(name)
    if m and 1_000_000_000_000 <= int(m.group(1)) <= 20_000_000_000_000:
        return datetime.fromtimestamp(int(m.group(1)) / 1000)
    m = UNIX_S_RE.search(name)
    if m and 1_000_000_000 <= int(m.group(1)) <= 2_000_000_000:
        return datetime.fromtimestamp(int(m.group(1)))
    return None


def get_photo_time(path: Path):
    """照片时间:EXIF 优先;HEIC 无 pillow-heif 或 EXIF 缺失时返回 None"""
    if not HAVE_PIL:
        return None
    try:
        with Image.open(path) as img:
            exif = img.getexif()
            for tag in EXIF_TIME_TAGS:
                raw = exif.get(tag)
                if not raw:
                    continue
                raw = str(raw).strip()
                for fmt in ("%Y:%m:%d %H:%M:%S", "%Y-%m-%d %H:%M:%S"):
                    try:
                        return datetime.strptime(raw, fmt)
                    except ValueError:
                        continue
    except Exception:
        return None
    return None


def get_video_time(path: Path):
    """视频时间:ffprobe 读 creation_time(UTC → 本地),失败返回 None"""
    try:
        out = subprocess.run(
            ["ffprobe", "-v", "error", "-select_streams", "v:0",
             "-show_entries", "format_tags=creation_time",
             "-of", "default=noprint_wrappers=1:nokey=1", str(path)],
            capture_output=True, text=True, timeout=30)
        raw = out.stdout.strip()
        if not raw:
            return None
        raw = raw.replace("Z", "+00:00")
        try:
            # UTC -> 本地时间;不转的话东八区所有视频会差 8 小时,排序全乱
            return datetime.fromisoformat(raw).astimezone().replace(tzinfo=None)
        except ValueError:
            for fmt in ("%Y-%m-%dT%H:%M:%S", "%Y-%m-%d %H:%M:%S"):
                try:
                    return datetime.strptime(raw[:19], fmt)
                except ValueError:
                    continue
    except Exception:
        return None
    return None


def get_media_time(path: Path):
    """三级兜底:元数据 → 文件名 → mtime。返回 (时间, 来源标记)"""
    ext = path.suffix.lower()
    t = None
    if ext in IMAGE_EXTS:
        t = get_photo_time(path)
    elif ext in VIDEO_EXTS:
        t = get_video_time(path)
    if t is not None:
        return t, "EXIF" if ext in IMAGE_EXTS else "meta"
    t = parse_name_time(path)
    if t is not None:
        return t, "name"
    return datetime.fromtimestamp(path.stat().st_mtime), "mtime"


def file_md5(path: Path, chunk=1 << 20):
    h = hashlib.md5()
    with open(path, "rb") as f:
        while True:
            b = f.read(chunk)
            if not b:
                break
            h.update(b)
    return h.hexdigest()


def main():
    ap = argparse.ArgumentParser(description="按拍摄时间归档手机导出的照片/视频")
    ap.add_argument("src", help="输入目录(手机导出内容的根目录)")
    ap.add_argument("dst", help="输出目录(自动按 年/月 归档)")
    ap.add_argument("--dry-run", action="store_true", help="只预览,不复制")
    ap.add_argument("--move", action="store_true", help="移动而非复制(默认复制,更安全)")
    ap.add_argument("--dedupe", action="store_true", help="按内容哈希去重(微信/相机重复保存时用)")
    args = ap.parse_args()

    src, dst = Path(args.src), Path(args.dst)
    if not src.is_dir():
        sys.exit(f"错误:输入目录不存在 -> {src}")

    files = sorted(p for p in src.rglob("*")
                   if p.is_file() and p.suffix.lower() in ALL_EXTS)
    if not files:
        sys.exit("未找到任何照片/视频文件")

    # ---- 去重(可选):按 (大小, MD5) 判重,保留第一个 ----
    skipped_dup = []
    if args.dedupe:
        seen = {}
        kept = []
        for f in files:
            key = (f.stat().st_size, file_md5(f))
            if key in seen:
                skipped_dup.append((str(f), seen[key]))
                continue
            seen[key] = str(f)
            kept.append(f)
        files = kept
        print(f"去重:跳过 {len(skipped_dup)} 个重复文件\n" if skipped_dup else "去重:未发现重复文件\n")

    # ---- 时间归档 ----
    counter = defaultdict(int)
    stats = defaultdict(int)
    times = []
    print(f"共 {len(files)} 个文件 | 来源:EXIF=照片元数据 meta=视频元数据 name=文件名 mtime=修改时间")
    print("-" * 104)

    for f in files:
        t, src_tag = get_media_time(f)
        stats[src_tag] += 1
        times.append(t)
        key = t.strftime("%Y-%m-%d_%H%M%S")
        counter[key] += 1
        seq = counter[key]
        new_name = f"{key}_{seq:03d}{f.suffix.lower()}"
        target = dst / t.strftime("%Y") / t.strftime("%m") / new_name

        # 同秒序号撞车(理论上不会)或目标已存在:递增序号,绝不覆盖源文件
        guard = 0
        while target.exists() or guard > 999:
            seq += 1
            new_name = f"{key}_{seq:03d}{f.suffix.lower()}"
            target = dst / t.strftime("%Y") / t.strftime("%m") / new_name
            guard += 1

        mark = "" if src_tag in ("EXIF", "meta") else f"  <-- 时间来自{src_tag},请留意"
        print(f"[{src_tag:<5}] {t.strftime('%Y-%m-%d %H:%M:%S')}  {target.relative_to(dst)}{mark}")

        if not args.dry_run:
            try:
                target.parent.mkdir(parents=True, exist_ok=True)
                if args.move:
                    shutil.move(str(f), str(target))
                else:
                    shutil.copy2(str(f), str(target))
            except Exception as e:
                stats["fail"] += 1
                print(f"  !! 处理失败: {e}", file=sys.stderr)

    # ---- 自检摘要 ----
    print("-" * 104)
    if times:
        print(f"时间跨度: {min(times):%Y-%m-%d %H:%M}  ~  {max(times):%Y-%m-%d %H:%M}")
        by_year = defaultdict(int)
        for t in times:
            by_year[t.year] += 1
        print("按年分布: " + "  ".join(f"{y}年{n}个" for y, n in sorted(by_year.items())))
    print(f"来源统计: EXIF {stats['EXIF']} | 视频元数据 {stats['meta']} | 文件名 {stats['name']} | 修改时间 {stats['mtime']} | 失败 {stats['fail']}")
    if stats["mtime"]:
        print("注意:有文件时间来自修改时间(多为无 ffprobe 的视频),建议装 ffmpeg 后重跑一次更准")
    if not HAVE_HEIF:
        print("提示:未安装 pillow-heif,HEIC 照片无法读 EXIF,走文件名/mtime 兜底")
    if args.dry_run:
        print("本次为预览(--dry-run),未做任何复制/移动")
    if skipped_dup:
        print(f"\n去重明细(保留 → 跳过):")
        for dup, keep in skipped_dup[:20]:
            print(f"  保留 {keep}")
            print(f"  跳过 {dup}")
        if len(skipped_dup) > 20:
            print(f"  ...等 {len(skipped_dup) - 20} 条略")


if __name__ == "__main__":
    main()
