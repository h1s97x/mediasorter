package core

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"time"
)

// writeBoxBytes64 用 64 位扩展 size 写 box。
func writeBoxBytes64(typ string, payload []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(1))
	b.WriteString(typ)
	binary.Write(&b, binary.BigEndian, uint64(16+len(payload)))
	b.Write(payload)
	return b.Bytes()
}

// makeMp4WithBoxes 用给定顶层 box 列表构造 MP4 字节。
func makeMp4WithBoxes(t *testing.T, boxes ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, b := range boxes {
		buf.Write(b)
	}
	return buf.Bytes()
}

// TestParseMp4CreationTime_V0 验证 version0 的 creation_time。
func TestParseMp4CreationTime_V0(t *testing.T) {
	var moov bytes.Buffer
	// mvhd version0: [1][3][4B creation][4B modification]...
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[4:8], uint32(mpegEpochDelta+1600000000))
	binary.BigEndian.PutUint32(mvhd[8:12], uint32(mpegEpochDelta+1600000000))
	writeBox(&moov, "mvhd", mvhd)
	writeBox(&moov, "trak", make([]byte, 8))

	var buf bytes.Buffer
	writeBox(&buf, "moov", moov.Bytes())
	writeBox(&buf, "mdat", make([]byte, 16))

	p := writeTempFile(t, buf.Bytes())
	tm, ok := parseMp4CreationTime(p)
	if !ok {
		t.Fatal("version0 应解析成功")
	}
	want := time.Unix(1600000000, 0).Local()
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseMp4CreationTime_SkipFreeWide moov 内 free/wide 占位块被跳过。
func TestParseMp4CreationTime_SkipFreeWide(t *testing.T) {
	var moov bytes.Buffer
	// 在 mvhd 前放 free 与 wide 占位块
	writeBox(&moov, "free", make([]byte, 8))
	writeBox(&moov, "wide", make([]byte, 8))
	// mvhd
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[4:8], uint32(mpegEpochDelta+1650000000))
	binary.BigEndian.PutUint32(mvhd[8:12], uint32(mpegEpochDelta+1650000000))
	writeBox(&moov, "mvhd", mvhd)
	// mvhd 后再放 free
	writeBox(&moov, "free", make([]byte, 4))

	var buf bytes.Buffer
	writeBox(&buf, "moov", moov.Bytes())

	p := writeTempFile(t, buf.Bytes())
	tm, ok := parseMp4CreationTime(p)
	if !ok {
		t.Fatal("应跳过 free/wide 并解析成功")
	}
	want := time.Unix(1650000000, 0).Local()
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseMp4CreationTime_64BitMoov moov 用 64 位扩展 size。
func TestParseMp4CreationTime_64BitMoov(t *testing.T) {
	var moov bytes.Buffer
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[4:8], uint32(mpegEpochDelta+1700000000))
	binary.BigEndian.PutUint32(mvhd[8:12], uint32(mpegEpochDelta+1700000000))
	writeBox(&moov, "mvhd", mvhd)

	var buf bytes.Buffer
	buf.Write(writeBoxBytes64("moov", moov.Bytes()))
	writeBox(&buf, "mdat", make([]byte, 8))

	p := writeTempFile(t, buf.Bytes())
	tm, ok := parseMp4CreationTime(p)
	if !ok {
		t.Fatal("64 位 moov 应解析成功")
	}
	want := time.Unix(1700000000, 0).Local()
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseMp4CreationTime_NoMoov 找不到 moov。
func TestParseMp4CreationTime_NoMoov(t *testing.T) {
	var buf bytes.Buffer
	writeBox(&buf, "ftyp", make([]byte, 8))
	writeBox(&buf, "mdat", make([]byte, 8))
	p := writeTempFile(t, buf.Bytes())
	if _, ok := parseMp4CreationTime(p); ok {
		t.Error("无 moov 应返回 false")
	}
}

// TestParseMp4CreationTime_NoMvhd moov 内无 mvhd。
func TestParseMp4CreationTime_NoMvhd(t *testing.T) {
	var moov bytes.Buffer
	writeBox(&moov, "trak", make([]byte, 8))
	writeBox(&moov, "mdia", make([]byte, 8))
	var buf bytes.Buffer
	writeBox(&buf, "moov", moov.Bytes())
	p := writeTempFile(t, buf.Bytes())
	if _, ok := parseMp4CreationTime(p); ok {
		t.Error("moov 内无 mvhd 应返回 false")
	}
}

