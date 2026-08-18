package core

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// ===== findExifItemID 测试 =====

// buildIinf 构造 iinf 负载(不含 iinf box 头,仅 full box body)。
// version<2 时用 2 字节 count;version>=2 用 4 字节 count。
func buildIinf(version byte, infeBoxes [][]byte) []byte {
	var b bytes.Buffer
	b.WriteByte(version)
	b.Write([]byte{0, 0, 0})
	if version < 2 {
		binary.Write(&b, binary.BigEndian, uint16(len(infeBoxes)))
	} else {
		binary.Write(&b, binary.BigEndian, uint32(len(infeBoxes)))
	}
	for _, in := range infeBoxes {
		b.Write(in)
	}
	return b.Bytes()
}

// buildInfeV1 构造 version 0/1 的 infe box(含头)。
func buildInfeV1(itemID uint16, itemType string) []byte {
	var body bytes.Buffer
	body.WriteByte(1) // version 1
	body.Write([]byte{0, 0, 0})
	binary.Write(&body, binary.BigEndian, itemID)
	binary.Write(&body, binary.BigEndian, uint16(0)) // protection_index
	body.WriteString(itemType)
	return writeBoxBytes("infe", body.Bytes())
}

// buildInfeV2 构造 version 2 的 infe box(含头)。
func buildInfeV2(itemID uint16, itemType string) []byte {
	var body bytes.Buffer
	body.WriteByte(2) // version 2
	body.Write([]byte{0, 0, 0})
	binary.Write(&body, binary.BigEndian, itemID)
	binary.Write(&body, binary.BigEndian, uint16(0)) // protection_index
	binary.Write(&body, binary.BigEndian, uint16(0)) // reserved
	binary.Write(&body, binary.BigEndian, uint16(0)) // flags
	body.WriteString(itemType)
	return writeBoxBytes("infe", body.Bytes())
}

// writeBoxBytes 构造一个含头的 box 字节(不使用 *bytes.Buffer 以复用)。
func writeBoxBytes(typ string, payload []byte) []byte {
	var b bytes.Buffer
	total := 8 + len(payload)
	binary.Write(&b, binary.BigEndian, uint32(total))
	b.WriteString(typ)
	b.Write(payload)
	return b.Bytes()
}

// TestFindExifItemID_V1 找到 version1 的 Exif item。
func TestFindExifItemID_V1(t *testing.T) {
	iinf := buildIinf(0, [][]byte{
		buildInfeV1(1, "hvc1"),
		buildInfeV1(2, "Exif"),
	})
	id, ok := findExifItemID(iinf)
	if !ok || id != 2 {
		t.Errorf("got id=%d ok=%v, want id=2 ok=true", id, ok)
	}
}

// TestFindExifItemID_V2 找到 version2 的 Exif item。
func TestFindExifItemID_V2(t *testing.T) {
	iinf := buildIinf(2, [][]byte{
		buildInfeV2(1, "Exif"),
	})
	id, ok := findExifItemID(iinf)
	if !ok || id != 1 {
		t.Errorf("got id=%d ok=%v, want id=1 ok=true", id, ok)
	}
}

// TestFindExifItemID_NoExif 无 Exif item。
func TestFindExifItemID_NoExif(t *testing.T) {
	iinf := buildIinf(0, [][]byte{
		buildInfeV1(1, "hvc1"),
		buildInfeV1(2, "grid"),
	})
	if _, ok := findExifItemID(iinf); ok {
		t.Error("无 Exif 应返回 false")
	}
}

// TestFindExifItemID_EmptyIinf 空 iinf。
func TestFindExifItemID_EmptyIinf(t *testing.T) {
	if _, ok := findExifItemID([]byte{0, 0, 0, 0, 0, 0}); ok {
		t.Error("空 iinf 应返回 false")
	}
	// 数据不足 6 字节
	if _, ok := findExifItemID([]byte{1, 2, 3}); ok {
		t.Error("过短 iinf 应返回 false")
	}
}

// ===== findExtent 测试 =====

