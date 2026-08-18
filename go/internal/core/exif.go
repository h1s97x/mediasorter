// JPEG EXIF 解析(纯 Go,手写 APP1 -> TIFF -> ExifIFD -> DateTimeOriginal)
// 覆盖 JPG/JPEG;HEIC/HEIF 走 ISO BMFF 的 meta/Exif 提取(见 heif.go),
// 解析失败时降级到文件名/mtime 兜底。
package core

import (
	"encoding/binary"
	"io"
	"os"
	"strings"
	"time"
)

// parseJpegExifTime 读取 JPEG 的 DateTimeOriginal(0x9003)
func parseJpegExifTime(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	var hdr [2]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || hdr[0] != 0xFF || hdr[1] != 0xD8 {
		return time.Time{}, false // 不是 JPEG
	}

	for {
		var seg [2]byte
		if _, err := io.ReadFull(f, seg[:]); err != nil {
			return time.Time{}, false
		}
		if seg[0] != 0xFF {
			return time.Time{}, false // 文件损坏
		}
		marker := seg[1]
		// 无长度段的 marker:SOI/EOI/RSTx/TEM
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			continue
		}
		if marker == 0xD9 { // EOI
			return time.Time{}, false
		}
		var lenB [2]byte
		if _, err := io.ReadFull(f, lenB[:]); err != nil {
			return time.Time{}, false
		}
		segLen := int(binary.BigEndian.Uint16(lenB[:])) - 2
		if segLen < 0 {
			return time.Time{}, false
		}
		payload := make([]byte, segLen)
		if _, err := io.ReadFull(f, payload); err != nil {
			return time.Time{}, false
		}
		if marker == 0xE1 && segLen > 6 && string(payload[:6]) == "Exif\x00\x00" {
			return parseTiffDateTime(payload[6:])
		}
		if marker == 0xDA { // SOS:后面是图像数据,EXIF 必然在之前
			return time.Time{}, false
		}
	}
}

// parseTiffDateTime 解析 TIFF 结构,取 ExifIFD 的 DateTimeOriginal
func parseTiffDateTime(b []byte) (time.Time, bool) {
	if len(b) < 8 {
		return time.Time{}, false
	}
	var order binary.ByteOrder
	switch {
	case b[0] == 'I' && b[1] == 'I':
		order = binary.LittleEndian
	case b[0] == 'M' && b[1] == 'M':
		order = binary.BigEndian
	default:
		return time.Time{}, false
	}
	if order.Uint16(b[2:4]) != 42 {
		return time.Time{}, false // 不是 TIFF
	}
	ifdOff := int(order.Uint32(b[4:8]))
	if ifdOff+2 > len(b) {
		return time.Time{}, false
	}

	readDateTime := func(entryOff int) string {
		if entryOff+12 > len(b) {
			return ""
		}
		tag := order.Uint16(b[entryOff : entryOff+2])
		typ := order.Uint16(b[entryOff+2 : entryOff+4])
		if tag != 0x9003 && tag != 0x0132 { // DateTimeOriginal / DateTime
			return ""
		}
		if typ != 2 { // ASCII
			return ""
		}
		return readAsciiValue(b, entryOff, order)
	}

	var dt string
	cnt := int(order.Uint16(b[ifdOff : ifdOff+2]))
	var exifPtr int
	for i := 0; i < cnt; i++ {
		e := ifdOff + 2 + i*12
		if e+12 > len(b) {
			break
		}
		tag := order.Uint16(b[e : e+2])
		switch tag {
		case 0x8769: // ExifIFDPointer
			exifPtr = int(order.Uint32(b[e+8 : e+12]))
		case 0x0132:
			if dt == "" {
				dt = readDateTime(e)
			}
		}
	}
	// ExifIFD 里找 0x9003
	if exifPtr > 0 && exifPtr+2 <= len(b) {
		cnt2 := int(order.Uint16(b[exifPtr : exifPtr+2]))
		for i := 0; i < cnt2; i++ {
			e := exifPtr + 2 + i*12
			if e+12 > len(b) {
				break
			}
			if order.Uint16(b[e:e+2]) == 0x9003 {
				if s := readDateTime(e); s != "" {
					dt = s
				}
			}
		}
	}
	if dt == "" {
		return time.Time{}, false
	}
	s := strings.TrimSpace(strings.Trim(dt, "\x00 "))
	for _, layout := range []string{"2006:01:02 15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// readAsciiValue 读取 ASCII 类型条目:count<=4 直接取 4 字节;否则按 offset 读
func readAsciiValue(b []byte, entryOff int, order binary.ByteOrder) string {
	count := order.Uint32(b[entryOff+4 : entryOff+8])
	if count == 0 || count > 64*1024 {
		return ""
	}
	if count <= 4 {
		return string(b[entryOff+8 : entryOff+8+int(count)])
	}
	off := int(order.Uint32(b[entryOff+8 : entryOff+12]))
	if off+int(count) > len(b) {
		return ""
	}
	return string(b[off : off+int(count)])
}
