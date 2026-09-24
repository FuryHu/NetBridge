//go:build !windows && !darwin

package main

import (
	"fmt"
	"runtime"
)

// RestartAsAdmin 兜底实现：客户端只在 Windows / macOS 上有提权方案。
func RestartAsAdmin() error {
	return fmt.Errorf("当前平台（%s）不支持提权重启", runtime.GOOS)
}
