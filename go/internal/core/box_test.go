package core

import (
	"encoding/binary"
	"os"
	"testing"
	"time"
)

// mustOpenTestFile 创建并打开测试文件
func mustOpenTestFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建测试文件失败: %v", err)
	}
	return f
}

// mustWriteTestFile 从当前偏移写入内容,并重置到文件头
func mustWriteTestFile(t *testing.T, f *os.File, data []byte) {
	t.Helper()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek 失败: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("重置偏移失败: %v", err)
	}
}

// bigBox 构造一个 size+type 的 box 头
func testBox(typ string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[:4], uint32(8+len(payload)))
	copy(b[4:8], typ)
	copy(b[8:], payload)
	return b
}

// TestParseChildren_FreeWide 验证 free/wide/skip 占位 box 被安全跳过,
// 不干扰后续目标 box 的解析。
func TestParseChildren_FreeWide(t *testing.T) {
	var data []byte
	data = append(data, testBox("free", nil)...)
	data = append(data, testBox("wide", nil)...)
	data = append(data, testBox("skip", nil)...)
	data = append(data, testBox("mvhd", make([]byte, 12))...)

	var got []string
	parseChildren(data, func(typ string, _ []byte) bool {
		got = append(got, typ)
		return true
	})
	want := []string{"free", "wide", "skip", "mvhd"}
	if len(got) != len(want) {
		t.Fatalf("遍历 box 数量 = %d,期望 %d;got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个 box = %q,期望 %q", i, got[i], want[i])
		}
	}
}

// TestParseChildren_SizeZero 验证 size==0 时 box 延伸至末尾,payload 为剩余数据。
func TestParseChildren_SizeZero(t *testing.T) {
	// 构造: 一个普通 box + 一个 size==0 的占位 box(整段剩余)
	var data []byte
	data = append(data, testBox("ftyp", []byte("isom"))...)
	// size==0 box: 头 8 字节,size 字段为 0,type 为 "mdat"
	tail := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0, 1, 2, 3}
	sizeZero := make([]byte, 8+len(tail))
	binary.BigEndian.PutUint32(sizeZero[:4], 0) // size==0
	copy(sizeZero[4:8], "mdat")
	copy(sizeZero[8:], tail)
	data = append(data, sizeZero...)

	var mdatPayload []byte
	var sawMdat bool
	parseChildren(data, func(typ string, payload []byte) bool {
		if typ == "mdat" {
			sawMdat = true
			mdatPayload = append([]byte(nil), payload...)
		}
		return true
	})
	if !sawMdat {
		t.Fatal("未遍历到 size==0 的 mdat box")
	}
	if string(mdatPayload) != string(tail) {
		t.Errorf("size==0 payload = %v,期望 %v", mdatPayload, tail)
	}
}

// TestParseChildren_SizeOne 验证 size==1 时 64 位扩展大小被正确解析。
func TestParseChildren_SizeOne(t *testing.T) {
	// 构造一个 size==1 的 box,64 位大小 = 16 + payload
	payload := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	box := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint32(box[:4], 1) // size==1 标记
	copy(box[4:8], "uuid")
	binary.BigEndian.PutUint64(box[8:16], uint64(16+len(payload)))
	copy(box[16:], payload)

	var gotPayload []byte
	var saw bool
	parseChildren(box, func(typ string, p []byte) bool {
		if typ == "uuid" {
			saw = true
			gotPayload = append([]byte(nil), p...)
		}
		return true
	})
	if !saw {
		t.Fatal("未遍历到 size==1 的 uuid box")
	}
	if string(gotPayload) != string(payload) {
		t.Errorf("size==1 payload = %v,期望 %v", gotPayload, payload)
	}
}