// buildIlocV0 构造 version 0 iloc 负载。
// offSize/lenSize/baseOffSize 控制字段宽度。
func buildIlocV0(offSize, lenSize byte, items []ilocItem) []byte {
	var b bytes.Buffer
	b.WriteByte(0)
	b.Write([]byte{0, 0, 0})
	b.WriteByte(offSize<<4 | lenSize) // offset_size, length_size
	b.WriteByte(0)                    // base_offset_size=0, index_size=0
	binary.Write(&b, binary.BigEndian, uint16(len(items)))
	for _, it := range items {
		binary.Write(&b, binary.BigEndian, it.itemID)
		binary.Write(&b, binary.BigEndian, uint16(0)) // data_reference_index
		binary.Write(&b, binary.BigEndian, uint16(1)) // extent_count=1
		if offSize > 0 {
			writeFixedUint(&b, uint64(it.offset), offSize)
		}
		if lenSize > 0 {
			writeFixedUint(&b, uint64(it.length), lenSize)
		}
	}
	return b.Bytes()
}

type ilocItem struct {
	itemID uint16
	offset uint64
	length uint64
}

// writeFixedUint 写固定宽度大端整数。
func writeFixedUint(b *bytes.Buffer, v uint64, width byte) {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	b.Write(tmp[8-int(width):])
}

// TestFindExtent_Found 找到匹配 item 的 extent。
func TestFindExtent_Found(t *testing.T) {
	iloc := buildIlocV0(4, 4, []ilocItem{
		{itemID: 1, offset: 0, length: 64},
		{itemID: 2, offset: 100, length: 200},
	})
	loc, ok := findExtent(iloc, 2)
	if !ok {
		t.Fatal("期望找到 extent")
	}
	if loc.offset != 100 || loc.length != 200 {
		t.Errorf("got (%d,%d), want (100,200)", loc.offset, loc.length)
	}
}

// TestFindExtent_NotMatch 无匹配 item。
func TestFindExtent_NotMatch(t *testing.T) {
	iloc := buildIlocV0(4, 4, []ilocItem{{itemID: 1, offset: 0, length: 64}})
	if _, ok := findExtent(iloc, 99); ok {
		t.Error("无匹配 item 应返回 false")
	}
}

// TestFindExtent_ShortIloc 过短 iloc。
func TestFindExtent_ShortIloc(t *testing.T) {
	if _, ok := findExtent([]byte{1, 2}, 1); ok {
		t.Error("过短 iloc 应返回 false")
	}
}

// TestFindExtent_ZeroWidth offSize/lenSize 为 0 时偏移/长度为 0。
func TestFindExtent_ZeroWidth(t *testing.T) {
	// offSize=0,lenSize=0
	iloc := buildIlocV0(0, 0, []ilocItem{{itemID: 1}})
	loc, ok := findExtent(iloc, 1)
	if !ok {
		t.Fatal("期望找到 extent")
	}
	if loc.offset != 0 || loc.length != 0 {
		t.Errorf("zero width 应得 (0,0), got (%d,%d)", loc.offset, loc.length)
	}
}

// TestFindExtent_V1 带 construction_method 的 version1 iloc。
func TestFindExtent_V1(t *testing.T) {
	var b bytes.Buffer
	b.WriteByte(1)
	b.Write([]byte{0, 0, 0})
	b.WriteByte(0x44)                             // offset_size=4, length_size=4
	b.WriteByte(0x00)                             // base_offset=0, index_size=0
	binary.Write(&b, binary.BigEndian, uint16(1)) // item_count
	binary.Write(&b, binary.BigEndian, uint16(5)) // item_ID
	binary.Write(&b, binary.BigEndian, uint16(0)) // construction_method(reserved+method)
	binary.Write(&b, binary.BigEndian, uint16(0)) // data_reference_index
	binary.Write(&b, binary.BigEndian, uint16(1)) // extent_count
	binary.Write(&b, binary.BigEndian, uint32(77))
	binary.Write(&b, binary.BigEndian, uint32(88))
	loc, ok := findExtent(b.Bytes(), 5)
	if !ok {
		t.Fatal("V1 期望找到 extent")
	}
	if loc.offset != 77 || loc.length != 88 {
		t.Errorf("got (%d,%d), want (77,88)", loc.offset, loc.length)
	}
}

