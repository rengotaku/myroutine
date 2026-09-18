//go:build !windows

package main

import (
	"syscall"
	"unsafe"
)

type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// termWidth は fd（通常は stdout）の端末幅を ioctl(TIOCGWINSZ) で取得する。
// 外部依存を足さず標準ライブラリの syscall だけで行う。
func termWidth(fd uintptr) (int, bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.Col == 0 {
		return 0, false
	}
	return int(ws.Col), true
}
