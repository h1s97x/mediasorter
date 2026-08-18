//go:build windows

package core

import (
	"syscall"
	"time"
)

// birthTime 读取 Windows 文件创建时间(Filetime -> epoch 纳秒)
func birthTime(path string) (time.Time, bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return time.Time{}, false
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return time.Time{}, false
	}
	defer syscall.CloseHandle(h)
	var fi syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &fi); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, fi.CreationTime.Nanoseconds()).Local(), true
}
