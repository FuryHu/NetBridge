// Package tun 封装虚拟网卡的生命周期与流量桥接。
//
// NetAdapter 是虚拟网卡的抽象接口，允许 WinTun 实现（需 CGO）和 Mock 实现并存。
package tun

// NetAdapter 虚拟网卡抽象接口。
// 需管理员权限创建，负责读写原始 IP 包（L3）。
type NetAdapter interface {
	// ReadPacket 从网卡读取一个 IP 包（阻塞），返回原始字节。
	ReadPacket() ([]byte, error)
	// WritePacket 向网卡写入一个 IP 包。
	WritePacket(data []byte) error
	// SetVIP 重新配置虚拟 IP / MTU / 防火墙规则。
	// 设计为幂等——切换房间时直接调用，无需销毁重建 adapter。
	SetVIP(vip string) error
	// Close 关闭网卡并释放资源。
	Close() error
}