// ===== 64 位扩展 size 与 free 占位块测试 =====

// TestParseHeifExifTime_SkipFreeBoxes meta 内包含 free 占位块仍能解析。
func TestParseHeifExifTime_SkipFreeBoxes(t *testing.T) {
	le := binary.LittleEndian
	strData := []byte("2023:05:20 09:15:00\x00")
	strOff := uint32(8 + 2 + 12 + 4 + 2 + 12 + 4)
	ifd0Entry := make([]byte, 12)
	le.PutUint16(ifd0Entry[0:2], 0x8769)
	le.PutUint16(ifd0Entry[2:4], 4)
	le.PutUint32(ifd0Entry[4:8], 1)
	le.PutUint32(ifd0Entry[8:12], uint32(8+2+12+4))
	exifEntry := tiffAsciiEntry(le, 0x9003, "2023:05:20 09:15:00", strOff)
	tiff := buildTiff(t, "II", ifd0Entry, exifEntry, strData)
	extentLen := uint32(4 + len(tiff))

	// 构造 meta: free + iinf + free + iloc 交错
	var metaBody bytes.Buffer
	metaBody.WriteByte(0)
	metaBody.Write([]byte{0, 0, 0})
	metaBody.Write(writeBoxBytes("free", make([]byte, 4)))

	var iinfBody bytes.Buffer
	iinfBody.WriteByte(0)
	iinfBody.Write([]byte{0, 0, 0})
	binary.Write(&iinfBody, binary.BigEndian, uint16(1))
	iinfBody.Write(buildInfeV2(1, "Exif"))
	metaBody.Write(writeBoxBytes("iinf", iinfBody.Bytes()))
	metaBody.Write(writeBoxBytes("free", make([]byte, 8)))

	var ilocBody bytes.Buffer
	ilocBody.WriteByte(0)
	ilocBody.Write([]byte{0, 0, 0})
	ilocBody.WriteByte(0x44)
	ilocBody.WriteByte(0x00)
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint16(0))
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint32(0))
	binary.Write(&ilocBody, binary.BigEndian, extentLen)
	metaBody.Write(writeBoxBytes("iloc", ilocBody.Bytes()))

	var buf bytes.Buffer
	writeBox(&buf, "meta", metaBody.Bytes())
	mdat := make([]byte, 4+len(tiff))
	binary.BigEndian.PutUint32(mdat[:4], 4)
	copy(mdat[4:], tiff)
	writeBox(&buf, "mdat", mdat)

	p := writeTempFile(t, buf.Bytes())
	tm, ok := parseHeifExifTime(p)
	if !ok {
		t.Fatal("含 free 占位块的 HEIC 应解析成功")
	}
	want := time.Date(2023, 5, 20, 9, 15, 0, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseHeifExifTime_NoExifItem HEIC 无 Exif item -> no-exif 状态。
func TestParseHeifExifTime_NoExifItem(t *testing.T) {
	var metaBody bytes.Buffer
	metaBody.WriteByte(0)
	metaBody.Write([]byte{0, 0, 0})
	var iinfBody bytes.Buffer
	iinfBody.WriteByte(0)
	iinfBody.Write([]byte{0, 0, 0})
	binary.Write(&iinfBody, binary.BigEndian, uint16(1))
	iinfBody.Write(buildInfeV1(1, "hvc1")) // 非 Exif
	metaBody.Write(writeBoxBytes("iinf", iinfBody.Bytes()))
	var buf bytes.Buffer
	writeBox(&buf, "meta", metaBody.Bytes())
	writeBox(&buf, "mdat", make([]byte, 10))

	p := writeTempFile(t, buf.Bytes())
	if _, ok := parseHeifExifTime(p); ok {
		t.Error("无 Exif item 应返回 false")
	}
}

// TestParseHeifExifTime_NoMdat HEIC 无 mdat。
func TestParseHeifExifTime_NoMdat(t *testing.T) {
	var metaBody bytes.Buffer
	metaBody.WriteByte(0)
	metaBody.Write([]byte{0, 0, 0})
	var iinfBody bytes.Buffer
	iinfBody.WriteByte(0)
	iinfBody.Write([]byte{0, 0, 0})
	binary.Write(&iinfBody, binary.BigEndian, uint16(1))
	iinfBody.Write(buildInfeV2(1, "Exif"))
	metaBody.Write(writeBoxBytes("iinf", iinfBody.Bytes()))
	var ilocBody bytes.Buffer
	ilocBody.WriteByte(0)
	ilocBody.Write([]byte{0, 0, 0})
	ilocBody.WriteByte(0x44)
	ilocBody.WriteByte(0x00)
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint16(0))
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint32(0))
	binary.Write(&ilocBody, binary.BigEndian, uint32(10))
	metaBody.Write(writeBoxBytes("iloc", ilocBody.Bytes()))
	var buf bytes.Buffer
	writeBox(&buf, "meta", metaBody.Bytes())
	// 无 mdat
	p := writeTempFile(t, buf.Bytes())
	if _, ok := parseHeifExifTime(p); ok {
		t.Error("无 mdat 应返回 false")
	}
}

