// 时间提取:文件名时间戳解析(跨平台,零依赖)
package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	nameDateTimeRe = regexp.MustCompile(`(\d{8})[_-](\d{6})`)                 // IMG_20260817_104121 / REC_20260817_104121
	nameDashRe     = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})[_-](\d{6})`) // Screenshot_2026-08-17-104121
	unixMsRe       = regexp.MustCompile(`(\d{13})`)                           // 毫秒时间戳
	unixSRe        = regexp.MustCompile(`(\d{10})`)                           // 秒时间戳 mmexport1722534678
)

// parseFileNameTime 从文件名提取拍摄时间
func parseFileNameTime(name string) (time.Time, bool) {
	if m := nameDateTimeRe.FindStringSubmatch(name); m != nil {
		if t, err := time.ParseInLocation("20060102150405", m[1]+m[2], time.Local); err == nil {
			// 防止 2月30日 之类非法日期被 Go 自动进位
			// 通过反向格式化成原串比较验证日期合法
			if t.Format("20060102150405") == m[1]+m[2] {
				return t, true
			}
		}
	}

	if m := nameDashRe.FindStringSubmatch(name); m != nil {
		hm := m[4]
		if len(hm) == 6 {
			// 用 ParseInLocation + 反向格式化验证日期合法,
			// 防止 2月30日 之类非法日期被 Go 自动进位。
			// Go 的 time 支持 year 0,需显式排除 0000 年
			y, _ := strconv.Atoi(m[1])
			if y > 0 {
				str := m[1] + "-" + m[2] + "-" + m[3] + " " + hm
				if t, err := time.ParseInLocation("2006-01-02 150405", str, time.Local); err == nil {
					if t.Format("2006-01-02 150405") == str {
						return t, true
					}
				}
			}
		}
	}

	if m := unixMsRe.FindStringSubmatch(name); m != nil {
		if v, err := strconv.ParseInt(m[1], 10, 64); err == nil && v >= 1_000_000_000_000 && v <= 20_000_000_000_000 {
			return time.Unix(v/1000, 0).Local(), true
		}
	}
	if m := unixSRe.FindStringSubmatch(name); m != nil {
		if v, err := strconv.ParseInt(m[1], 10, 64); err == nil && v >= 1_000_000_000 && v <= 2_000_000_000 {
			return time.Unix(v, 0).Local(), true
		}
	}
	return time.Time{}, false
}

// fileFallbackTime 创建/修改时间取较旧者(更接近拍摄时间)
func fileFallbackTime(path string) (time.Time, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	mt := fi.ModTime()
	// 创建时间仅部分平台可得(Windows/macOS);取不到就用修改时间
	ct, ok := birthTime(path)
	if !ok {
		return mt, true
	}
	if ct.Before(mt) {
		return ct, true
	}
	return mt, true
}

// GetCaptureTime 四级兜底:EXIF -> 视频元数据 -> 文件名 -> 文件时间
// 返回 (时间, 来源标记, 是否成功)
func GetCaptureTime(path string) (time.Time, string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if imageExts[ext] {
		// JPEG 走 APP1 EXIF;HEIC/HEIF 走 ISO BMFF meta 里的 Exif 项
		switch ext {
		case ".jpg", ".jpeg":
			if t, ok := parseJpegExifTime(path); ok {
				return t, "EXIF", true
			}
		case ".heic", ".heif":
			if t, ok := parseHeifExifTime(path); ok {
				return t, "EXIF", true
			}
		}
	}
	if videoExts[ext] {
		if t, ok := parseMp4CreationTime(path); ok {
			return t, "meta", true
		}
	}
	if t, ok := parseFileNameTime(filepath.Base(path)); ok {
		return t, "name", true
	}
	if t, ok := fileFallbackTime(path); ok {
		return t, "mtime", true
	}
	return time.Time{}, "", false
}
