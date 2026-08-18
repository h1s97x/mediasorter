// HEIF/HEIC EXIF 解析(纯 Go,手写 ISO BMFF 容器解析,零第三方依赖)
// 定位链路: 顶层找 meta -> 在 meta 内用 iinf 找 item_type=="Exif" 的 item_ID
//        -> 用 iloc 按 item_ID 查 extent(offset+length) -> 从 mdat 提取数据
//        -> EXIF 数据块前 4 字节为大端 exif_tiff_header_offset,其后是 TIFF -> 复用 parseTiffDateTime
// 覆盖 HEIC/HEIF(JPEG EXIF 在 APP1 段,结构不同,走 exif.go)。
package core

import (
	"encoding/binary"
	"os"
	"time"
)

// parseHeifExifTime 读取 HEIC/HEIF 的 Exif 数据里的 DateTimeOriginal
func parseHeifExifTime(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	var meta []byte
	var mdatDataStart int64 = -1
	// 顶层 box 遍历;对 meta 读取内容,对其它 box(尤其 mdat)只记录数据起点并 seek 跳过
	for {
		typ, dataStart, boxSize, ok := peekTopBox(f)
		if !ok {
			break
		}
		if typ == "mdat" && mdatDataStart < 0 {
			mdatDataStart = dataStart
		}
		if typ == "meta" {
			// 读取 meta 负载(version/flags + 子 box)
			payload := make([]byte, boxSize-8)
			if _, err := f.Read(payload); err != nil {
				break
			}
			meta = payload
			continue
		}
		// 跳到下一个 box:已读过 8 字节头,需跳过剩余 boxSize-8 字节
		if _, err := f.Seek(int64(boxSize-8), 1); err != nil {
			break
		}
	}

	if len(meta) == 0 || mdatDataStart < 0 {
		return time.Time{}, false
	}

	loc, ok := extractExifLocFromMeta(meta)
	if !ok {
		return time.Time{}, false
	}
	// 从 mdat 按偏移读取 EXIF 数据块
	if _, err := f.Seek(mdatDataStart+loc.offset, 0); err != nil {
		return time.Time{}, false
	}
	buf := make([]byte, loc.length)
	if _, err := f.Read(buf); err != nil {
		return time.Time{}, false
	}
	// EXIF 数据块: 前 4 字节 = exif_tiff_header_offset(大端),其后为 TIFF 数据
	if len(buf) < 4 {
		return time.Time{}, false
	}
	tiffOff := int(binary.BigEndian.Uint32(buf[:4]))
	if tiffOff < 4 || tiffOff >= len(buf) {
		return time.Time{}, false
	}
	return parseTiffDateTime(buf[tiffOff:])
}

// exifLoc 描述 EXIF 数据块在 mdat 数据区中的偏移和长度
type exifLoc struct {
	offset int64
	length int64
}

// extractExifLocFromMeta 在 meta 内解析 iinf/iloc,定位 Exif 条目的数据位置。
// meta 是 full box,跳过 4 字节 version/flags 后的 body 才是子 box。
func extractExifLocFromMeta(meta []byte) (exifLoc, bool) {
	if len(meta) < 4 {
		return exifLoc{}, false
	}
	body := meta[4:]

	exifItemID := -1
	var iloc []byte
	parseMetaChildren(body, func(typ string, payload []byte) {
		switch typ {
		case "iinf":
			if id, ok := findExifItemID(payload); ok {
				exifItemID = id
			}
		case "iloc":
			iloc = payload
		}
	})
	if exifItemID < 0 || len(iloc) == 0 {
		return exifLoc{}, false
	}
	return findExtent(iloc, exifItemID)
}

// parseMetaChildren 遍历 meta body 内的子 box
func parseMetaChildren(body []byte, visit func(typ string, payload []byte)) {
	for off := 0; off+8 <= len(body); {
		size := int(binary.BigEndian.Uint32(body[off : off+4]))
		typ := string(body[off+4 : off+8])
		if size == 1 {
			if off+16 > len(body) {
				return
			}
			size = int(binary.BigEndian.Uint64(body[off+8 : off+16]))
			if size < 16 {
				return
			}
		} else if size < 8 {
			return
		}
		if off+size > len(body) {
			return
		}
		visit(typ, body[off+8:off+size])
		off += size
	}
}

// findExifItemID 解析 iinf,返回 item_type=="Exif" 的 item_ID。
// iinf 是 full box: 4 字节 version/flags 后,version<2 时 2 字节 entry_count。
func findExifItemID(iinf []byte) (int, bool) {
	if len(iinf) < 6 {
		return -1, false
	}
	version := iinf[0]
	body := iinf[4:]
	var entries []byte
	var count int
	if version < 2 {
		if len(body) < 2 {
			return -1, false
		}
		count = int(binary.BigEndian.Uint16(body[:2]))
		entries = body[2:]
	} else {
		if len(body) < 4 {
			return -1, false
		}
		count = int(binary.BigEndian.Uint32(body[:4]))
		entries = body[4:]
	}
	// 每个条目是 infe box
	off := 0
	for n := 0; n < count; n++ {
		if off+8 > len(entries) {
			break
		}
		size := int(binary.BigEndian.Uint32(entries[off : off+4]))
		typ := string(entries[off+4 : off+8])
		if size == 1 {
			if off+16 > len(entries) {
				break
			}
			size = int(binary.BigEndian.Uint64(entries[off+8 : off+16]))
			if size < 16 {
				break
			}
		} else if size < 8 {
			break
		}
		if off+size > len(entries) {
			break
		}
		if typ == "infe" {
			if id, isExif := parseInfe(entries[off+8 : off+size]); isExif {
				return id, true
			}
		}
		off += size
	}
	return -1, false
}

