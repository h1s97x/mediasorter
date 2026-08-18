// MP4 creation_time 解析(纯 Go,跨平台)
// 定位 moov -> mvhd,读取大端 creation_time(1904-01-01 UTC 起算,秒),转本地时间
// 全程流式 I/O:按需 seek 定位目标字段,不将整个 moov/mvhd 负载读入内存。
package core

import (
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

	// 顶层 box 遍历,按需定位 moov(不读取其负载)
	moov, ok := findTopBox(f, "moov")
	if !ok {
		return time.Time{}, false
	}
	// 在 moov 内按需定位 mvhd,解析 creation_time
	return parseMvhdBySeek(f, moov)
}

// findTopBox 遍历顶层 box,按需定位指定 type 的 box。
// 返回该 box 的头部信息(数据区偏移/总大小),但不读取其负载。
func findTopBox(f *os.File, want string) (boxHeader, bool) {
	for {
		bh, ok := readBoxHeader(f)
		if !ok {
			return boxHeader{}, false
		}
		if bh.typ == want {
			return bh, true
		}
		if !skipToNextBox(f, bh) {
			return boxHeader{}, false
		}
	}
}

// parseMvhdBySeek 在 moov box 内按需遍历子 box,定位 mvhd 并解析 creation_time。
// 不将 moov/mvhd 负载读入内存,只 seek 到所需字段。
func parseMvhdBySeek(f *os.File, moov boxHeader) (time.Time, bool) {
	// moov 数据区终点 = dataOff + (boxSize - hdrSize)
	end := moov.dataOff + (moov.boxSize - moov.hdrSize)
	off := moov.dataOff
	for off+8 <= end {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return time.Time{}, false
		}
		bh, ok := readBoxHeader(f)
		if !ok {
			return time.Time{}, false
		}
		if off+bh.boxSize > end {
			return time.Time{}, false // 子 box 超出 moov 范围
		}
		if bh.typ == "mvhd" {
			return parseMvhdHeader(f, bh)
		}
		off += bh.boxSize
	}
	return time.Time{}, false
}

// parseMvhdHeader 按需解析 mvhd box 的 version 和 creation_time 字段。
// 文件偏移需位于 mvhd 数据区起点(bh.dataOff)。
func parseMvhdHeader(f *os.File, bh boxHeader) (time.Time, bool) {
	// mvhd 是 full box: [1 version][3 flags][creation_time(4或8字节)]...
	// 数据区(跳过头部)至少 8 字节才能读到 version + creation_time 的起点
	if bh.boxSize-bh.hdrSize < 8 {
		return time.Time{}, false
	}
	// 读取 version(数据区第 0 字节)
	var vB [1]byte
	if _, err := f.ReadAt(vB[:], bh.dataOff); err != nil {
		return time.Time{}, false
	}
	version := vB[0]
	var secs int64
	switch version {
	case 1:
		// creation_time 为 8 字节,位于数据区 offset 4(跳过 version+flags)
		v, ok := readBoxUint(f, bh.dataOff+4, 8)
		if !ok {
			return time.Time{}, false
		}
		secs = v - mpegEpochDelta
	default: // version 0
		// creation_time 为 4 字节,位于数据区 offset 4
		v, ok := readBoxUint(f, bh.dataOff+4, 4)
		if !ok {
			return time.Time{}, false
		}
		secs = v - mpegEpochDelta
	}
	if secs < 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0).Local(), true
}
