// HEIF/HEIC EXIF 解析(纯 Go,手写 ISO BMFF 容器解析,零第三方依赖)
// 定位链路: 顶层找 meta -> 在 meta 内用 iinf 找 item_type=="Exif" 的 item_ID
//
//	-> 用 iloc 按 item_ID 查 extent(offset+length) -> 从 mdat 提取数据
//	-> EXIF 数据块前 4 字节为大端 exif_tiff_header_offset,其后是 TIFF -> 复用 parseTiffDateTime
//
// 覆盖 HEIC/HEIF(JPEG EXIF 在 APP1 段,结构不同,走 exif.go)。
// 内存策略: 全程流式 I/O,不整段读入 meta 负载;仅按需读取 iinf/iloc 等目标 box。
package core

import (
	"encoding/binary"
	"io"
	"os"
	"time"
)

// heifExifStatusKind 表示 HEIC/HEIF 的 Exif 提取状态
const (
	// heifNoExif 文件正常但未包含 Exif 数据(无 Exif item / 无 mdat / 无有效 extent)
	heifNoExif = "no-exif"
	// heifParseFailed 文件应含 Exif 但读取或结构不完整
	heifParseFailed = "parse-failed"
)

// heifStatus 描述 HEIC/HEIF 的 Exif 提取状态。
// ok=true 时 t 为解析出的拍摄时间,kind/desc 为空;
// ok=false 时 kind 为 heifNoExif 或 heifParseFailed。
type heifStatus struct {
	t    time.Time
	ok   bool
	kind string
	desc string
}

// heifExifStatus 读取并解析 HEIC/HEIF 的 Exif,返回提取状态。
func heifExifStatus(path string) heifStatus {
	f, err := os.Open(path)
	if err != nil {
		return heifStatus{ok: false, kind: heifParseFailed, desc: "HEIC 解析失败(无法打开文件)"}
	}
	defer f.Close()

	var mdatDataStart int64 = -1
	var exifItemID = -1
	var iloc []byte
	// 顶层 box 遍历
	for {
		bh, ok := readBoxHeader(f)
		if !ok {
			break
		}
		switch bh.typ {
		case "mdat":
			if mdatDataStart < 0 {
				mdatDataStart = bh.dataOff
			}
		case "meta":
			// meta 是 full box(4 字节 version/flags),其后才是子 box。
			// 按需遍历 meta 子 box,仅读取 iinf/iloc 的负载。
			if id, ilocData, ok2 := findMetaInfo(f, bh); ok2 {
				exifItemID = id
				iloc = ilocData
			}
		}
		if !skipToNextBox(f, bh) {
			break
		}
	}

	if exifItemID < 0 || len(iloc) == 0 || mdatDataStart < 0 {
		// 无 Exif item / 无 iloc / 无 mdat,视为该 HEIC 确实不含 Exif
		return heifStatus{ok: false, kind: heifNoExif, desc: "HEIC 无 Exif 数据(无 Exif item/iloc/mdat)"}
	}

	loc, ok := findExtent(iloc, exifItemID)
	if !ok {
		// 找不到 Exif item,说明文件正常但无 Exif 元数据
		return heifStatus{ok: false, kind: heifNoExif, desc: "HEIC 无 Exif 数据(未找到 Exif item)"}
	}
	// 从 mdat 按偏移读取 EXIF 数据块
	if _, err := f.Seek(mdatDataStart+loc.offset, io.SeekStart); err != nil {
		return heifStatus{ok: false, kind: heifParseFailed, desc: "HEIC 解析失败(数据偏移定位异常)"}
	}
	buf := make([]byte, loc.length)
	if _, err := io.ReadFull(f, buf); err != nil {
		return heifStatus{ok: false, kind: heifParseFailed, desc: "HEIC 解析失败(Exif 数据读取不完整)"}
	}
	// EXIF 数据块: 前 4 字节 = exif_tiff_header_offset(大端),其后为 TIFF 数据
	if len(buf) < 4 {
		return heifStatus{ok: false, kind: heifParseFailed, desc: "HEIC 解析失败(Exif 数据块过短)"}
	}
	tiffOff := int(binary.BigEndian.Uint32(buf[:4]))
	if tiffOff < 4 || tiffOff >= len(buf) {
		return heifStatus{ok: false, kind: heifParseFailed, desc: "HEIC 解析失败(TIFF 偏移无效)"}
	}
	t, ok := parseTiffDateTime(buf[tiffOff:])
	if !ok {
		return heifStatus{ok: false, kind: heifParseFailed, desc: "HEIC 解析失败(TIFF 内无 DateTimeOriginal)"}
	}
	return heifStatus{t: t, ok: true}
}

