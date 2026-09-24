//go:build !windows && !darwin

package main

import (
	"fmt"
	"runtime"
)

// prepareTunRuntime 兜底实现：客户端只在 Windows / macOS 上有真实网卡实现。
func prepareTunRuntime() error {
	return fmt.Errorf("当前平台（%s）不支持虚拟网卡", runtime.GOOS)
}

// hideOnCloseEnabled 兜底平台没有托盘，关窗即退出。
func hideOnCloseEnabled() bool { return false }