// parseInfe 解析单个 infe box,返回 item_ID 和是否为 Exif 类型。
// infe 是 full box;version 0/1 布局:
//   [4 version/flags][2 item_ID][2 protection_index][4 item_type]...
// version >=2 在 item_type 前有 2 字节 reserved + 2 字节 protection_index:
//   [4 version/flags][2 item_ID][2 protection_index][2 reserved][2 flags][4 item_type]...
func parseInfe(infe []byte) (int, bool) {
	if len(infe) < 12 {
		return -1, false
	}
	version := infe[0]
	var itemID int
	var typeOff int
	switch {
	case version == 0 || version == 1:
		itemID = int(binary.BigEndian.Uint16(infe[4:6]))
		typeOff = 8
	default: // version >= 2
		if len(infe) < 16 {
			return -1, false
		}
		itemID = int(binary.BigEndian.Uint16(infe[4:6]))
		typeOff = 12
	}
	if typeOff+4 > len(infe) {
		return -1, false
	}
	itemType := string(infe[typeOff : typeOff+4])
	return itemID, itemType == "Exif"
}

// findExtent 解析 iloc,返回指定 item_ID 的第一个 extent(offset,length)。
// offset 相对 mdat 数据起点。
func findExtent(iloc []byte, wantID int) (exifLoc, bool) {
	if len(iloc) < 8 {
		return exifLoc{}, false
	}
	version := iloc[0]
	offSize := int(iloc[4] >> 4 & 0x0F)
	lenSize := int(iloc[4] & 0x0F)
	baseOffSize := int(iloc[5] >> 4 & 0x0F)
	idxSize := int(iloc[5] & 0x0F) // version 0 时是 reserved

	p := 6
	var itemCount int
	if version < 2 {
		if p+2 > len(iloc) {
			return exifLoc{}, false
		}
		itemCount = int(binary.BigEndian.Uint16(iloc[p : p+2]))
		p += 2
	} else {
		if p+4 > len(iloc) {
			return exifLoc{}, false
		}
		itemCount = int(binary.BigEndian.Uint32(iloc[p : p+4]))
		p += 4
	}
	for i := 0; i < itemCount; i++ {
		var itemID int
		if version < 2 {
			if p+2 > len(iloc) {
				return exifLoc{}, false
			}
			itemID = int(binary.BigEndian.Uint16(iloc[p : p+2]))
			p += 2
		} else {
			if p+4 > len(iloc) {
				return exifLoc{}, false
			}
			itemID = int(binary.BigEndian.Uint32(iloc[p : p+4]))
			p += 4
		}
		if version == 1 || version == 2 {
			p += 2 // 12 bits reserved + 4 bits construction_method
		}
		p += 2 // data_reference_index
		if baseOffSize > 0 {
			if p+baseOffSize > len(iloc) {
				return exifLoc{}, false
			}
			p += baseOffSize
		}
		if p+2 > len(iloc) {
			return exifLoc{}, false
		}
		extentCount := int(binary.BigEndian.Uint16(iloc[p : p+2]))
		p += 2
		for j := 0; j < extentCount; j++ {
			if (version == 1 || version == 2) && idxSize > 0 {
				if p+idxSize > len(iloc) {
					return exifLoc{}, false
				}
				p += idxSize
			}
			var extentOffset, extentLength int64
			if offSize > 0 {
				if p+offSize > len(iloc) {
					return exifLoc{}, false
				}
				extentOffset = readUint(iloc[p : p+offSize])
				p += offSize
			}
			if lenSize > 0 {
				if p+lenSize > len(iloc) {
					return exifLoc{}, false
				}
				extentLength = readUint(iloc[p : p+lenSize])
				p += lenSize
			}
			if itemID == wantID {
				return exifLoc{offset: extentOffset, length: extentLength}, true
			}
		}
	}
	return exifLoc{}, false
}

// readUint 从固定宽度(1-8 字节)大端字节读无符号整数
func readUint(b []byte) int64 {
	var v int64
	for _, c := range b {
		v = v<<8 | int64(c)
	}
	return v
}

// peekTopBox 从当前文件偏移读取一个顶层 box 的头,返回其类型、
// 数据区起始偏移(dataStart,即跳过 header 后)和 box 总大小(boxSize,含头)。
// 不读取负载数据,由调用方决定读取或跳过。
func peekTopBox(f *os.File) (typ string, dataStart int64, boxSize int64, ok bool) {
	var hdr [8]byte
	if _, err := f.Read(hdr[:]); err != nil {
		return "", 0, 0, false
	}
	size := binary.BigEndian.Uint32(hdr[:4])
	typ = string(hdr[4:8])
	if size == 1 {
		var ext [8]byte
		if _, err := f.Read(ext[:]); err != nil {
			return "", 0, 0, false
		}
		sz64 := binary.BigEndian.Uint64(ext[:])
		if sz64 < 16 {
			return "", 0, 0, false
		}
		pos, _ := f.Seek(0, 1) // 当前偏移 = 16 字节头结束
		return typ, pos, int64(sz64), true
	}
	if size < 8 {
		return "", 0, 0, false
	}
	pos, _ := f.Seek(0, 1) // 当前偏移 = 8 字节头结束
	return typ, pos, int64(size), true
}
