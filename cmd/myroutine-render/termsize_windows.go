//go:build windows

package main

// termWidth は Windows では未対応。呼び出し側は false を受けて既定幅にする。
func termWidth(fd uintptr) (int, bool) {
	return 0, false
}
