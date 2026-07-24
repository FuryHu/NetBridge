// Package peer 定义 NetBridge 服务端管理的客户端节点。
package peer

import (
	"net"
	"sync"
	"time"
)

// Peer 代表一个已注册的客户端节点，保存其身份、网络端点和心跳状态。
type Peer struct {
	ID        string       // 客户端生成的唯一 ID（uuid）
	NickName  string       // 玩家昵称
	Addr      *net.UDPAddr // 公网端点（含 NAT 映射端口，用于打洞与回包）
	VirtualIP uint32       // 分配的虚拟 IP 主机号（如 2 -> 10.66.0.2）

	// 语音状态：由客户端通过 PeerStatusPacket 上报，广播给房间其他成员的信息卡展示。
	// 写入频率低（仅在切麦时），沿用 mu 保护。
	VoiceOn bool
	MicOn   bool

	mu       sync.RWMutex
	lastSeen time.Time
}

// SetVoiceStatus 更新该 peer 的语音状态（开语音 / 开麦），由 PeerStatusPacket 触发。
func (p *Peer) SetVoiceStatus(voiceOn, micOn bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.VoiceOn = voiceOn
	p.MicOn = micOn
}

// New 创建 Peer 实例，初始化心跳时间为当前时间。
func New(id, nickName string, addr *net.UDPAddr) *Peer {
	return &Peer{
		ID:       id,
		NickName: nickName,
		Addr:     addr,
		lastSeen: time.Now(),
	}
}

// Touch 刷新最后活跃时间（收到心跳时调用）。
func (p *Peer) Touch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastSeen = time.Now()
}

// LastSeen 返回最后活跃时间。
func (p *Peer) LastSeen() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastSeen
}

// IsTimeout 判断是否超过超时阈值（HeartbeatInterval < 超时 < PeerTimeout）。
func (p *Peer) IsTimeout(timeout time.Duration) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return time.Since(p.lastSeen) > timeout
}

// SetVirtualIP 设置虚拟 IP 主机号。
func (p *Peer) SetVirtualIP(vip uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.VirtualIP = vip
}
