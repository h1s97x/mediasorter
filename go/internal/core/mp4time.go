// MP4 creation_time 解析(纯 Go,跨平台)
// 定位 moov -> mvhd,读取大端 creation_time(1904-01-01 UTC 起算,秒),转本地时间
package core

import (
	"encoding/binary"
	"io"
	"os"
	"time"
)

const (
	mpegEpochDelta = 2082844800 // 1904-01-01 -> 1970-01-01 的秒数
)

// parseMp4CreationTime 读取 MP4 文件头里的创建时间(UTC -> 本地)
func parseMp4CreationTime(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	// 顶层 box 遍历,找 moov
	moov := findBox(f, "moov")
	if moov == nil {
		return time.Time{}, false
	}
	// 在 moov 内找 mvhd
	if t, ok := parseMvhd(moov); ok {
		return t, true
	}
	return time.Time{}, false
}

// findBox 读取顶层 box,返回匹配 type 的 payload。
// 支持 32 位 size、64 位扩展(size==1)与 size==0(延伸至文件末尾)三种边界。
func findBox(f *os.File, want string) []byte {
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return nil
		}
		size := binary.BigEndian.Uint32(hdr[:4])
		typ := string(hdr[4:8])
		if size == 0 { // box 延伸至文件末尾
			if typ == want {
				rest, err := io.ReadAll(f)
				if err != nil {
					return nil
				}
				return rest
			}
			return nil // 已到文件末尾,无更多 box
		}
		if size == 1 { // 64 位扩展 size
			var ext [8]byte
			if _, err := io.ReadFull(f, ext[:]); err != nil {
				return nil
			}
			size64 := binary.BigEndian.Uint64(ext[:])
			if size64 < 16 {
				return nil
			}
			if typ == want {
				payload := make([]byte, size64-16)
				if _, err := io.ReadFull(f, payload); err != nil {
					return nil
				}
				return payload
			}
			if _, err := f.Seek(int64(size64)-16, io.SeekCurrent); err != nil {
				return nil
			}
			continue
		}
		if size < 8 {
			return nil
		}
		if typ == want {
			payload := make([]byte, size-8)
			if _, err := io.ReadFull(f, payload); err != nil {
				return nil
			}
			return payload
		}
		if _, err := f.Seek(int64(size)-8, io.SeekCurrent); err != nil {
			return nil
		}
	}
}

// parseMvhd 在 moov payload 里找 mvhd,解析 creation_time。
// 复用统一的 parseChildren 遍历,可正确处理 free/wide 等占位 box 及
// size==0 / size==1 边界。
func parseMvhd(moov []byte) (time.Time, bool) {
	var t time.Time
	var found bool
	parseChildren(moov, func(typ string, payload []byte) bool {
		if typ == "mvhd" {
			if tt, ok := parseMvhdPayload(payload); ok {
				t = tt
				found = true
				return false // 命中 mvhd,提前终止遍历
			}
		}
		return true
	})
	return t, found
}

func parseMvhdPayload(p []byte) (time.Time, bool) {
	if len(p) < 8 {
		return time.Time{}, false
	}
	version := p[0]
	var secs int64
	switch version {
	case 1:
		if len(p) < 16 {
			return time.Time{}, false
		}
		secs = int64(binary.BigEndian.Uint64(p[4:12])) - mpegEpochDelta
	default:
		if len(p) < 12 {
			return time.Time{}, false
		}
		secs = int64(binary.BigEndian.Uint32(p[4:8])) - mpegEpochDelta
	}
	if secs < 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0).Local(), true
}
