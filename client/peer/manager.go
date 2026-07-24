// Package peer 实现客户端的对端信息管理。
package peer

import (
	"sync"

	"github.com/FuryHu/netbridge/protocol"
)

// Manager 管理同房间内所有 peer 的信息，并发安全。
type Manager struct {
	peers    map[string]*protocol.PeerInfo // peerID → PeerInfo
	self     protocol.PeerInfo             // 自己的信息
	p2pRTT   map[string]int64               // peerID → P2P 直连实测 RTT（ms）
	peerSRTT map[string]int64               // peerID → 该 peer 自测上报的服务器 RTT（ms）
	mu       sync.RWMutex
}

// NewManager 创建 peer 管理器。
func NewManager() *Manager {
	return &Manager{
		peers:    make(map[string]*protocol.PeerInfo),
		p2pRTT:   make(map[string]int64),
		peerSRTT: make(map[string]int64),
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

// Remove 移除 peer，并清理其延迟记录。
func (m *Manager) Remove(peerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peers, peerID)
	delete(m.p2pRTT, peerID)
	delete(m.peerSRTT, peerID)
}

// SetVoiceStatus 更新指定 peer 的语音状态（开语音 / 开麦），peer 不存在返回 false。
// 由收到 PeerStatusPacket 时调用，仅改这两字段、不重建 PeerInfo。
func (m *Manager) SetVoiceStatus(peerID string, voiceOn, micOn bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[peerID]
	if !ok {
		return false
	}
	p.VoiceOn = voiceOn
	p.MicOn = micOn
	return true
}

// SetPeerP2PRTT 记录到指定 peer 的 P2P 直连实测 RTT（ms）。peer 不存在则忽略。
func (m *Manager) SetPeerP2PRTT(peerID string, ms int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.peers[peerID]; !ok {
		return
	}
	m.p2pRTT[peerID] = ms
}

// GetPeerP2PRTT 返回到指定 peer 的 P2P 实测 RTT，无记录返回 0。
func (m *Manager) GetPeerP2PRTT(peerID string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.p2pRTT[peerID]
}

// SetPeerServerRTT 记录某 peer 自测上报的服务器 RTT（ms）。peer 不存在返回 false。
func (m *Manager) SetPeerServerRTT(peerID string, ms int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.peers[peerID]; !ok {
		return false
	}
	m.peerSRTT[peerID] = ms
	return true
}

// GetPeerServerRTT 返回某 peer 自测上报的服务器 RTT，无记录返回 0。
func (m *Manager) GetPeerServerRTT(peerID string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.peerSRTT[peerID]
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

// Reset 清空所有 peer 与延迟记录。
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers = make(map[string]*protocol.PeerInfo)
	m.p2pRTT = make(map[string]int64)
	m.peerSRTT = make(map[string]int64)
	m.self = protocol.PeerInfo{}
}
