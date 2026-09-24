//go:build windows

package tun

import "log/slog"

// Create 是平台无关的网卡工厂（见 adapter.go 的 NetAdapter）。
// Windows 走 wintun；非 CGO 构建下由 tun_stub.go 的桩实现兜底并返回错误。
func Create(name, vip string, log *slog.Logger) (NetAdapter, error) {
	return CreateWinTun(name, vip, log)
}