// TestParseHeifExifTime_64BitExtSize 顶层 meta/mdat 使用 64 位扩展 size。
func TestParseHeifExifTime_64BitExtSize(t *testing.T) {
	le := binary.LittleEndian
	strData := []byte("2024:07:07 07:07:07\x00")
	strOff := uint32(8 + 2 + 12 + 4 + 2 + 12 + 4)
	ifd0Entry := make([]byte, 12)
	le.PutUint16(ifd0Entry[0:2], 0x8769)
	le.PutUint16(ifd0Entry[2:4], 4)
	le.PutUint32(ifd0Entry[4:8], 1)
	le.PutUint32(ifd0Entry[8:12], uint32(8+2+12+4))
	exifEntry := tiffAsciiEntry(le, 0x9003, "2024:07:07 07:07:07", strOff)
	tiff := buildTiff(t, "II", ifd0Entry, exifEntry, strData)
	extentLen := uint32(4 + len(tiff))

	// meta 子 box: iinf + iloc
	var iinfBody bytes.Buffer
	iinfBody.WriteByte(0)
	iinfBody.Write([]byte{0, 0, 0})
	binary.Write(&iinfBody, binary.BigEndian, uint16(1))
	iinfBody.Write(buildInfeV2(1, "Exif"))
	var ilocBody bytes.Buffer
	ilocBody.WriteByte(0)
	ilocBody.Write([]byte{0, 0, 0})
	ilocBody.WriteByte(0x44)
	ilocBody.WriteByte(0x00)
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint16(0))
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))
	binary.Write(&ilocBody, binary.BigEndian, uint32(0))
	binary.Write(&ilocBody, binary.BigEndian, extentLen)

	var metaBody bytes.Buffer
	metaBody.WriteByte(0)
	metaBody.Write([]byte{0, 0, 0})
	metaBody.Write(writeBoxBytes("iinf", iinfBody.Bytes()))
	metaBody.Write(writeBoxBytes("iloc", ilocBody.Bytes()))

	var buf bytes.Buffer
	// meta 用 64 位扩展 size
	metaPayload := metaBody.Bytes()
	binary.Write(&buf, binary.BigEndian, uint32(1)) // size==1
	buf.WriteString("meta")
	binary.Write(&buf, binary.BigEndian, uint64(16+len(metaPayload)))
	buf.Write(metaPayload)

	// mdat 用 64 位扩展 size
	mdat := make([]byte, 4+len(tiff))
	binary.BigEndian.PutUint32(mdat[:4], 4)
	copy(mdat[4:], tiff)
	binary.Write(&buf, binary.BigEndian, uint32(1))
	buf.WriteString("mdat")
	binary.Write(&buf, binary.BigEndian, uint64(16+len(mdat)))
	buf.Write(mdat)

	p := writeTempFile(t, buf.Bytes())
	tm, ok := parseHeifExifTime(p)
	if !ok {
		t.Fatal("64 位扩展 size 的 HEIC 应解析成功")
	}
	want := time.Date(2024, 7, 7, 7, 7, 7, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}
