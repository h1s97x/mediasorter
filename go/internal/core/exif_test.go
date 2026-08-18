package core

import (
	"encoding/binary"
	"path/filepath"
	"testing"
	"time"
)

// buildTiff 手工构造 TIFF 数据(供 parseTiffDateTime 单测)。
// byteOrder: "II" 小端 / "MM" 大端。
// ifd0Entry: IFD0 的条目列表(每 12 字节),由调用方传入已完成填充的条目。
// exifIfdEntries: ExifIFD 条目列表(可空)。
// strData: 追加到 TIFF 末尾的字符串数据(可多个,依次排列),调用方需自行计算各段偏移。
func buildTiff(t *testing.T, byteOrder string, ifd0Entry []byte, exifIfdEntries []byte, strData ...[]byte) []byte {
	t.Helper()
	var order binary.ByteOrder
	if byteOrder == "II" {
		order = binary.LittleEndian
	} else {
		order = binary.BigEndian
	}

	// IFD0 条目数
	n0 := 0
	if len(ifd0Entry) > 0 {
		n0 = len(ifd0Entry) / 12
	}
	// ExifIFD 条目数
	nex := 0
	if len(exifIfdEntries) > 0 {
		nex = len(exifIfdEntries) / 12
	}

	// 布局: TIFF 头(8) + IFD0 + next(4) + ExifIFD + next(4) + 字符串
	exifIfdOff := 8 + 2 + n0*12 + 4
	strOff := exifIfdOff + 2 + nex*12 + 4

	// 计算所有字符串段总长
	totalStrLen := 0
	for _, s := range strData {
		totalStrLen += len(s)
	}

	tiff := make([]byte, strOff+totalStrLen)
	copy(tiff[0:2], byteOrder)
	order.PutUint16(tiff[2:4], 42)
	order.PutUint32(tiff[4:8], 8) // IFD0 @ offset 8

	// IFD0
	ifd0Pos := 8
	order.PutUint16(tiff[ifd0Pos:ifd0Pos+2], uint16(n0))
	if n0 > 0 {
		copy(tiff[ifd0Pos+2:], ifd0Entry)
	}
	// next IFD 指针
	nextIfdPos := ifd0Pos + 2 + n0*12
	order.PutUint32(tiff[nextIfdPos:nextIfdPos+4], uint32(exifIfdOff))

	// ExifIFD
	order.PutUint16(tiff[exifIfdOff:exifIfdOff+2], uint16(nex))
	if nex > 0 {
		copy(tiff[exifIfdOff+2:], exifIfdEntries)
	}
	nextExifPos := exifIfdOff + 2 + nex*12
	order.PutUint32(tiff[nextExifPos:nextExifPos+4], 0) // 无下一个 IFD

	// 字符串
	pos := strOff
	for _, s := range strData {
		copy(tiff[pos:], s)
		pos += len(s)
	}
	return tiff
}

// tiffAsciiEntry 构造一个 ASCII 类型条目(12 字节)。
func tiffAsciiEntry(order binary.ByteOrder, tag uint16, value string, strOff uint32) []byte {
	e := make([]byte, 12)
	order.PutUint16(e[0:2], tag)
	order.PutUint16(e[2:4], 2) // ASCII
	order.PutUint32(e[4:8], uint32(len(value)+1))
	order.PutUint32(e[8:12], strOff)
	return e
}

// makeJpegBytes 构造 JPEG 二进制:可自定义 APP1 内容或跳过 EXIF。
// app1 nil 时不插入 APP1 段。
func makeJpegBytes(app1 []byte) []byte {
	var b []byte
	b = append(b, 0xFF, 0xD8) // SOI
	if app1 != nil {
		segLen := 2 + len(app1)
		b = append(b, 0xFF, 0xE1, byte(segLen>>8), byte(segLen))
		b = append(b, app1...)
	}
	b = append(b, 0xFF, 0xD9) // EOI
	return b
}

// ===== parseJpegExifTime 测试 =====

