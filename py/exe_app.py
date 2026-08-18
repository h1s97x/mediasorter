#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
荣耀照片按时间整理(拖放版 exe 入口)
====================================
给领导用的免安装版本:把手机导出的照片文件夹拖到 exe 上 → 松手 →
自动按拍摄时间归档到 exe 旁边的「照片整理」文件夹 → 按回车退出。

安全:只复制不删源;输出目录自身会被排除,不会递归拷贝自己。
"""

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

EXIF_TIME_TAGS = (36867, 36868, 306)
PHOTO_NAME_RE = re.compile(r"(\d{8})[_-](\d{6})")
DASH_NAME_RE = re.compile(r"(\d{4})-(\d{2})-(\d{2})[_-](\d{6})")
UNIX_MS_RE = re.compile(r"(\d{13})")
UNIX_S_RE = re.compile(r"(\d{10})")


def parse_name_time(path):
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


def get_photo_time(path):
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


def get_video_time(path):
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


def get_media_time(path):
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


def file_md5(path, chunk=1 << 20):
    h = hashlib.md5()
    with open(path, "rb") as f:
        while True:
            b = f.read(chunk)
            if not b:
                break
            h.update(b)
    return h.hexdigest()


def run(src, dst, dry_run=False, move=False, dedupe=True):
    src, dst = Path(src), Path(dst)
    dst_abs = dst.resolve()
    if not src.is_dir():
        print(f"错误:『{src}』不是一个文件夹")
        return

    files = sorted(
        p for p in src.rglob("*")
        if p.is_file()
        and p.suffix.lower() in ALL_EXTS
        and dst_abs not in p.resolve().parents  # 排除输出目录自身,防递归
    )
    if not files:
        print("没有找到照片或视频文件")
        return

    skipped_dup = []
    if dedupe:
        # 性能关键:先按文件大小分组,只有大小相同的才需要算 MD5,
        # 否则 1G 视频每次都要全量哈希一遍,白白多读一遍磁盘
        by_size = defaultdict(list)
        for f in files:
            by_size[f.stat().st_size].append(f)
        seen = {}
        kept = []
        for size, group in by_size.items():
            for f in group:
                key = (size, file_md5(f))
                if key in seen:
                    skipped_dup.append((str(f), seen[key]))
                    continue
                seen[key] = str(f)
                kept.append(f)
        files = kept
        if skipped_dup:
            print(f"去重:跳过 {len(skipped_dup)} 个重复文件")

    counter = defaultdict(int)
    stats = defaultdict(int)
    times = []
    t0 = datetime.now()
    print(f"共 {len(files)} 个文件")
    print("-" * 60)
    for f in files:
        t, src_tag = get_media_time(f)
        stats[src_tag] += 1
        times.append(t)
        key = t.strftime("%Y-%m-%d_%H%M%S")
        counter[key] += 1
        seq = counter[key]
        new_name = f"{key}_{seq:03d}{f.suffix.lower()}"
        target = dst / t.strftime("%Y") / t.strftime("%m") / new_name
        guard = 0
        while target.exists() and guard < 999:
            seq += 1
            new_name = f"{key}_{seq:03d}{f.suffix.lower()}"
            target = dst / t.strftime("%Y") / t.strftime("%m") / new_name
            guard += 1
        mark = "" if src_tag in ("EXIF", "meta") else f"  <-- 时间来自{src_tag}"
        print(f"[{src_tag:<5}] {t.strftime('%Y-%m-%d %H:%M:%S')}  {target.relative_to(dst)}{mark}")
        if not dry_run:
            try:
                target.parent.mkdir(parents=True, exist_ok=True)
                if move:
                    shutil.move(str(f), str(target))
                else:
                    shutil.copy2(str(f), str(target))
                stats["copied"] += 1
            except Exception as e:
                stats["fail"] += 1
                print(f"  !! 处理失败: {e}")
    elapsed = (datetime.now() - t0).total_seconds()

    print("-" * 60)
    if times:
        print(f"时间跨度: {min(times):%Y-%m-%d %H:%M} ~ {max(times):%Y-%m-%d %H:%M}")
    print(f"来源统计: EXIF {stats['EXIF']} | 视频元数据 {stats['meta']} | 文件名 {stats['name']} | 修改时间 {stats['mtime']} | 失败 {stats['fail']}")
    if not dry_run:
        biggest = max((p.stat().st_size for p in files), default=0)
        print(f"已处理 {stats['copied']} 个文件,总耗时 {elapsed:.1f} 秒(最大文件 {biggest / 1024 / 1024:.0f} MB)")


def pause():
    try:
        input("\n按回车键退出...")
    except EOFError:
        pass


def main():
    exe_dir = Path(sys.executable).resolve().parent if getattr(sys, "frozen", False) else Path(__file__).resolve().parent
    if len(sys.argv) < 2:
        print("=" * 56)
        print("  荣耀照片按时间整理工具")
        print("=" * 56)
        print("用法:把手机导出的照片文件夹(整个文件夹)")
        print("拖到本程序图标上,再松手。")
        print("整理结果会出现在本程序旁边的『照片整理』文件夹,")
        print("里面的文件都按拍摄时间排好了。")
        pause()
        return
    src = sys.argv[1]
    dst = exe_dir / "照片整理"
    print(f"输入文件夹: {src}")
    print(f"输出文件夹: {dst}")
    print()
    run(src, dst)
    print()
    print("整理完成!结果在『照片整理』文件夹里,按文件名排序即可按时间查看。")
    pause()


if __name__ == "__main__":
    main()
