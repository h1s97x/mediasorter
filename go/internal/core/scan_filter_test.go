package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanFormatFilter 验证 Scan 的扩展名白名单过滤。
// Extensions 为空(=全部)时处理所有支持的媒体;非空时仅处理白名单内的扩展名。
func TestScanFormatFilter(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	mk := func(name, content string) {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk("photo.jpg", "A")
	mk("img.png", "B")
	mk("video.mp4", "C")
	mk("note.txt", "not media") // 非媒体,永远不应被扫到

	// 全部格式
	all := Scan(src, dst, nil)
	if len(all) != 3 {
		t.Fatalf("期望 3 个媒体文件,实际 %d: %v", len(all), all)
	}

	// 仅 jpg
	onlyJpg := Scan(src, dst, map[string]bool{".jpg": true})
	if len(onlyJpg) != 1 || onlyJpg[0] != filepath.Join(src, "photo.jpg") {
		t.Fatalf("期望仅 photo.jpg,实际 %v", onlyJpg)
	}

	// jpg + mp4
	jpgMp4 := Scan(src, dst, map[string]bool{".jpg": true, ".mp4": true})
	if len(jpgMp4) != 2 {
		t.Fatalf("期望 2 个,实际 %v", jpgMp4)
	}

	// 仅视频
	onlyVid := Scan(src, dst, map[string]bool{".mp4": true})
	if len(onlyVid) != 1 || onlyVid[0] != filepath.Join(src, "video.mp4") {
		t.Fatalf("期望仅 video.mp4,实际 %v", onlyVid)
	}

	// 未知扩展名白名单(无匹配,且非媒体 txt 也不应进入)
	none := Scan(src, dst, map[string]bool{".txt": true})
	if len(none) != 0 {
		t.Fatalf("期望 0 个,实际 %v", none)
	}
}

// TestScanWithProgress 验证 ScanWithProgress 在遍历过程中上报累计扫描数。
func TestScanWithProgress(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 建一个子目录,验证递归遍历
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := []string{"a.jpg", "b.png", filepath.Join("sub", "c.mp4")}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(src, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var saw []int
	got := ScanWithProgress(src, dst, nil, func(n int) { saw = append(saw, n) })
	if len(got) != 3 {
		t.Fatalf("期望 3 个媒体文件,实际 %d", len(got))
	}
	// 回调应逐次上报 1,2,3(递增)
	if len(saw) != 3 {
		t.Fatalf("期望回调被调 3 次,实际 %d 次: %v", len(saw), saw)
	}
	for i, n := range saw {
		if n != i+1 {
			t.Fatalf("期望第 %d 次回调上报 %d,实际 %d", i, i+1, n)
		}
	}
	// onScan 为 nil 不应 panic
	_ = ScanWithProgress(src, dst, nil, nil)
}
