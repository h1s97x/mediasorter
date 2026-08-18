package core

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeBox 写入一个 box(含头),自动选择 32 位或 64 位扩展 size。
func writeBox(b *bytes.Buffer, typ string, payload []byte) {
	total := 8 + len(payload)
	if total <= 0xFFFFFFFF {
		binary.Write(b, binary.BigEndian, uint32(total))
		b.WriteString(typ)
		b.Write(payload)
	} else {
		// 64 位扩展 size
		binary.Write(b, binary.BigEndian, uint32(1))
		b.WriteString(typ)
		binary.Write(b, binary.BigEndian, uint64(16+len(payload)))
		b.Write(payload)
	}
}

// writeFullBox 写入 full box 形式的子 box(如 mvhd/iinf/iloc)
func writeFullBox(b *bytes.Buffer, typ string, version byte, flags uint32, payload []byte) {
	var body bytes.Buffer
	body.WriteByte(version)
	var fl [3]byte
	fl[0] = byte(flags >> 16)
	fl[1] = byte(flags >> 8)
	fl[2] = byte(flags)
	body.Write(fl[:])
	body.Write(payload)
	writeBox(b, typ, body.Bytes())
}

// makeMinimalMp4 生成一个最小 MP4: moov(内含 mvhd) + mdat。
// mvhdVersion 选择 0 或 1。
func makeMinimalMp4(t *testing.T, mvhdVersion byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var moov bytes.Buffer

	// mvhd full box
	var mvhdPayload []byte
	if mvhdVersion == 1 {
		mvhdPayload = make([]byte, 20)
		mvhdPayload[0] = 1 // version
		// creation_time 8 字节: 从 mpegEpochDelta 起算
		binary.BigEndian.PutUint64(mvhdPayload[4:12], uint64(mpegEpochDelta+1700000000))
		// modification_time 8 字节
		binary.BigEndian.PutUint64(mvhdPayload[12:20], uint64(mpegEpochDelta+1700000000))
		writeBox(&moov, "mvhd", mvhdPayload)
	} else {
		mvhdPayload = make([]byte, 16)
		mvhdPayload[0] = 0 // version
		binary.BigEndian.PutUint32(mvhdPayload[4:8], uint32(mpegEpochDelta+1700000000))
		binary.BigEndian.PutUint32(mvhdPayload[8:12], uint32(mpegEpochDelta+1700000000))
		writeBox(&moov, "mvhd", mvhdPayload)
	}
	// 再放一个大的 tkhd 模拟大文件场景(数据不真实但用于尺寸测试)
	tkhdPayload := make([]byte, 64)
	writeBox(&moov, "tkhd", tkhdPayload)

	writeBox(&buf, "moov", moov.Bytes())
	writeBox(&buf, "mdat", make([]byte, 32))
	return buf.Bytes()
}

// makeMinimalHeic 生成最小 HEIC: meta(内含 iinf/iloc) + mdat。
func makeMinimalHeic(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	var metaBody bytes.Buffer

	// meta 的 version/flags
	metaBody.WriteByte(0)
	metaBody.Write([]byte{0, 0, 0})

	// iinf full box
	var iinfBody bytes.Buffer
	iinfBody.WriteByte(0) // version
	iinfBody.Write([]byte{0, 0, 0})
	binary.Write(&iinfBody, binary.BigEndian, uint16(1)) // entry_count=1
	// infe full box: version=2
	var infeBody bytes.Buffer
	infeBody.WriteByte(2) // version
	infeBody.Write([]byte{0, 0, 0})
	binary.Write(&infeBody, binary.BigEndian, uint16(1)) // item_ID=1
	binary.Write(&infeBody, binary.BigEndian, uint16(0)) // protection_index
	binary.Write(&infeBody, binary.BigEndian, uint16(0)) // reserved
	binary.Write(&infeBody, binary.BigEndian, uint16(0)) // flags
	infeBody.WriteString("Exif")                         // item_type
	infeBody.WriteString("")                             // item_name(空)
	writeBox(&iinfBody, "infe", infeBody.Bytes())
	writeBox(&metaBody, "iinf", iinfBody.Bytes())

	// iloc full box: version=0
	var ilocBody bytes.Buffer
	ilocBody.WriteByte(0) // version
	ilocBody.Write([]byte{0, 0, 0})
	// offset_size=4, length_size=4, base_offset_size=0, index_size=0
	ilocBody.WriteByte(0x44)                              // offset_size=4, length_size=4
	ilocBody.WriteByte(0x00)                              // base_offset_size=0, index_size=0
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))  // item_count=1
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))  // item_ID=1
	binary.Write(&ilocBody, binary.BigEndian, uint16(0))  // data_reference_index
	binary.Write(&ilocBody, binary.BigEndian, uint16(1))  // extent_count=1
	binary.Write(&ilocBody, binary.BigEndian, uint32(0))  // extent_offset=0
	binary.Write(&ilocBody, binary.BigEndian, uint32(64)) // extent_length=64
	writeBox(&metaBody, "iloc", ilocBody.Bytes())

	writeBox(&buf, "meta", metaBody.Bytes())

	// mdat 数据区(64 字节)
	// EXIF 数据块: 前 4 字节 = tiff 偏移(4), 后跟 TIFF
	mdat := make([]byte, 64)
	binary.BigEndian.PutUint32(mdat[:4], 4)
	// 简单 TIFF: little-endian, IFD0 空
	tiff := mdat[4:]
	tiff[0] = 'I'
	tiff[1] = 'I'
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)  // IFD0 在 offset 8
	binary.LittleEndian.PutUint16(tiff[8:10], 0) // IFD0 entry count=0
	writeBox(&buf, "mdat", mdat)

	return buf.Bytes()
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.bin")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("写入临时文件失败: %v", err)
	}
	return p
}

