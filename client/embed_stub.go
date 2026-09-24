//go:build !darwin && (!cgo || !windows)

package main

import "fmt"

// extractWintunDLL 桩实现（需 CGO + MinGW-w64 编译才可用）。
// darwin 显式排除：macOS 用内核自带的 utun，不涉及 wintun.dll。
func extractWintunDLL() error {
	return fmt.Errorf("WinTun 需要 CGO 和 MinGW-w64 编译。" +
		"请安装 MinGW-w64，下载 wintun.dll 放入 client/assets/，设置 CGO_ENABLED=1 后重新编译")
}
