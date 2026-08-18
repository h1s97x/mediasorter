// ISO BMFF(MP4/HEIC)box 解析基础工具。
// 提供统一的 box 头读取(支持 32 位 size、64 位扩展 size 与 size==0 边界)
// 与按需 seek 跳过,避免对大文件把整个 box 负载读入内存,
// 与项目"流式 I/O / 常量内存"定位一致。
package core

import (
	"encoding/binary"
	"io"
	"os"
)

// boxHeader 描述一个 ISO BMFF box 的头部信息。
type boxHeader struct {
	typ     string // 4 字节类型(如 "moov"/"mvhd"/"meta")
	boxSize int64  // 整个 box 的总大小(含头部)
	dataOff int64  // 数据区起始的文件偏移(即跳过头部后的位置)
	hdrSize int64  // 头部大小:32 位 size 为 8,64 位扩展 size 为 16
}

// readBoxHeader 从当前文件偏移读取一个 box 的头部。
// 支持三种 size 边界:
//   - size==0: box 从当前位置一直延伸至文件末尾,boxSize 由文件长度计算得出。
//   - size==1: 紧跟 8 字节 64 位扩展 size,box 头共 16 字节。
//   - size>=8: 普通 32 位 size,box 头共 8 字节。
//
// 读取成功后,文件偏移位于数据区起点(dataOff);失败时返回 false。
// 边界校验:64 位扩展 size 最小 16,普通 size 最小 8。
func readBoxHeader(f *os.File) (boxHeader, bool) {
	var hdr [8]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return boxHeader{}, false
	}
	size := binary.BigEndian.Uint32(hdr[:4])
	typ := string(hdr[4:8])
	switch {
	case size == 0: // box 延伸至文件末尾
		pos, err := f.Seek(0, io.SeekCurrent) // 当前偏移 = 8 字节头结束
		if err != nil {
			return boxHeader{}, false
		}
		info, err := f.Stat()
		if err != nil {
			return boxHeader{}, false
		}
		return boxHeader{typ: typ, boxSize: info.Size() - pos, dataOff: pos, hdrSize: 8}, true
	case size == 1: // 64 位扩展 size
		var ext [8]byte
		if _, err := io.ReadFull(f, ext[:]); err != nil {
			return boxHeader{}, false
		}
		sz64 := binary.BigEndian.Uint64(ext[:])
		if sz64 < 16 { // 64 位扩展 size 最小为 16
			return boxHeader{}, false
		}
		pos, err := f.Seek(0, io.SeekCurrent) // 当前偏移 = 16 字节头结束
		if err != nil {
			return boxHeader{}, false
		}
		return boxHeader{typ: typ, boxSize: int64(sz64), dataOff: pos, hdrSize: 16}, true
	case size < 8: // 32 位 size 最小为 8
		return boxHeader{}, false
	}
	pos, err := f.Seek(0, io.SeekCurrent) // 当前偏移 = 8 字节头结束
	if err != nil {
		return boxHeader{}, false
	}
	return boxHeader{typ: typ, boxSize: int64(size), dataOff: pos, hdrSize: 8}, true
}

// skipToNextBox 跳过当前 box 的剩余负载,使文件偏移定位到下一个 box 的头部。
// 应在 readBoxHeader 返回后(文件偏移位于 dataOff)调用。
func skipToNextBox(f *os.File, bh boxHeader) bool {
	// 当前偏移 = dataOff,剩余负载 = boxSize - hdrSize
	_, err := f.Seek(int64(bh.boxSize-bh.hdrSize), io.SeekCurrent)
	return err == nil
}

// readBoxPayload 读取一个 box 的完整负载(不含头部)。
// 注意:对超大 box(moov/meta)不要使用本函数,应改用按需 seek 方案。
func readBoxPayload(f *os.File, bh boxHeader) ([]byte, bool) {
	payloadSize := bh.boxSize - bh.hdrSize
	if payloadSize <= 0 || payloadSize > 1<<31 { // 防御:超过 2GB 不整读
		return nil, false
	}
	payload := make([]byte, payloadSize)
	if _, err := io.ReadFull(f, payload); err != nil {
		return nil, false
	}
	return payload, true
}

// readBoxUint 从 box 内指定相对偏移读取固定宽度(1-8 字节)大端无符号整数。
// 供按需解析 mvhd 等 box 头字段使用。
func readBoxUint(f *os.File, off int64, width int) (int64, bool) {
	if width < 1 || width > 8 {
		return 0, false
	}
	var b [8]byte
	if _, err := f.ReadAt(b[:width], off); err != nil {
		return 0, false
	}
	var v int64
	for _, c := range b[:width] {
		v = v<<8 | int64(c)
	}
	return v, true
}