// TestReadBoxHeader32Bit 验证 32 位 size 的 box 头解析
func TestReadBoxHeader32Bit(t *testing.T) {
	var buf bytes.Buffer
	payload := make([]byte, 24)
	writeBox(&buf, "ftyp", payload)

	f, err := os.Open(writeTempFile(t, buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	bh, ok := readBoxHeader(f)
	if !ok {
		t.Fatal("readBoxHeader 失败")
	}
	if bh.typ != "ftyp" {
		t.Errorf("typ = %q,期望 ftyp", bh.typ)
	}
	if bh.boxSize != 32 {
		t.Errorf("boxSize = %d,期望 32", bh.boxSize)
	}
	if bh.hdrSize != 8 {
		t.Errorf("hdrSize = %d,期望 8", bh.hdrSize)
	}
	if bh.dataOff != 8 {
		t.Errorf("dataOff = %d,期望 8", bh.dataOff)
	}
}

// TestReadBoxHeader64Bit 验证 64 位扩展 size 的 box 头解析
func TestReadBoxHeader64Bit(t *testing.T) {
	var buf bytes.Buffer
	// 手动构造 64 位扩展 box
	binary.Write(&buf, binary.BigEndian, uint32(1)) // size==1 表示 64 位扩展
	buf.WriteString("moov")
	binary.Write(&buf, binary.BigEndian, uint64(16+100)) // 16 头 + 100 负载
	buf.Write(make([]byte, 100))

	f, err := os.Open(writeTempFile(t, buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	bh, ok := readBoxHeader(f)
	if !ok {
		t.Fatal("readBoxHeader 失败")
	}
	if bh.typ != "moov" {
		t.Errorf("typ = %q,期望 moov", bh.typ)
	}
	if bh.boxSize != 116 {
		t.Errorf("boxSize = %d,期望 116", bh.boxSize)
	}
	if bh.hdrSize != 16 {
		t.Errorf("hdrSize = %d,期望 16", bh.hdrSize)
	}
	if bh.dataOff != 16 {
		t.Errorf("dataOff = %d,期望 16", bh.dataOff)
	}
}

// TestReadBoxHeaderSizeZero 验证 size==0 时 box 延伸至文件末尾。
func TestReadBoxHeaderSizeZero(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // size==0
	buf.WriteString("mdat")
	buf.Write(make([]byte, 100)) // mdat 负载,box 一直延伸到文件末尾

	f, err := os.Open(writeTempFile(t, buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	bh, ok := readBoxHeader(f)
	if !ok {
		t.Fatal("readBoxHeader(size==0) 失败")
	}
	if bh.typ != "mdat" {
		t.Errorf("typ = %q,期望 mdat", bh.typ)
	}
	// 文件总长 = 8(头) + 100(负载) = 108,dataOff = 8
	// box 延伸至末尾,boxSize = 108 - 8 = 100
	if bh.boxSize != 100 {
		t.Errorf("boxSize = %d,期望 100", bh.boxSize)
	}
	if bh.hdrSize != 8 {
		t.Errorf("hdrSize = %d,期望 8", bh.hdrSize)
	}
}

// TestReadBoxHeaderInvalidSize 验证 size 边界(32 位 size<8,64 位 size<16)
func TestReadBoxHeaderInvalidSize(t *testing.T) {
	// size = 1(64 位),但 sz64 < 16
	{
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(1))
		buf.WriteString("free")
		binary.Write(&buf, binary.BigEndian, uint64(8)) // < 16
		f, _ := os.Open(writeTempFile(t, buf.Bytes()))
		defer f.Close()
		if _, ok := readBoxHeader(f); ok {
			t.Error("size64<16 应返回 false")
		}
	}
	// size = 4(32 位,但 < 8)
	{
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(4))
		buf.WriteString("free")
		f, _ := os.Open(writeTempFile(t, buf.Bytes()))
		defer f.Close()
		if _, ok := readBoxHeader(f); ok {
			t.Error("size<8 应返回 false")
		}
	}
}

// TestParseMp4CreationTime 验证按需 seek 解析 MP4 创建时间
func TestParseMp4CreationTime(t *testing.T) {
	for _, ver := range []byte{0, 1} {
		data := makeMinimalMp4(t, ver)
		p := writeTempFile(t, data)
		tm, ok := parseMp4CreationTime(p)
		if !ok {
			t.Fatalf("version %d: parseMp4CreationTime 失败", ver)
		}
		want := time.Unix(1700000000, 0).Local()
		if !tm.Equal(want) {
			t.Errorf("version %d: got %v, want %v", ver, tm, want)
		}
	}
}

// TestParseMp4NoMoov 验证无 moov 的 MP4 返回失败
func TestParseMp4NoMoov(t *testing.T) {
	var buf bytes.Buffer
	writeBox(&buf, "ftyp", make([]byte, 8))
	writeBox(&buf, "mdat", make([]byte, 8))
	p := writeTempFile(t, buf.Bytes())
	if _, ok := parseMp4CreationTime(p); ok {
		t.Error("无 moov 应返回 false")
	}
}

// TestParseHeifExifTime 验证按需 seek 解析 HEIF EXIF 时间
func TestParseHeifExifTime(t *testing.T) {
	data := makeMinimalHeic(t)
	p := writeTempFile(t, data)
	// 目前最小 HEIC 中 TIFF 不含 DateTimeOriginal,返回 false 是预期
	// 但流程应正常执行不 panic。
	parseHeifExifTime(p)
	// 主要验证不 panic 且内部逻辑可运行;具体日期解析已在 exif.go 中覆盖
}

// TestFindMetaInfo 验证在 meta 内按需定位 iinf/iloc
func TestFindMetaInfo(t *testing.T) {
	data := makeMinimalHeic(t)
	p := writeTempFile(t, data)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// 遍历顶层找到 meta
	for {
		bh, ok := readBoxHeader(f)
		if !ok {
			break
		}
		if bh.typ == "meta" {
			id, iloc, ok := findMetaInfo(f, bh)
			if !ok {
				t.Fatal("findMetaInfo 失败")
			}
			if id != 1 {
				t.Errorf("itemID = %d,期望 1", id)
			}
			if len(iloc) == 0 {
				t.Error("iloc 为空")
			}
			// 验证 findExtent
			loc, ok := findExtent(iloc, id)
			if !ok {
				t.Error("findExtent 失败")
			}
			if loc.offset != 0 || loc.length != 64 {
				t.Errorf("extent = (%d,%d),期望 (0,64)", loc.offset, loc.length)
			}
			return
		}
		if !skipToNextBox(f, bh) {
			break
		}
	}
	t.Fatal("未找到 meta box")
}

// TestSkipToNextBox 验证 skipToNextBox 正确跳过 box 负载
func TestSkipToNextBox(t *testing.T) {
	var buf bytes.Buffer
	writeBox(&buf, "moov", make([]byte, 100))
	writeBox(&buf, "mdat", make([]byte, 50))
	writeBox(&buf, "free", make([]byte, 10))

	p := writeTempFile(t, buf.Bytes())
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// 跳过 moov
	bh, ok := readBoxHeader(f)
	if !ok || bh.typ != "moov" {
		t.Fatal("读取 moov 头失败")
	}
	if !skipToNextBox(f, bh) {
		t.Fatal("skipToNextBox 失败")
	}
	// 下一个应该是 mdat
	bh2, ok := readBoxHeader(f)
	if !ok || bh2.typ != "mdat" {
		t.Fatalf("跳过 moov 后应读到 mdat,got %q", bh2.typ)
	}
	if !skipToNextBox(f, bh2) {
		t.Fatal("skipToNextBox 失败")
	}
	// 下一个应该是 free
	bh3, ok := readBoxHeader(f)
	if !ok || bh3.typ != "free" {
		t.Fatalf("跳过 mdat 后应读到 free,got %q", bh3.typ)
	}
}

// TestReadBoxPayload 验证读取 box 负载
func TestReadBoxPayload(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello-world-payload")
	writeBox(&buf, "test", payload)

	f, _ := os.Open(writeTempFile(t, buf.Bytes()))
	defer f.Close()

	bh, ok := readBoxHeader(f)
	if !ok {
		t.Fatal("readBoxHeader 失败")
	}
	got, ok := readBoxPayload(f, bh)
	if !ok {
		t.Fatal("readBoxPayload 失败")
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload = %q,期望 %q", got, payload)
	}
}
