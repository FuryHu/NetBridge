//go:build !windows && !darwin

package tun

import (
	"fmt"
	"log/slog"
	"runtime"
)

// Create 在既不是 Windows 也不是 macOS 的平台上兜底。
// 客户端目前只支持这两端；服务端不依赖本包。
func Create(name, vip string, log *slog.Logger) (NetAdapter, error) {
	return nil, fmt.Errorf("当前平台（%s）不支持创建虚拟网卡", runtime.GOOS)
}