// TestParseJpegExifTime_ValidDateTimeOriginal 正常 DateTimeOriginal(小端 II)。
func TestParseJpegExifTime_ValidDateTimeOriginal(t *testing.T) {
	le := binary.LittleEndian
	strData := []byte("2021:06:15 08:30:00\x00")
	strOff := uint32(8 + 2 + 12 + 4 + 2 + 12 + 4) // 计算字符串偏移
	ifd0Entry := make([]byte, 12)
	le.PutUint16(ifd0Entry[0:2], 0x8769) // ExifIFDPointer
	le.PutUint16(ifd0Entry[2:4], 4)
	le.PutUint32(ifd0Entry[4:8], 1)
	le.PutUint32(ifd0Entry[8:12], uint32(8+2+12+4)) // ExifIFD 偏移

	exifEntry := tiffAsciiEntry(le, 0x9003, "2021:06:15 08:30:00", strOff)
	tiff := buildTiff(t, "II", ifd0Entry, exifEntry, strData)

	app1 := append([]byte("Exif\x00\x00"), tiff...)
	data := makeJpegBytes(app1)
	p := writeTempFile(t, data)

	tm, ok := parseJpegExifTime(p)
	if !ok {
		t.Fatal("期望解析成功")
	}
	want := time.Date(2021, 6, 15, 8, 30, 0, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseJpegExifTime_BigEndian 大端 MM TIFF。
func TestParseJpegExifTime_BigEndian(t *testing.T) {
	be := binary.BigEndian
	strData := []byte("2022:03:04 12:00:00\x00")
	strOff := uint32(8 + 2 + 12 + 4 + 2 + 12 + 4)
	ifd0Entry := make([]byte, 12)
	be.PutUint16(ifd0Entry[0:2], 0x8769)
	be.PutUint16(ifd0Entry[2:4], 4)
	be.PutUint32(ifd0Entry[4:8], 1)
	be.PutUint32(ifd0Entry[8:12], uint32(8+2+12+4))

	exifEntry := tiffAsciiEntry(be, 0x9003, "2022:03:04 12:00:00", strOff)
	tiff := buildTiff(t, "MM", ifd0Entry, exifEntry, strData)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	data := makeJpegBytes(app1)
	p := writeTempFile(t, data)

	tm, ok := parseJpegExifTime(p)
	if !ok {
		t.Fatal("期望解析成功(大端)")
	}
	want := time.Date(2022, 3, 4, 12, 0, 0, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseJpegExifTime_NoExif 无 EXIF 的 JPEG(仅 SOI+EOI)。
func TestParseJpegExifTime_NoExif(t *testing.T) {
	data := []byte{0xFF, 0xD8, 0xFF, 0xD9}
	p := writeTempFile(t, data)
	if _, ok := parseJpegExifTime(p); ok {
		t.Error("无 EXIF 应返回 false")
	}
}

// TestParseJpegExifTime_NotJpeg 非 JPEG 文件(头不是 FFD8)。
func TestParseJpegExifTime_NotJpeg(t *testing.T) {
	data := []byte("GIF89a......")
	p := writeTempFile(t, data)
	if _, ok := parseJpegExifTime(p); ok {
		t.Error("非 JPEG 应返回 false")
	}
}

// TestParseJpegExifTime_CorruptMarker 文件损坏:marker 前不是 0xFF。
func TestParseJpegExifTime_CorruptMarker(t *testing.T) {
	// SOI 后跟一个非 0xFF 的字节,应视为损坏
	data := []byte{0xFF, 0xD8, 0x00, 0x11, 0x22}
	p := writeTempFile(t, data)
	if _, ok := parseJpegExifTime(p); ok {
		t.Error("损坏 marker 应返回 false")
	}
}

// TestParseJpegExifTime_NoDataAfterSOI SOI 后直接 EOF。
func TestParseJpegExifTime_NoDataAfterSOI(t *testing.T) {
	data := []byte{0xFF, 0xD8}
	p := writeTempFile(t, data)
	if _, ok := parseJpegExifTime(p); ok {
		t.Error("SOI 后无数据应返回 false")
	}
}

// TestParseJpegExifTime_SOSBeforeExif SOS 之后无 EXIF(图像数据)。
func TestParseJpegExifTime_SOSBeforeExif(t *testing.T) {
	// SOI + SOS(FFDA)+ 长度 + 图像数据
	data := []byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x04, 0x01, 0x02}
	p := writeTempFile(t, data)
	if _, ok := parseJpegExifTime(p); ok {
		t.Error("SOS 后无 EXIF 应返回 false")
	}
}

// TestParseJpegExifTime_DateTimeFallback IFD0 里用 DateTime(0x0132) 兜底。
func TestParseJpegExifTime_DateTimeFallback(t *testing.T) {
	le := binary.LittleEndian
	strData := []byte("2020:12:31 23:59:59\x00")
	strOff := uint32(8 + 2 + 12 + 4 + 2 + 0 + 4)
	ifd0Entry := tiffAsciiEntry(le, 0x0132, "2020:12:31 23:59:59", strOff)
	tiff := buildTiff(t, "II", ifd0Entry, nil, strData)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	data := makeJpegBytes(app1)
	p := writeTempFile(t, data)

	tm, ok := parseJpegExifTime(p)
	if !ok {
		t.Fatal("DateTime 兜底应解析成功")
	}
	want := time.Date(2020, 12, 31, 23, 59, 59, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseJpegExifTime_DateTimePreferred DateTimeOriginal 优先于 DateTime。
func TestParseJpegExifTime_DateTimePreferred(t *testing.T) {
	le := binary.LittleEndian
	strD1 := []byte("2019:01:01 10:00:00\x00") // DateTime (IFD0)
	strD2 := []byte("2018:02:02 11:11:11\x00") // DateTimeOriginal (ExifIFD)

	// IFD0 含 2 个条目(ExifIFDPointer + DateTime),ExifIFD 含 1 个条目(DateTimeOriginal)
	// 偏移: TIFF头(8) + IFD0count(2) + 2*12 + next(4) + ExifIFDcount(2) + 1*12 + next(4) = 56
	exifIfdOff := uint32(8 + 2 + 2*12 + 4)
	str1Off := uint32(8 + 2 + 2*12 + 4 + 2 + 1*12 + 4)
	str2Off := str1Off + uint32(len(strD1))

	// IFD0: ExifIFDPointer + DateTime 两个条目
	ptrEntry := make([]byte, 12)
	le.PutUint16(ptrEntry[0:2], 0x8769) // ExifIFDPointer
	le.PutUint16(ptrEntry[2:4], 4)
	le.PutUint32(ptrEntry[4:8], 1)
	le.PutUint32(ptrEntry[8:12], exifIfdOff)
	ifd0Entry := tiffAsciiEntry(le, 0x0132, "2019:01:01 10:00:00", str1Off)
	ifd0Bytes := append(ptrEntry, ifd0Entry...)

	// ExifIFD: DateTimeOriginal
	exifEntry := tiffAsciiEntry(le, 0x9003, "2018:02:02 11:11:11", str2Off)

	tiff := buildTiff(t, "II", ifd0Bytes, exifEntry, strD1, strD2)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	data := makeJpegBytes(app1)
	p := writeTempFile(t, data)

	tm, ok := parseJpegExifTime(p)
	if !ok {
		t.Fatal("期望解析成功")
	}
	want := time.Date(2018, 2, 2, 11, 11, 11, 0, time.Local) // 取 DateTimeOriginal
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseJpegExifTime_NonExifApp1 APP1 段存在但不是 Exif 标记。
func TestParseJpegExifTime_NonExifApp1(t *testing.T) {
	app1 := []byte("JFIF\x00........")
	data := makeJpegBytes(app1)
	p := writeTempFile(t, data)
	if _, ok := parseJpegExifTime(p); ok {
		t.Error("非 Exif 的 APP1 应返回 false")
	}
}

// TestParseJpegExifTime_MissingFile 文件不存在。
func TestParseJpegExifTime_MissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nonexistent.jpg")
	if _, ok := parseJpegExifTime(p); ok {
		t.Error("文件不存在应返回 false")
	}
}

// ===== parseTiffDateTime 测试 =====

// TestParseTiffDateTime_TooShort 数据过短。
func TestParseTiffDateTime_TooShort(t *testing.T) {
	if _, ok := parseTiffDateTime([]byte{1, 2, 3}); ok {
		t.Error("过短数据应返回 false")
	}
}

// TestParseTiffDateTime_BadByteOrder 非法字节序。
func TestParseTiffDateTime_BadByteOrder(t *testing.T) {
	// 8 字节但字节序不是 II/MM
	b := []byte{'X', 'Y', 42, 0, 8, 0, 0, 0}
	if _, ok := parseTiffDateTime(b); ok {
		t.Error("非法字节序应返回 false")
	}
}

// TestParseTiffDateTime_NotTiffMagic 字节序对但 magic 不是 42。
func TestParseTiffDateTime_NotTiffMagic(t *testing.T) {
	b := []byte{'I', 'I', 43, 0, 8, 0, 0, 0}
	if _, ok := parseTiffDateTime(b); ok {
		t.Error("非 42 magic 应返回 false")
	}
}

// TestParseTiffDateTime_IFDOffsetOutOfRange IFD 偏移超出范围。
func TestParseTiffDateTime_IFDOffsetOutOfRange(t *testing.T) {
	b := []byte{'I', 'I', 42, 0, 200, 0, 0, 0} // IFD offset=200
	if _, ok := parseTiffDateTime(b); ok {
		t.Error("IFD 偏移越界应返回 false")
	}
}

// TestParseTiffDateTime_NoDateTime IFD 为空(无条目)。
func TestParseTiffDateTime_NoDateTime(t *testing.T) {
	b := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0} // IFD0 count=0
	if _, ok := parseTiffDateTime(b); ok {
		t.Error("无条目应返回 false")
	}
}

// TestParseTiffDateTime_EmptyDateTime 日期字符串为空。
func TestParseTiffDateTime_EmptyDateTime(t *testing.T) {
	le := binary.LittleEndian
	// DateTime 条目,字符串为空
	e := tiffAsciiEntry(le, 0x0132, "", uint32(8+2+12+4))
	tiff := buildTiff(t, "II", e, nil, []byte{0})
	if _, ok := parseTiffDateTime(tiff); ok {
		t.Error("空日期应返回 false")
	}
}
