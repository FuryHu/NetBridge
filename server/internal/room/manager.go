package room

import (
	"net"
	"sync"
	"time"

	"github.com/FuryHu/netbridge/server/internal/peer"
)

// Manager 管理所有房间的生命周期，并发安全。
type Manager struct {
	rooms map[string]*Room // roomID → Room
	mu    sync.RWMutex
}

// NewManager 创建房间管理器实例。
func NewManager() *Manager {
	return &Manager{
		rooms: make(map[string]*Room),
	}
}

// GetOrCreate 按房间号查找房间，不存在则自动创建。
func (m *Manager) GetOrCreate(roomID string) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.rooms[roomID]; ok {
		return r
	}
	r := NewRoom(roomID)
	m.rooms[roomID] = r
	return r
}

// Get 按房间号查找房间，不存在返回 nil。
func (m *Manager) Get(roomID string) *Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rooms[roomID]
}

// Remove 删除房间（空房间回收）。
func (m *Manager) Remove(roomID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rooms, roomID)
}

// ScanTimeout 扫描所有房间，移除超时 peer 并回收空房间。
// 返回所有被移除的 peer 及其所属房间号，调用方负责广播通知。
func (m *Manager) ScanTimeout(timeout time.Duration) []TimeoutResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	var results []TimeoutResult
	var emptyRooms []string

	for roomID, r := range m.rooms {
		timedOut := r.ScanTimeout(timeout)
		for _, p := range timedOut {
			results = append(results, TimeoutResult{RoomID: roomID, Peer: p})
		}
		if r.IsEmpty() {
			emptyRooms = append(emptyRooms, roomID)
		}
	}

	for _, roomID := range emptyRooms {
		delete(m.rooms, roomID)
	}

	return results
}

// TimeoutResult 超时扫描结果：被移除的 peer 及其原属房间。
type TimeoutResult struct {
	RoomID string
	Peer   *peer.Peer
}

// LookupByAddr 按公网端点反查所属房间。
// 用于 Relay 紧凑帧路由——帧头里没有 Room 字段，靠发送方地址匹配。
// 一个 peer 同一时刻只在一个房间，遍历代价对中转流量级别可接受。
func (m *Manager) LookupByAddr(addr *net.UDPAddr) *Room {
	if addr == nil {
		return nil
	}
	want := addr.String()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.rooms {
		if r.HasAddr(want) {
			return r
		}
	}
	return nil
}
