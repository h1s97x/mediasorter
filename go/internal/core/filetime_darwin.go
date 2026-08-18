//go:build darwin

package core

import (
	"syscall"
	"time"
)

// birthTime 读取 macOS 文件创建时间(Birthtimespec)
func birthTime(path string) (time.Time, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return time.Time{}, false
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec).Local(), true
}
