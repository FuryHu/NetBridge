//go:build !cgo || !windows

package tun

import (
	"fmt"
	"log/slog"
)

// StubAdapter 非 CGO 环境下的虚拟网卡桩实现。
type StubAdapter struct{}

// CreateWinTun 桩实现（始终返回错误，需 CGO 编译才可用）。
func CreateWinTun(name, vip string, log *slog.Logger) (*StubAdapter, error) {
	return nil, fmt.Errorf("WinTun 需要 CGO 和 MinGW-w64 编译，当前环境不支持。" +
		"请安装 MinGW-w64 并设置 CGO_ENABLED=1")
}

func (s *StubAdapter) ReadPacket() ([]byte, error) {
	return nil, fmt.Errorf("WinTun 不可用：需 CGO 编译")
}

func (s *StubAdapter) WritePacket(data []byte) error {
	return fmt.Errorf("WinTun 不可用：需 CGO 编译")
}

func (s *StubAdapter) SetVIP(vip string) error {
	return fmt.Errorf("WinTun 不可用：需 CGO 编译")
}

func (s *StubAdapter) Close() error { return nil }
func Destroy()                                  {}
