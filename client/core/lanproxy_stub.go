//go:build !darwin

package core

// LanProxy 仅在 darwin 上实现：Windows 的 wintun 能正常收发广播，
// 游戏发现协议原生可用，不需要代理中继。此桩类型保证 app.go 跨平台编译。
type LanProxy struct{}

// NewLanProxy 返回空实现的代理（非 darwin 平台无行为）。
func NewLanProxy(c *Client) *LanProxy { return &LanProxy{} }

// Start 无操作。
func (p *LanProxy) Start() {}

// Stop 无操作。
func (p *LanProxy) Stop() {}