// TestParseMp4CreationTime_MvhdTooShort mvhd 数据区过短(<8)。
func TestParseMp4CreationTime_MvhdTooShort(t *testing.T) {
	var moov bytes.Buffer
	writeBox(&moov, "mvhd", make([]byte, 4)) // 数据区只有 4 字节
	var buf bytes.Buffer
	writeBox(&buf, "moov", moov.Bytes())
	p := writeTempFile(t, buf.Bytes())
	if _, ok := parseMp4CreationTime(p); ok {
		t.Error("mvhd 过短应返回 false")
	}
}

// TestParseMp4CreationTime_ChildOutOfRange 子 box 超出 moov 范围。
func TestParseMp4CreationTime_ChildOutOfRange(t *testing.T) {
	// 构造一个 moov,其中 mvhd 声称大小超过 moov 边界
	var moov bytes.Buffer
	// 手动写一个 mvhd size 很大
	var mvhdHeader bytes.Buffer
	binary.Write(&mvhdHeader, binary.BigEndian, uint32(0xFFFFFFF0)) // 超大 size
	mvhdHeader.WriteString("mvhd")
	moov.Write(mvhdHeader.Bytes())
	writeBox(&moov, "trak", make([]byte, 8))

	var buf bytes.Buffer
	writeBox(&buf, "moov", moov.Bytes())
	p := writeTempFile(t, buf.Bytes())
	if _, ok := parseMp4CreationTime(p); ok {
		t.Error("子 box 超界应返回 false")
	}
}

// TestParseMp4CreationTime_V1 验证 version1 的 8 字节 creation_time。
func TestParseMp4CreationTime_V1(t *testing.T) {
	var moov bytes.Buffer
	mvhd := make([]byte, 28)
	mvhd[0] = 1 // version 1
	binary.BigEndian.PutUint64(mvhd[4:12], uint64(mpegEpochDelta+1800000000))
	binary.BigEndian.PutUint64(mvhd[12:20], uint64(mpegEpochDelta+1800000000))
	writeBox(&moov, "mvhd", mvhd)
	var buf bytes.Buffer
	writeBox(&buf, "moov", moov.Bytes())
	p := writeTempFile(t, buf.Bytes())
	tm, ok := parseMp4CreationTime(p)
	if !ok {
		t.Fatal("version1 应解析成功")
	}
	want := time.Unix(1800000000, 0).Local()
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestParseMp4CreationTime_East8TimeZone 东八区换算:UTC 时间转本地。
func TestParseMp4CreationTime_East8TimeZone(t *testing.T) {
	var moov bytes.Buffer
	mvhd := make([]byte, 20)
	// creation_time = mpegEpochDelta + 1580000000 (UTC)
	binary.BigEndian.PutUint32(mvhd[4:8], uint32(mpegEpochDelta+1580000000))
	binary.BigEndian.PutUint32(mvhd[8:12], uint32(mpegEpochDelta+1580000000))
	writeBox(&moov, "mvhd", mvhd)
	var buf bytes.Buffer
	writeBox(&buf, "moov", moov.Bytes())
	p := writeTempFile(t, buf.Bytes())
	tm, ok := parseMp4CreationTime(p)
	if !ok {
		t.Fatal("应解析成功")
	}
	// 结果是 Unix 秒数转本地时区
	want := time.Unix(1580000000, 0).Local()
	if !tm.Equal(want) {
		t.Errorf("got %v, want %v", tm, want)
	}
}

// TestFindTopBox 验证 findTopBox 定位指定顶层 box。
func TestFindTopBox(t *testing.T) {
	var buf bytes.Buffer
	writeBox(&buf, "ftyp", make([]byte, 8))
	writeBox(&buf, "moov", make([]byte, 8))
	p := writeTempFile(t, buf.Bytes())
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	bh, ok := findTopBox(f, "moov")
	if !ok {
		t.Fatal("应找到 moov")
	}
	if bh.typ != "moov" {
		t.Errorf("typ=%q, want moov", bh.typ)
	}
}
