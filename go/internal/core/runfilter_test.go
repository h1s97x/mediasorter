package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunTimeFilter 端到端验证 Run 的录制日期筛选:
// has 模式只处理有 EXIF 的;none 模式只处理无 EXIF 的。
func TestRunTimeFilter(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "A.jpg"), makeJpgWithExif("2020:01:01 00:00:00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "B.jpg"), []byte{0xff, 0xd8, 0xff, 0xd9}, 0o600); err != nil {
		t.Fatal(err)
	}

	// has: 只处理有 EXIF 的 A
	dstHas := t.TempDir()
	if res := Run(Options{Src: src, Dst: dstHas, TimeFilter: "has", Dedupe: false}, nil); res.Processed != 1 {
		t.Fatalf("TimeFilter=has 期望处理 1 个,实际 %d", res.Processed)
	}

	// none: 只处理无 EXIF 的 B(靠 mtime 兜底)
	dstNone := t.TempDir()
	if res := Run(Options{Src: src, Dst: dstNone, TimeFilter: "none", Dedupe: false}, nil); res.Processed != 1 {
		t.Fatalf("TimeFilter=none 期望处理 1 个,实际 %d", res.Processed)
	}

	// 空 = 全部: 处理 2 个(B 无 EXIF 走 mtime)
	dstAll := t.TempDir()
	if res := Run(Options{Src: src, Dst: dstAll, TimeFilter: "", Dedupe: false}, nil); res.Processed != 2 {
		t.Fatalf("TimeFilter=空 期望处理 2 个,实际 %d", res.Processed)
	}
}

// makeJpgWithExif 构造一个含 DateTimeOriginal 的极小 JPEG
func makeJpgWithExif(dt string) []byte {
	dbytes := []byte(dt + "\x00")
	le := littleEndian{}
	// TIFF: II 42 IFD0@8
	// IFD0(1 entry: ExifIFDPointer 0x8769) -> 18 字节
	exifIfdOff := 8 + 18
	strOff := exifIfdOff + 18
	ifd0Entry := make([]byte, 12)
	le.PutUint16(ifd0Entry[0:], 0x8769)
	le.PutUint16(ifd0Entry[2:], 4)
	le.PutUint32(ifd0Entry[4:], 1)
	le.PutUint32(ifd0Entry[8:], uint32(exifIfdOff))
	ifd0Body := append([]byte{1, 0}, ifd0Entry...)
	ifd0Body = append(ifd0Body, 0, 0, 0, 0)
	exifEntry := make([]byte, 12)
	le.PutUint16(exifEntry[0:], 0x9003) // DateTimeOriginal
	le.PutUint16(exifEntry[2:], 2)      // ASCII
	le.PutUint32(exifEntry[4:], uint32(len(dbytes)))
	le.PutUint32(exifEntry[8:], uint32(strOff))
	exifIfd := append([]byte{1, 0}, exifEntry...)
	exifIfd = append(exifIfd, 0, 0, 0, 0)
	tiff := append([]byte{'I', 'I', 42, 0, 8, 0, 0, 0}, ifd0Body...)
	tiff = append(tiff, exifIfd...)
	tiff = append(tiff, dbytes...)
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	segLen := 2 + len(app1)
	seg := append([]byte{0xff, 0xe1, byte(segLen >> 8), byte(segLen)}, app1...)
	return append(append([]byte{0xff, 0xd8}, seg...), 0xff, 0xd9)
}

type littleEndian struct{}

func (littleEndian) PutUint16(b []byte, v uint16) { b[0] = byte(v); b[1] = byte(v >> 8) }
func (littleEndian) PutUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
