// Fuzz 测试: 对 EXIF/HEIC/MP4 二进制解析做 fuzzing,确保任意损坏输入不 panic、不崩溃。
package core

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// fuzzBox 构造一个含头的 box 字节。
func fuzzBox(typ string, payload []byte) []byte {
	var b bytes.Buffer
	total := 8 + len(payload)
	binary.Write(&b, binary.BigEndian, uint32(total))
	b.WriteString(typ)
	b.Write(payload)
	return b.Bytes()
}

// fuzzMinimalMp4 构造最小 MP4(mvhd version0),不含 testing.T 依赖,供 fuzz 种子使用。
func fuzzMinimalMp4() []byte {
	var moov bytes.Buffer
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[4:8], uint32(mpegEpochDelta+1700000000))
	binary.BigEndian.PutUint32(mvhd[8:12], uint32(mpegEpochDelta+1700000000))
	moov.Write(fuzzBox("mvhd", mvhd))
	moov.Write(fuzzBox("tkhd", make([]byte, 64)))
	var buf bytes.Buffer
	buf.Write(fuzzBox("moov", moov.Bytes()))
	buf.Write(fuzzBox("mdat", make([]byte, 32)))
	return buf.Bytes()
}

// fuzzMinimalHeic 构造最小 HEIC(不含 DateTimeOriginal),供 fuzz 种子使用。
func fuzzMinimalHeic() []byte {
	var metaBody bytes.Buffer
	metaBody.WriteByte(0)
	metaBody.Write([]byte{0, 0, 0})
	// iinf
	var iinfBody bytes.Buffer
	iinfBody.WriteByte(0)
	iinfBody.Write([]byte{0, 0, 0})
	binary.Write(&iinfBody, binary.BigEndian, uint16(1))
	var infeBody bytes.Buffer
	infeBody.WriteByte(2)
	infeBody.Write([]byte{0, 0, 0})
	binary.Write(&infeBody, binary.BigEndian, uint16(1))
	binary.Write(&infeBody, binary.BigEndian, uint16(0))
	binary.Write(&infeBody, binary.BigEndian, uint16(0))
	binary.Write(&infeBody, binary.BigEndian, uint16(0))
	infeBody.WriteString("Exif")
	infeBody.WriteString("")
	iinfBody.Write(fuzzBox("infe", infeBody.Bytes()))
	metaBody.Write(fuzzBox("iinf", iinfBody.Bytes()))
	// iloc
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
	binary.Write(&ilocBody, binary.BigEndian, uint32(64))
	metaBody.Write(fuzzBox("iloc", ilocBody.Bytes()))

	var buf bytes.Buffer
	buf.Write(fuzzBox("meta", metaBody.Bytes()))
	mdat := make([]byte, 64)
	binary.BigEndian.PutUint32(mdat[:4], 4)
	mdat[4] = 'I'
	mdat[5] = 'I'
	binary.LittleEndian.PutUint16(mdat[6:8], 42)
	binary.LittleEndian.PutUint32(mdat[8:12], 8)
	binary.LittleEndian.PutUint16(mdat[12:14], 0)
	buf.Write(fuzzBox("mdat", mdat))
	return buf.Bytes()
}

// FuzzParseJpegExifTime 对 JPEG EXIF 解析做 fuzz。
// 目标: 任意输入不应 panic(应优雅返回 false)。
func FuzzParseJpegExifTime(f *testing.F) {
	// 种子: 合法 JPEG+EXIF
	f.Add(makeJpgWithExif("2023:01:01 00:00:00"))
	// 种子: 空 JPEG
	f.Add([]byte{0xff, 0xd8, 0xff, 0xd9})
	// 种子: 无 EXIF 的 APP1
	f.Add([]byte{0xff, 0xd8, 0xff, 0xe1, 0x00, 0x0a, 'J', 'F', 'I', 'F', 0x00, 0xff, 0xd9})
	// 种子: 截断数据
	f.Add([]byte{0xff, 0xd8})
	f.Add([]byte{0xff})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		p := filepath.Join(dir, "fuzz.jpg")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// 不 panic 即通过;返回值不重要
		parseJpegExifTime(p)
	})
}

// FuzzParseTiffDateTime 对 TIFF 结构解析做 fuzz。
func FuzzParseTiffDateTime(f *testing.F) {
	f.Add([]byte{'I', 'I', 42, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0}) // 最小合法 TIFF
	f.Add([]byte{'M', 'M', 42, 0, 8, 0, 0, 0})
	f.Add([]byte{})
	f.Add([]byte{'I', 'I'})
	f.Add([]byte{0xff, 0xfe, 0xfd})

	f.Fuzz(func(t *testing.T, data []byte) {
		parseTiffDateTime(data) // 不 panic 即通过
	})
}

// FuzzParseMp4CreationTime 对 MP4 box 解析做 fuzz。
func FuzzParseMp4CreationTime(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x00, 0x18, 'm', 'o', 'o', 'v', 0x00, 0x00, 0x00, 0x00}) // 含 moov
	f.Add(fuzzMinimalMp4())
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x00, 0x00, 0x08, 'f', 't', 'y', 'p'})

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		p := filepath.Join(dir, "fuzz.mp4")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		parseMp4CreationTime(p) // 不 panic 即通过
	})
}

// FuzzParseHeifExifTime 对 HEIC box 解析做 fuzz。
func FuzzParseHeifExifTime(f *testing.F) {
	f.Add(fuzzMinimalHeic())
	f.Add([]byte{0x00, 0x00, 0x00, 0x10, 'm', 'e', 't', 'a'})
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 'm', 'd', 'a', 't'})

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		p := filepath.Join(dir, "fuzz.heic")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		parseHeifExifTime(p) // 不 panic 即通过
	})
}
