//go:build !windows && !darwin

package core

import "time"

// birthTime Linux/BSD 等:无原生创建时间语义,返回 false,回退到修改时间
func birthTime(path string) (time.Time, bool) {
	return time.Time{}, false
}
