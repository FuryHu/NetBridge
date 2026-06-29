// Package peer 实现客户端的对端信息管理。
package peer

import (
	"sync"

	"github.com/FuryHu/netbridge/protocol"
)

// Manager 管理同房间内所有 peer 的信息，并发安全。
type Manager struct {
	peers map[string]*protocol.PeerInfo // peerID → PeerInfo
	self  protocol.PeerInfo             // 自己的信息
	mu    sync.RWMutex
}

// NewManager 创建 peer 管理器。
func NewManager() *Manager {
	return &Manager{
		peers: make(map[string]*protocol.PeerInfo),
	}
}

// SetSelf 设置自己的 peer 信息（JoinRoom 成功后由服务端下发）。
func (m *Manager) SetSelf(info protocol.PeerInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.self = info
}

// Self 返回自己的 peer 信息。
func (m *Manager) Self() protocol.PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.self
}

// Upsert 添加或更新 peer 信息。
func (m *Manager) Upsert(info protocol.PeerInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers[info.ID] = &info
}

// Remove 移除 peer。
func (m *Manager) Remove(peerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peers, peerID)
}

// Get 按 ID 查找 peer。
func (m *Manager) Get(peerID string) *protocol.PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.peers[peerID]
}

// GetByVIP 按虚拟 IP 查找 peer。
func (m *Manager) GetByVIP(vip uint32) *protocol.PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.peers {
		if p.VirtualIP == vip {
			return p
		}
	}
	return nil
}

// List 返回所有 peer 的快照。
func (m *Manager) List() []protocol.PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]protocol.PeerInfo, 0, len(m.peers))
	for _, p := range m.peers {
		list = append(list, *p)
	}
	return list
}

// Count 返回当前 peer 数量。
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.peers)
}

// Reset 清空所有 peer。
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers = make(map[string]*protocol.PeerInfo)
	m.self = protocol.PeerInfo{}
}
