// Package room 实现 NetBridge 服务端的房间管理。
//
// 每个房间持有一组 peer，按加入顺序分配虚拟 IP。
// 所有公开方法均为并发安全。
package room

import (
	"sync"
	"time"

	"github.com/FuryHu/netbridge/protocol"
	"github.com/FuryHu/netbridge/server/internal/peer"
)

// Room 代表一个虚拟房间，玩家通过相同的房间号进入同一房间。
//
// VIP 分配策略：每次 Join 时扫描 peers 已用 VIP 集合，挑出从 protocol.VIPStart
// 开始的最低空闲号。这样 peer 离开后 VIP 立刻可被复用，避免"重连不断递增"。
// 单房间 peer 数远小于 65535，线性扫描代价微不足道。
type Room struct {
	id    string
	peers map[string]*peer.Peer // peerID → Peer
	mu    sync.RWMutex
}

// NewRoom 创建房间实例。
func NewRoom(id string) *Room {
	return &Room{
		id:    id,
		peers: make(map[string]*peer.Peer),
	}
}

// ID 返回房间号。
func (r *Room) ID() string { return r.id }

// Join 将 peer 加入房间并分配虚拟 IP。
// 若 peer 已存在则直接返回当前 peer 列表（重连场景）。
func (r *Room) Join(p *peer.Peer) uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.peers[p.ID]; ok {
		// 重连：更新地址，保留原 VIP。
		existing.Addr = p.Addr
		existing.Touch()
		return existing.VirtualIP
	}

	vip := r.allocVIPLocked()
	p.SetVirtualIP(vip)
	r.peers[p.ID] = p
	return vip
}

// allocVIPLocked 在 r.mu 已持有的前提下，返回最低空闲 VIP 主机号。
// 调用方必须先锁 r.mu。
func (r *Room) allocVIPLocked() uint32 {
	used := make(map[uint32]struct{}, len(r.peers))
	for _, p := range r.peers {
		used[p.VirtualIP] = struct{}{}
	}
	for vip := uint32(protocol.VIPStart); vip < 0xFFFF; vip++ {
		if _, ok := used[vip]; !ok {
			return vip
		}
	}
	// 理论上单房间不会到 65535 个 peer——若真出现，返回 VIPStart 让最后加入的复用最低号；
	// 同房间出现 VIP 冲突由调用方在路由层兜底。
	return protocol.VIPStart
}

// Leave 将 peer 从房间移除，返回被移除的 peer（不存在返回 nil）。
func (r *Room) Leave(peerID string) *peer.Peer {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.peers[peerID]
	if ok {
		delete(r.peers, peerID)
	}
	return p
}

// GetPeer 按 ID 查找 peer，不存在返回 nil。
func (r *Room) GetPeer(peerID string) *peer.Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.peers[peerID]
}

// GetPeerByVIP 按虚拟 IP 查找 peer，不存在返回 nil（供 relay 中转查找）。
func (r *Room) GetPeerByVIP(vip uint32) *peer.Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.peers {
		if p.VirtualIP == vip {
			return p
		}
	}
	return nil
}

// HasAddr 检查房间内是否有 peer 公网端点匹配给定字符串（"host:port" 形式）。
func (r *Room) HasAddr(addrStr string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.peers {
		if p.Addr != nil && p.Addr.String() == addrStr {
			return true
		}
	}
	return false
}

// PeerList 返回当前房间内所有 peer 的快照（用于构建 RoomStatus）。
func (r *Room) PeerList() []*peer.Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]*peer.Peer, 0, len(r.peers))
	for _, p := range r.peers {
		list = append(list, p)
	}
	return list
}

// PeerCount 返回当前在线人数。
func (r *Room) PeerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.peers)
}

// IsEmpty 判断房间是否为空（用于自动回收）。
func (r *Room) IsEmpty() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.peers) == 0
}

// ScanTimeout 扫描房间内所有 peer，返回超时的 peer 列表并将其从房间移除。
// 调用方负责广播 PeerLeave 通知。
func (r *Room) ScanTimeout(timeout time.Duration) []*peer.Peer {
	r.mu.Lock()
	defer r.mu.Unlock()

	var timedOut []*peer.Peer
	for id, p := range r.peers {
		if p.IsTimeout(timeout) {
			delete(r.peers, id)
			timedOut = append(timedOut, p)
		}
	}
	return timedOut
}