// HeifExifStatus 供 core.Run 判断 HEIC/HEIF 的 Exif 降级原因。
// 返回 (kind, desc):kind 为 "no-exif" 表示文件无 Exif 数据,
// "parse-failed" 表示解析失败;文件非 HEIC/HEIF 或解析成功时返回 ("","")。
func HeifExifStatus(path string) (kind, desc string) {
	st := heifExifStatus(path)
	return st.kind, st.desc
}

// parseHeifExifTime 读取 HEIC/HEIF 的 Exif 数据里的 DateTimeOriginal
func parseHeifExifTime(path string) (time.Time, bool) {
	st := heifExifStatus(path)
	return st.t, st.ok
}

// findMetaInfo 在 meta box 内按需遍历子 box。
// meta 是 full box(跳过 4 字节 version/flags 后的 body 才是子 box)。
// 仅读取 iinf/iloc 的负载并解析,返回 Exif 条目 item_ID 与 iloc payload。
// 其它子 box 仅 seek 跳过,不读入内存。
func findMetaInfo(f *os.File, meta boxHeader) (exifItemID int, iloc []byte, ok bool) {
	// 无论成功失败,退出时都恢复文件偏移到 meta 数据区起点
	defer f.Seek(meta.dataOff, io.SeekStart)

	// meta 数据区起点,前 4 字节为 version/flags(full box)
	bodyStart := meta.dataOff + 4
	bodyEnd := meta.dataOff + (meta.boxSize - meta.hdrSize)
	if bodyStart+8 > bodyEnd {
		return -1, nil, false
	}

	exifItemID = -1
	off := bodyStart
	for off+8 <= bodyEnd {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return -1, nil, false
		}
		child, ok2 := readBoxHeader(f)
		if !ok2 {
			return -1, nil, false
		}
		if off+child.boxSize > bodyEnd {
			return -1, nil, false // 子 box 超出 meta 范围
		}
		switch child.typ {
		case "iinf":
			// 读取 iinf 负载,解析 Exif item_ID
			payload, ok3 := readBoxPayload(f, child)
			if !ok3 {
				return -1, nil, false
			}
			if id, found := findExifItemID(payload); found {
				exifItemID = id
			}
		case "iloc":
			// 读取 iloc 负载
			payload, ok3 := readBoxPayload(f, child)
			if !ok3 {
				return -1, nil, false
			}
			iloc = payload
		}
		// 绝对定位到下一个子 box 头部。
		// 注意:对已调用 readBoxPayload 的 box,文件偏移已位于 box 末尾,
		// 此处用绝对 seek 覆盖,避免相对 skip 造成偏移双倍前进。
		if _, err := f.Seek(off+child.boxSize, io.SeekStart); err != nil {
			return -1, nil, false
		}
		off += child.boxSize
	}
	if exifItemID < 0 || len(iloc) == 0 {
		return -1, nil, false
	}
	return exifItemID, iloc, true
}

// exifLoc 描述 EXIF 数据块在 mdat 数据区中的偏移和长度
type exifLoc struct {
	offset int64
	length int64
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
//
//	[4 version/flags][2 item_ID][2 protection_index][4 item_type]...
//
// version >=2 在 item_type 前有 2 字节 reserved + 2 字节 protection_index:
//
//	[4 version/flags][2 item_ID][2 protection_index][2 reserved][2 flags][4 item_type]...
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