// TestParseChildren_VisitStop 验证 visit 返回 false 可提前终止遍历。
func TestParseChildren_VisitStop(t *testing.T) {
	var data []byte
	data = append(data, testBox("free", nil)...)
	data = append(data, testBox("mvhd", make([]byte, 12))...)
	data = append(data, testBox("skip", nil)...)

	var got []string
	parseChildren(data, func(typ string, _ []byte) bool {
		got = append(got, typ)
		return typ != "mvhd" // 遇到 mvhd 后停止
	})
	want := []string{"free", "mvhd"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("提前终止遍历结果 = %v,期望 %v", got, want)
	}
}

// mvhd 构造一个 mvhd payload(version 0),creation_time 由 secs 指定。
func testMvhdPayload(secs uint32) []byte {
	p := make([]byte, 20)
	p[0] = 0 // version
	binary.BigEndian.PutUint32(p[4:8], secs)
	return p
}

// TestParseMvhd_WithPlaceholders 验证 moov 内含 free/wide 占位 box、
// size==0 与 size==1 边界时,parseMvhd 仍能正确定位并解析 mvhd。
func TestParseMvhd_WithPlaceholders(t *testing.T) {
	var moov []byte
	moov = append(moov, testBox("free", nil)...)              // 普通占位
	moov = append(moov, testBox("wide", []byte{0, 0, 0, 0})...) // 带负载占位

	secs := uint32(3000000000)
	mvhdPayload := testMvhdPayload(secs)

	// mvhd 使用 size==1(64 位扩展)形式
	mvhd := make([]byte, 16+len(mvhdPayload))
	binary.BigEndian.PutUint32(mvhd[:4], 1)
	copy(mvhd[4:8], "mvhd")
	binary.BigEndian.PutUint64(mvhd[8:16], uint64(16+len(mvhdPayload)))
	copy(mvhd[16:], mvhdPayload)
	moov = append(moov, mvhd...)

	// 末尾一个 size==0 的占位 box
	sizeZero := make([]byte, 8)
	binary.BigEndian.PutUint32(sizeZero[:4], 0)
	copy(sizeZero[4:8], "free")
	moov = append(moov, sizeZero...)

	tm, ok := parseMvhd(moov)
	if !ok {
		t.Fatal("parseMvhd 未能解析含占位/边界 box 的 moov")
	}
	want := time.Unix(int64(secs)-mpegEpochDelta, 0)
	if tm.Unix() != want.Unix() {
		t.Errorf("parseMvhd 时间 = %v,期望 %v", tm, want)
	}
}

// TestParseMvhd_NoMvhd 验证不含 mvhd 时返回 false,且不因 size==0 死循环。
func TestParseMvhd_NoMvhd(t *testing.T) {
	var moov []byte
	moov = append(moov, testBox("free", nil)...)
	// size==0 的占位 box
	sizeZero := make([]byte, 8)
	binary.BigEndian.PutUint32(sizeZero[:4], 0)
	copy(sizeZero[4:8], "skip")
	moov = append(moov, sizeZero...)

	if _, ok := parseMvhd(moov); ok {
		t.Fatal("不含 mvhd 时不应返回 ok=true")
	}
}

// TestFindBox_SizeZero 验证 findBox 对 size==0 的顶层 box 读取到文件末尾。
func TestFindBox_SizeZero(t *testing.T) {
	path := t.TempDir() + "/s0.mp4"
	f := mustOpenTestFile(t, path)
	defer f.Close()

	// 构造: ftyp + moov(内含 mvhd box) + size==0 的 mdat 延伸到末尾
	moovPayload := testBox("mvhd", testMvhdPayload(3000000000))
	content := append(testBox("ftyp", []byte("isom")), testBox("moov", moovPayload)...)
	mdat := make([]byte, 8)
	binary.BigEndian.PutUint32(mdat[:4], 0)
	copy(mdat[4:8], "mdat")
	content = append(content, mdat...)
	mustWriteTestFile(t, f, content)

	// 定位 moov(普通路径)
	moov := findBox(f, "moov")
	if moov == nil {
		t.Fatal("findBox 未找到 moov")
	}
	if tm, ok := parseMvhd(moov); !ok || tm.IsZero() {
		t.Fatal("moov 内解析 mvhd 失败")
	}
}
