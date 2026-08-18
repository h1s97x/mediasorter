package core

import "encoding/binary"

// ISO BMFF box 遍历工具。
//
// ISO BMFF(MP4/HEIF 等)容器由嵌套的 box 组成,每个 box 形如:
//
//	[size(4 字节,大端)][type(4 字节 ASCII)]...payload...
//
// 特殊边界:
//   - size==0: box 从当前位置一直延伸至容器(文件)末尾,payload 为剩余全部数据。
//   - size==1: 紧跟 8 字节 64 位大端真实大小(size64),box 头共 16 字节。
//   - size<8:  非法,头都不完整,视为损坏。
//
// free / wide / skip 等占位 box 无负载语义,但结构上仍是普通 box,由本遍历按
// 通用规则安全跳过;调用方通过 type 判断即可避免误当作目标 box。

// parseChildren 遍历一段容器数据内的子 box。
// 对每个 box 调用 visit(typ,payload);visit 返回 false 可提前终止遍历。
// 遍历规则:
//   - size==0: 剩余数据整体作为该 box 的 payload,遍历结束。
//   - size==1: 读 64 位扩展大小;size64<16 视为损坏终止。
//   - size<8:  视为损坏终止。
//   - box 越界(size 超出 data 范围)视为损坏终止。
func parseChildren(data []byte, visit func(typ string, payload []byte) bool) {
	for off := 0; off+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		header := 8 // 默认 8 字节头
		switch {
		case size == 0:
			// box 延伸至容器末尾,payload 为剩余全部数据。
			visit(typ, data[off+8:])
			return
		case size == 1:
			if off+16 > len(data) {
				return
			}
			size64 := binary.BigEndian.Uint64(data[off+8 : off+16])
			if size64 < 16 {
				return
			}
			size = int(size64)
			header = 16 // 64 位扩展为 16 字节头
		case size < 8:
			return
		}
		if off+size > len(data) {
			return
		}
		if !visit(typ, data[off+header:off+size]) {
			return
		}
		off += size
	}
}
