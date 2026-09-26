// Package core 实现 NetBridge 客户端的主控逻辑。
//
// Client 是客户端的核心，管理以下生命周期：
//
//	disconnected → connecting → joined → punching → p2p_connected / relay_connected
package core

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FuryHu/netbridge/client/netconn"
	"github.com/FuryHu/netbridge/client/peer"
	"github.com/FuryHu/netbridge/protocol"
)

// State 客户端连接状态。
type State int

const (
	StateDisconnected State = iota
	StateConnecting
	StateJoined
	StatePunching
	StateP2P
	StateRelay
)

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "未连接"
	case StateConnecting:
		return "连接中"
	case StateJoined:
		return "已连接"
	case StatePunching:
		return "打洞中"
	case StateP2P:
		return "P2P直连"
	case StateRelay:
		return "服务器中转"
	default:
		return "unknown"
	}
}

// Client 客户端主控结构。
type Client struct {
	conn       *netconn.UDPConn
	serverAddr *net.UDPAddr
	cfg        Config
	state      State
	stateMu    sync.RWMutex

	peerMgr *peer.Manager

	// channels 按 peerID 记录到每个对端的通道。
	channels   map[string]netconn.Channel
	channelsMu sync.RWMutex

	// pendingPunches 打洞等待通知：peer 公网地址 → 成功信号。
	pendingPunches   map[string]chan struct{}
	pendingPunchesMu sync.Mutex

	// pongCh Pong 报文从 dispatch 走到 PingServer 的回传通道。
	// 同一个 UDP socket 同时供 dispatch 和 PingServer 用，必须由 dispatch 单读。
	pongCh chan protocol.PongPacket

	// serverRTT 本端到服务器的最近一次 RTT（ms），由 pongLoop 从心跳 Pong 计算。
	// 中转 peer 的延迟估算为我方 serverRTT 加对方 serverRTT，见 PeerMetricsPacket。
	serverRTT atomic.Int64
	// pongSeq 每次 pongLoop 收到 Pong 自增；PingServer 据其推进判断"新 Pong 到达"，
	// 不再直接读 pongCh，避免与心跳 pong 抢通道。
	pongSeq atomic.Uint64

	// lastEmittedLatency 上次推给前端的各 peer 延迟，用于增量去重（latencyEmitLoop）。
	lastEmittedLatency map[string]int64
	latencyMu          sync.Mutex

	// onLatencyUpdate 单个 peer 延迟变化回调（peerID, ms；ms=-1 表示未知或已离线）。
	onLatencyUpdate func(peerID string, ms int64)

	// dataHandler 收到对端数据时的回调（供 tun bridge 注入）。
	dataHandler func(srcVIP uint32, data []byte)

	// voiceHandler 收到对端语音帧时的回调（供前端播放模块注入）。
	// 与 dataHandler 独立：语音不进虚拟网卡，走单独的播放通路。
	voiceHandler func(srcVIP uint32, payload []byte)

	// videoHandler 收到对端完整视频帧（已重组）时的回调（供前端播放模块注入）。
	// 与 voiceHandler 同构：视频不进虚拟网卡。payload 为重组后的完整 H.264 帧。
	videoHandler func(srcVIP uint32, payload []byte, keyframe bool, ts uint64)

	// videoReassemblers 按 srcVIP 维护视频分片重组器。视频帧拆成多个 UDP 包，
	// 重组在 Go 侧完成（而非前端），避免每分片一次 Wails IPC。
	// 注意：UDPConn.Start 对每个包开独立 goroutine 调 dispatch，关键帧的数十个分片会并发
	// 到达，故 videoReassemblers 及重组器内部状态均由 videoMu 保护。
	videoReassemblers map[uint32]*protocol.VideoReassembler
	videoMu           sync.Mutex

	// chatHandler 收到聊天消息时的回调（供前端展示）。
	chatHandler func(nickName, msg string, ts int64)

	// logHandler 后端日志回调（供前端日志面板展示）。
	logHandler func(msg string)

	// 状态变更回调（供 Wails App 注入）。
	onStateChange func(State)
	onPeerUpdate  func([]protocol.PeerInfo)
	onSelfUpdate  func(protocol.PeerInfo)

	log *slog.Logger

	// 生命周期控制。
	ctx    context.Context
	cancel context.CancelFunc
}

// New 创建客户端实例。
func New(log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		cfg:                DefaultConfig(),
		peerMgr:            peer.NewManager(),
		channels:           make(map[string]netconn.Channel),
		pendingPunches:     make(map[string]chan struct{}),
		pongCh:             make(chan protocol.PongPacket, 4),
		lastEmittedLatency: make(map[string]int64),
		videoReassemblers:  make(map[uint32]*protocol.VideoReassembler),
		state:              StateDisconnected,
		log:                log,
		ctx:                ctx,
		cancel:             cancel,
	}
}

// SetOnStateChange 注册状态变更回调。
func (c *Client) SetOnStateChange(fn func(State)) { c.onStateChange = fn }

// SetOnPeerUpdate 注册 peer 列表更新回调。
func (c *Client) SetOnPeerUpdate(fn func([]protocol.PeerInfo)) { c.onPeerUpdate = fn }

// SetOnSelfUpdate 注册自身信息更新回调——RoomStatus 到达后被触发，
// 让前端尽快拿到 VIP / 公网端点，不再依赖 GetSelf 轮询。
func (c *Client) SetOnSelfUpdate(fn func(protocol.PeerInfo)) { c.onSelfUpdate = fn }

// SetDataHandler 注册对端数据回调（用于 tun bridge 写入网卡）。
func (c *Client) SetDataHandler(fn func(srcVIP uint32, data []byte)) { c.dataHandler = fn }

// SetVoiceHandler 注册对端语音帧回调（供前端播放模块注入）。
func (c *Client) SetVoiceHandler(fn func(srcVIP uint32, payload []byte)) { c.voiceHandler = fn }

// SetVideoHandler 注册对端视频帧回调（已重组的完整帧，供前端播放注入）。
// keyframe 标识该帧是否为关键帧，ts 为采集时间戳（微秒），喂给解码器维持单调时间戳。
func (c *Client) SetVideoHandler(fn func(srcVIP uint32, payload []byte, keyframe bool, ts uint64)) {
	c.videoHandler = fn
}

// SetChatHandler 注册聊天消息回调。
func (c *Client) SetChatHandler(fn func(nickName, msg string, ts int64)) { c.chatHandler = fn }

// SetLogHandler 注册日志回调，将关键事件推送到前端日志面板。
func (c *Client) SetLogHandler(fn func(msg string)) { c.logHandler = fn }

// SetOnLatencyUpdate 注册单个 peer 延迟变化回调（peerID, ms；-1 表示未知或离线）。
func (c *Client) SetOnLatencyUpdate(fn func(peerID string, ms int64)) { c.onLatencyUpdate = fn }

// clientLog 同时输出到 slog 和前端日志面板。
func (c *Client) clientLog(level, msg string, args ...any) {
	full := fmt.Sprintf(msg, args...)
	c.log.Info(full, "level", level)
	if c.logHandler != nil {
		c.logHandler(fmt.Sprintf("[%s] %s", level, full))
	}
}

// SendChat 发送聊天消息到房间（经由服务端广播）。
func (c *Client) SendChat(msg string) error {
	if c.serverAddr == nil {
		return fmt.Errorf("未连接服务器")
	}
	pkt := protocol.ChatPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypeChat,
			Room:   c.cfg.Room,
			PeerID: c.cfg.PeerID,
		},
		NickName:  c.cfg.NickName,
		Message:   msg,
		Timestamp: time.Now().UnixMilli(),
	}
	return c.conn.SendPacket(c.serverAddr, pkt)
}

// SendPeerStatus 把本地语音状态（开语音 / 开麦）上报给服务端，由服务端广播给房间其他成员。
// 仅在状态变化时调用，不随每帧语音。
func (c *Client) SendPeerStatus(voiceOn, micOn bool) error {
	if c.serverAddr == nil {
		return fmt.Errorf("未连接服务器")
	}
	pkt := protocol.PeerStatusPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypePeerStatus,
			Room:   c.cfg.Room,
			PeerID: c.cfg.PeerID,
		},
		VoiceOn: voiceOn,
		MicOn:   micOn,
	}
	return c.conn.SendPacket(c.serverAddr, pkt)
}

// State 返回当前状态。
func (c *Client) State() State {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state
}

func (c *Client) setState(s State) {
	c.stateMu.Lock()
	old := c.state
	c.state = s
	c.stateMu.Unlock()
	if old != s {
		c.log.Info("客户端状态变更", "from", old, "to", s)
		if c.onStateChange != nil {
			c.onStateChange(s)
		}
	}
}

// Connect 连接服务器，启动读循环和心跳。
func (c *Client) Connect(serverAddr string) error {
	addr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return fmt.Errorf("解析服务器地址失败: %w", err)
	}
	c.serverAddr = addr

	conn, err := netconn.NewUDPConn(func(msg string, args ...any) {
		c.log.Debug(fmt.Sprintf(msg, args...))
	})
	if err != nil {
		return fmt.Errorf("创建 UDP socket 失败: %w", err)
	}
	c.conn = conn

	c.setState(StateConnecting)

	// 启动持续读循环（使用 client 的 context）——握手 Pong 要靠它 dispatch 进 pongCh。
	c.conn.Start(c.ctx, c.dispatch)

	// pongLoop 作为 pongCh 的唯一消费者，持续算本端到服务器的 RTT。
	// 必须在 PingServer 之前启动：握手 Pong 靠它落入 serverRTT。
	go c.pongLoop()
	// latencyEmitLoop 周期把各 peer 延迟增量推给前端。
	go c.latencyEmitLoop()

	// 握手：发 Ping 等 Pong，确认服务器真实可达。
	// 只建本地 socket 不算"已连接"——服务器没开时 socket 照样建成功，
	// 会导致前端误判已连接、进入房间页却永远收不到 RoomStatus（VIP 不分配、网卡不创建）。
	if _, err := c.PingServer(); err != nil {
		c.failConnect()
		return fmt.Errorf("无法连接到服务器: %w", err)
	}

	// 握手成功后才启动心跳——失败路径不启动，避免 failConnect 后残留双心跳。
	go c.heartbeatLoop()

	c.log.Info("已连接服务器", "server", serverAddr, "local", conn.LocalAddr())
	return nil
}

// failConnect 清理一次失败的连接尝试，恢复到可重连的初始状态。
// 仅在 Connect 握手失败时调用：此时未进房间、未启心跳，只需停读循环、关 socket。
func (c *Client) failConnect() {
	c.cancel()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.serverAddr = nil
	c.setState(StateDisconnected)
	// 重建 context，供下次 Connect 使用。
	c.ctx, c.cancel = context.WithCancel(context.Background())
}

// JoinRoom 加入房间并等待 RoomStatus 响应。
func (c *Client) JoinRoom(room, nickName string) error {
	c.cfg.Room = room
	c.cfg.NickName = nickName

	pkt := protocol.JoinRoomPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypeJoinRoom,
			Room:   room,
			PeerID: c.cfg.PeerID,
		},
		Room:     room,
		NickName: nickName,
	}

	if err := c.conn.SendPacket(c.serverAddr, pkt); err != nil {
		return fmt.Errorf("发送 JoinRoom 失败: %w", err)
	}

	c.log.Info("已发送加入房间请求", "room", room, "name", nickName)
	c.setState(StateJoined)
	return nil
}

// LeaveRoom 退出房间并清理资源，同时通知服务端。
func (c *Client) LeaveRoom() {
	// 通知服务端自己退出。
	if c.serverAddr != nil && c.cfg.Room != "" {
		leavePkt := protocol.PeerLeavePacket{
			Packet: protocol.Packet{
				Type:   protocol.TypePeerLeave,
				Room:   c.cfg.Room,
				PeerID: c.cfg.PeerID,
			},
			PeerID: c.cfg.PeerID,
		}
		_ = c.conn.SendPacket(c.serverAddr, leavePkt)
	}

	// 清理打洞等待。
	c.pendingPunchesMu.Lock()
	for k, ch := range c.pendingPunches {
		close(ch)
		delete(c.pendingPunches, k)
	}
	c.pendingPunchesMu.Unlock()

	// 通道里的 P2P 实例持有 keepalive goroutine——逐个 Close 防泄漏。
	c.channelsMu.Lock()
	for _, ch := range c.channels {
		ch.Close()
	}
	c.channels = make(map[string]netconn.Channel)
	c.channelsMu.Unlock()

	c.peerMgr.Reset()
	c.latencyMu.Lock()
	c.lastEmittedLatency = make(map[string]int64)
	c.latencyMu.Unlock()
	c.cfg.Room = ""

	// 仍在线就保持 StateJoined 语义上的「已连接服务器、未进房」是 StateConnecting，
	// 但前端把 connecting 显示为「连接中…」反直觉。维持 Joined 也不准（已离房）。
	// 这里干脆按是否还有 socket 区分：有 socket → 已连接服务器；没有 → 未连接。
	if c.conn != nil {
		c.setState(StateJoined) // 复用：表示「已连服务器但当前不在房间」
		// 注意：再次 JoinRoom 时仍会重置为 StateJoined，无副作用。
	} else {
		c.setState(StateDisconnected)
	}

	// 通知前端清空 self / peers 视图。
	if c.onSelfUpdate != nil {
		c.onSelfUpdate(protocol.PeerInfo{})
	}

	c.log.Info("已退出房间")
}

// GetPeers 返回当前 peer 列表。
func (c *Client) GetPeers() []protocol.PeerInfo { return c.peerMgr.List() }

// GetPeerCount 返回在线人数（含自己）。
func (c *Client) GetPeerCount() int { return c.peerMgr.Count() + 1 }

// GetSelf 返回自己的 peer 信息。
func (c *Client) GetSelf() protocol.PeerInfo { return c.peerMgr.Self() }

// GetStatus 返回状态字符串。
func (c *Client) GetStatus() string { return c.State().String() }

// Disconnect 断开与服务器的连接（不通知服务端，直接关闭 socket）。
func (c *Client) Disconnect() {
	c.LeaveRoom()
	c.cancel()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.serverAddr = nil
	c.setState(StateDisconnected)

	// 创建新的 context 以便后续重新连接。
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.log.Info("已断开连接")
}

// GetPeerChannel 返回指定 peer 的通道类型（p2p/relay/none）。
func (c *Client) GetPeerChannel(peerID string) string {
	c.channelsMu.RLock()
	defer c.channelsMu.RUnlock()
	ch, ok := c.channels[peerID]
	if !ok {
		return "none"
	}
	switch ch.Type() {
	case netconn.ChannelP2P:
		return "p2p"
	case netconn.ChannelRelay:
		return "relay"
	default:
		return "none"
	}
}

// GetPeerChannelInfo 返回指定 peer 的通道详情：类型 + 是否 IPv6。
// p2p 通道下，addr 是真正用于直连的对端公网地址；relay/none 通道下 addr 为空。
func (c *Client) GetPeerChannelInfo(peerID string) (kind string, addr string, isV6 bool) {
	c.channelsMu.RLock()
	defer c.channelsMu.RUnlock()
	ch, ok := c.channels[peerID]
	if !ok {
		return "none", "", false
	}
	switch v := ch.(type) {
	case *netconn.P2PChannel:
		a := v.PeerAddr()
		s := ""
		if a != nil {
			s = a.String()
		}
		return "p2p", s, protocol.IsIPv6Addr(s)
	case *netconn.RelayChannel:
		return "relay", "", false
	default:
		return "none", "", false
	}
}

// channelForVIP 返回（或创建 Relay 兜底的）到指定 VIP 的通道。
//
// 早期出包（打洞还没完成）会落到 Relay 兜底；一旦 punchPeer 拿到 punch_reply，
// 下次调用会自动用到新建的 P2P 通道--map 替换是原子的，
// 旧 Relay 通道仅持有 conn 引用、无 goroutine，被替换后由 GC 回收，无泄漏。
//
// SendToPeer（游戏数据）与 SendVoice（语音）共用此选路逻辑：
// 语音与游戏数据享受同样的 P2P 优先 / Relay 回落，无需为语音单独建路。
func (c *Client) channelForVIP(dstVIP uint32) (netconn.Channel, error) {
	p := c.peerMgr.GetByVIP(dstVIP)
	if p == nil {
		return nil, fmt.Errorf("未知的目标 VIP: %d", dstVIP)
	}

	c.channelsMu.RLock()
	ch, ok := c.channels[p.ID]
	c.channelsMu.RUnlock()
	if ok {
		return ch, nil
	}

	// 无通道则创建 Relay 兜底。
	self := c.peerMgr.Self()
	relay := netconn.NewRelayChannel(c.conn, c.serverAddr, self.VirtualIP, dstVIP)
	c.channelsMu.Lock()
	// double-check：可能在加锁前别的 goroutine 已经建好通道（打洞 / 被动建立）。
	if existing, ok2 := c.channels[p.ID]; ok2 {
		c.channelsMu.Unlock()
		return existing, nil
	}
	c.channels[p.ID] = relay
	c.channelsMu.Unlock()
	c.notifyPeers()

	c.log.Debug("创建 Relay 通道", "peer", p.ID, "dstVIP", dstVIP)
	return relay, nil
}

// SendToPeer 向指定 peer 发送数据（自动选择通道：P2P > Relay）。
func (c *Client) SendToPeer(dstVIP uint32, data []byte) error {
	ch, err := c.channelForVIP(dstVIP)
	if err != nil {
		return err
	}
	return ch.Send(data)
}

// SendVoice 向指定 peer 发送一帧语音（payload 为 voice 子格式，见 protocol/voice.go）。
// 复用同样的选路，因此语音与游戏数据走同一条通道。
func (c *Client) SendVoice(dstVIP uint32, payload []byte) error {
	ch, err := c.channelForVIP(dstVIP)
	if err != nil {
		return err
	}
	return ch.SendTyped(protocol.FrameVoice, payload)
}

// SendVoiceToAll 向房间内所有其他 peer 广播一帧语音。
// 前端每帧只调一次本方法，由后端遍历 peer 列表分发，
// 避免 50pps × N 次跨 Wails IPC 调用。
func (c *Client) SendVoiceToAll(payload []byte) error {
	selfVIP := c.peerMgr.Self().VirtualIP
	var lastErr error
	for _, p := range c.peerMgr.List() {
		if p.VirtualIP == selfVIP {
			continue
		}
		if err := c.SendVoice(p.VirtualIP, payload); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// SendVideo 向指定 peer 发送一帧完整视频：按 MTU 切成多个 FrameVideo 分片逐包发出。
// P2P 闸门：仅走 P2P 直连通道，Relay 通道直接跳过--投屏不消耗服务器带宽。
// 无通道或非 P2P 时返回 nil（视为"该 peer 暂不可投屏"，非错误）。
func (c *Client) SendVideo(dstVIP uint32, codec, flags byte, frameID uint16, ts uint64, frame []byte) error {
	frags := protocol.EncodeVideoFragments(codec, flags, frameID, ts, frame, protocol.VideoMaxFragData)
	return c.sendVideoFrags(dstVIP, frags)
}

// sendVideoFrags 向指定 peer 发送已切好的分片（P2P 闸门）。供 SendVideo 与 SendVideoToAll 复用。
func (c *Client) sendVideoFrags(dstVIP uint32, frags [][]byte) error {
	ch, err := c.channelForVIP(dstVIP)
	if err != nil {
		return nil
	}
	if ch.Type() != netconn.ChannelP2P {
		return nil // P2P 闸门：relay 通道不投屏
	}
	for _, f := range frags {
		if err := ch.SendTyped(protocol.FrameVideo, f); err != nil {
			return err
		}
	}
	return nil
}

// SendVideoToAll 解析视频帧封装并广播给房间内所有 P2P 直连 peer。
// 与 SendVoiceToAll 同构：前端每帧只调一次本方法（payload 为 envelope），由后端遍历 peer 分发。
// 切分对所有 peer 完全相同，故只切一次复用，避免逐 peer 重复切分。Relay peer 被 P2P 闸门跳过。
func (c *Client) SendVideoToAll(payload []byte) error {
	codec, flags, frameID, ts, frame, err := protocol.DecodeVideoEnvelope(payload)
	if err != nil {
		return err
	}
	frags := protocol.EncodeVideoFragments(codec, flags, frameID, ts, frame, protocol.VideoMaxFragData)
	selfVIP := c.peerMgr.Self().VirtualIP
	var lastErr error
	for _, p := range c.peerMgr.List() {
		if p.VirtualIP == selfVIP {
			continue
		}
		if err := c.sendVideoFrags(p.VirtualIP, frags); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// PingServer 向服务器发送 Ping 并等待 Pong，返回 RTT（毫秒）。
//
// 不直接读 pongCh：pongLoop 是 pongCh 的唯一消费者，每收一个 Pong 就算出 serverRTT
// 并自增 pongSeq。这里发 Ping 后轮询 pongSeq 是否推进，推进即拿到一次有效 RTT；
// 心跳运行时若恰有在飞 Pong，会取到心跳的 RTT，对握手/测试场景足够。
func (c *Client) PingServer() (int64, error) {
	if c.conn == nil || c.serverAddr == nil {
		return 0, fmt.Errorf("未连接服务器")
	}
	before := c.pongSeq.Load()
	ping := protocol.NewPing(c.cfg.PeerID, time.Now().UnixMilli())
	if err := c.conn.SendPacket(c.serverAddr, ping); err != nil {
		return 0, fmt.Errorf("发送 Ping 失败: %w", err)
	}
	// 等 pongLoop 收到 Pong（pongSeq 推进）。心跳运行时若恰有在飞 Pong，会取到一次有效 RTT。
	deadline := time.Now().Add(5 * time.Second)
	for c.pongSeq.Load() <= before {
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("等待 Pong 超时")
		}
		select {
		case <-c.ctx.Done():
			return 0, fmt.Errorf("客户端已停止")
		case <-time.After(20 * time.Millisecond):
		}
	}
	return c.serverRTT.Load(), nil
}

// pongLoop 是 pongCh 的唯一消费者：每收到一个 Pong 就算出本端到服务器 RTT
// 并自增 pongSeq，供 PingServer 检测"新 Pong 到达"。心跳 Pong 也由此捕获，
// 使 serverRTT 在连接期间持续刷新。
func (c *Client) pongLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case pong := <-c.pongCh:
			rtt := time.Now().UnixMilli() - pong.Timestamp
			if rtt < 0 || rtt > 30000 {
				continue // 时钟回退或陈旧包，丢弃
			}
			c.serverRTT.Store(rtt)
			c.pongSeq.Add(1)
		}
	}
}

// sendPeerMetrics 把本端自测的服务器 RTT 上报给服务端，由服务端广播给房间其他成员，
// 供中转 peer 按"我方 SRTT + 对方 SRTT"估算延迟。每轮心跳顺带发一次，值未变也重发
// 以保证刷新与补丢。
func (c *Client) sendPeerMetrics() {
	if c.serverAddr == nil || c.cfg.Room == "" {
		return
	}
	rtt := c.serverRTT.Load()
	if rtt <= 0 {
		return
	}
	pkt := protocol.PeerMetricsPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypePeerMetrics,
			Room:   c.cfg.Room,
			PeerID: c.cfg.PeerID,
		},
		ServerRTT: rtt,
	}
	_ = c.conn.SendPacket(c.serverAddr, pkt)
}

// handlePeerMetrics 收到他人自测的服务器 RTT：记到该 peer 名下，供中转延迟估算。
func (c *Client) handlePeerMetrics(raw []byte) {
	var pkt protocol.PeerMetricsPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return
	}
	if pkt.PeerID == "" || pkt.PeerID == c.cfg.PeerID {
		return // 自己的回环，忽略
	}
	c.peerMgr.SetPeerServerRTT(pkt.PeerID, pkt.ServerRTT)
}

// updateChannelAddr 把 srcVIP 对应 peer 的 P2P 通道地址更新为实际来源（自愈）。
// 仅当该 peer 已有 P2P 通道且地址确实变化时才更新；Relay 通道不受影响
// （FrameP2P 直连帧到达但本地通道是 Relay 时，升级通道交给 createPassiveP2P 的打洞路径）。
func (c *Client) updateChannelAddr(remote *net.UDPAddr, srcVIP uint32) {
	p := c.peerMgr.GetByVIP(srcVIP)
	if p == nil {
		return
	}
	c.channelsMu.RLock()
	ch, ok := c.channels[p.ID]
	c.channelsMu.RUnlock()
	if !ok {
		return
	}
	p2p, isP2P := ch.(*netconn.P2PChannel)
	if !isP2P {
		return
	}
	if p2p.PeerAddr().String() != remote.String() {
		old := p2p.PeerAddr().String()
		p2p.SetPeerAddr(remote)
		c.clientLog("P2P", "通道地址更新(自愈) peer=%s %s -> %s", p.ID, old, remote.String())
	}
}

// handlePeerPing 收到 P2P 延迟探测：回填同一时间戳的 FramePong，沿来路 remote 直发。
// 走 remote 而非通道选路，确保即便本端到该 peer 的通道是 Relay（对端却直连成功）
// 也能在直连路径上回包。服务端 relay 不转发 FramePing/FramePong，故这两类帧只走直连。
func (c *Client) handlePeerPing(remote *net.UDPAddr, srcVIP uint32, payload []byte) {
	ts, ok := protocol.DecodePingTS(payload)
	if !ok {
		return
	}
	selfVIP := c.peerMgr.Self().VirtualIP
	if selfVIP == 0 {
		return
	}
	frame := protocol.EncodeFrame(protocol.FramePong, selfVIP, srcVIP, protocol.EncodePingTS(ts))
	_ = c.conn.SendRaw(remote, frame)
}

// handlePeerPong 收到 P2P 探测回包：据时间戳算 RTT，记到该 peer 名下。
func (c *Client) handlePeerPong(srcVIP uint32, payload []byte) {
	ts, ok := protocol.DecodePingTS(payload)
	if !ok {
		return
	}
	rtt := time.Now().UnixMilli() - ts
	if rtt < 0 || rtt > 10000 {
		return // 时钟跳变或陈旧，丢弃
	}
	p := c.peerMgr.GetByVIP(srcVIP)
	if p == nil {
		return
	}
	c.peerMgr.SetPeerP2PRTT(p.ID, rtt)
}

// GetPeerLatency 返回到指定 peer 的展示延迟（ms）：P2P 用实测 RTT，中转用双方 SRTT 之和。
// 未知（未测到或通道未建立）返回 -1。
func (c *Client) GetPeerLatency(peerID string) int64 {
	switch c.GetPeerChannel(peerID) {
	case "p2p":
		if rtt := c.peerMgr.GetPeerP2PRTT(peerID); rtt > 0 {
			return rtt
		}
	case "relay":
		srtt := c.serverRTT.Load()
		peerSRTT := c.peerMgr.GetPeerServerRTT(peerID)
		if srtt > 0 && peerSRTT > 0 {
			return srtt + peerSRTT
		}
	}
	return -1
}

// latencyEmitLoop 每秒把各 peer 当前延迟与上次推送值比对，变化的增量推给前端。
// 只读本地状态，零网络开销；仅对变化的 peer 触发 onLatencyUpdate。
func (c *Client) latencyEmitLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.emitLatencies()
		}
	}
}

func (c *Client) emitLatencies() {
	if c.onLatencyUpdate == nil {
		return
	}
	for _, p := range c.peerMgr.List() {
		lat := c.GetPeerLatency(p.ID)
		c.latencyMu.Lock()
		prev, ok := c.lastEmittedLatency[p.ID]
		changed := !ok || lat != prev
		if changed {
			c.lastEmittedLatency[p.ID] = lat
		}
		c.latencyMu.Unlock()
		if changed {
			c.onLatencyUpdate(p.ID, lat)
		}
	}
}

// Conn 返回底层 UDPConn（供 tun bridge 直接使用）。
func (c *Client) Conn() *netconn.UDPConn { return c.conn }

// ServerAddr 返回服务端地址。
func (c *Client) ServerAddr() *net.UDPAddr { return c.serverAddr }

// Close 关闭客户端连接并清理所有资源。
func (c *Client) Close() {
	c.LeaveRoom()
	c.cancel() // 停止所有后台 goroutine。
	if c.conn != nil {
		c.conn.Close()
	}
	c.setState(StateDisconnected)
}

// ---- 心跳 ----

func (c *Client) heartbeatLoop() {
	ticker := time.NewTicker(time.Duration(protocol.HeartbeatInterval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			pkt := protocol.NewPing(c.cfg.PeerID, time.Now().UnixMilli())
			pkt.Room = c.cfg.Room
			if err := c.conn.SendPacket(c.serverAddr, pkt); err != nil {
				c.log.Warn("心跳发送失败", "err", err)
			}
			// 顺带上报本端服务器 RTT，供中转 peer 估算延迟。
			c.sendPeerMetrics()
		}
	}
}

// ---- 内部分发逻辑 ----

func (c *Client) dispatch(remote *net.UDPAddr, data []byte) {
	// 优先识别紧凑二进制帧——数据通道流量占绝大多数，让它走 O(1) 分流。
	if protocol.IsCompactFrame(data) {
		c.handleCompactFrame(remote, data)
		return
	}

	ptype, err := protocol.PeekType(data)
	if err != nil {
		return
	}

	switch ptype {
	case protocol.TypePong:
		var pong protocol.PongPacket
		if err := protocol.Decode(data, &pong); err == nil {
			// 非阻塞写——没人等就丢弃，避免 dispatch goroutine 卡住。
			select {
			case c.pongCh <- pong:
			default:
			}
		}
	case protocol.TypePing:
		c.handleIncomingPunch(remote, data)
	case protocol.TypeRoomStatus:
		c.handleRoomStatus(data)
	case protocol.TypePeerAddress:
		c.handlePeerAddress(data)
	case protocol.TypePeerLeave:
		c.handlePeerLeave(data)
	case protocol.TypeRelayData:
		// 兼容旧版 JSON Relay 帧（理论上不会再收到，留作降级）。
		c.handleRelayData(data)
	case protocol.TypeGameData:
		c.handleGameData(data)
	case protocol.TypeChat:
		c.handleChat(data)
	case protocol.TypePeerStatus:
		c.handlePeerStatus(data)
	case protocol.TypePeerMetrics:
		c.handlePeerMetrics(data)
	}
}

// handleCompactFrame 处理 P2P / Relay / Voice 紧凑帧，按帧类型分流：
//   - FrameVoice -> voiceHandler（前端播放），不进虚拟网卡
//   - FrameP2P / FrameRelay -> dataHandler（写虚拟网卡，裸 IP 包）
//
// 空载 payload（keepalive）直接丢弃，对上层透明。
func (c *Client) handleCompactFrame(remote *net.UDPAddr, data []byte) {
	frameType, srcVIP, _, payload, err := protocol.DecodeFrame(data)
	if err != nil {
		return
	}
	// P2P 通道地址自愈：CGNAT（移动家宽等）会轮换 NAT 映射，对端直连帧实际到达的
	// 源地址可能与打洞时协商的地址不同——一旦出站还打向旧映射，就会出现
	// "对端能 ping 通我、我 ping 不通对端"的单向死路。收到直连帧时把通道地址
	// 更新为实际来源即可恢复。FrameP2P/FramePing/FramePong 只走直连（服务端
	// relay 不转发，见 server/internal/relay），不会误把服务器地址写进通道。
	if frameType == protocol.FrameP2P || frameType == protocol.FramePing || frameType == protocol.FramePong {
		c.updateChannelAddr(remote, srcVIP)
	}
	if len(payload) == 0 {
		return // keepalive 空载帧
	}
	// payload 是 data 的尾段视图，handler 可能异步使用 -> 拷贝一份。
	buf := make([]byte, len(payload))
	copy(buf, payload)
	switch frameType {
	case protocol.FramePing:
		c.handlePeerPing(remote, srcVIP, buf)
		return
	case protocol.FramePong:
		c.handlePeerPong(srcVIP, buf)
		return
	case protocol.FrameVoice:
		if c.voiceHandler != nil {
			c.voiceHandler(srcVIP, buf)
		}
	case protocol.FrameVideo:
		if c.videoHandler != nil {
			c.handleVideoFragment(srcVIP, buf)
		}
	default: // FrameP2P / FrameRelay -> 网卡
		if c.dataHandler != nil {
			c.dataHandler(srcVIP, buf)
		}
	}
}

// handleVideoFragment 重组单个视频分片；一帧收齐后回调 videoHandler（已重组的完整帧字节）。
// 按 srcVIP 维护独立重组器；peer 退出时在 handlePeerLeave 清理其重组器，map 达 32 上限则
// 淘汰单个非当前条目腾位（极端兜底，仅丢一个 peer 的在途帧）。
// dispatch 由 UDPConn.Start 每包一个 goroutine 调用，关键帧的数十个分片会并发到达，
// 故 map 与重组器内部状态均由 videoMu 保护；videoHandler（EventsEmit）在锁外调用。
func (c *Client) handleVideoFragment(srcVIP uint32, payload []byte) {
	frag, err := protocol.DecodeVideoFragment(payload)
	if err != nil {
		return
	}
	c.videoMu.Lock()
	r := c.videoReassemblers[srcVIP]
	if r == nil {
		if len(c.videoReassemblers) >= 32 { // 上限保护：淘汰一个非当前条目腾位，避免无限增长
			for k := range c.videoReassemblers {
				delete(c.videoReassemblers, k)
				break
			}
		}
		r = protocol.NewVideoReassembler()
		c.videoReassemblers[srcVIP] = r
	}
	assembled, _, flags, ts, ok := r.Push(frag)
	c.videoMu.Unlock()
	if !ok {
		return
	}
	c.videoHandler(srcVIP, assembled, flags&protocol.VideoFlagKeyframe != 0, ts)
}

func (c *Client) handleRoomStatus(raw []byte) {
	var pkt protocol.RoomStatusPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		c.log.Warn("解码 RoomStatus 失败", "err", err)
		return
	}

	c.peerMgr.Reset()
	// 服务端没有显式告诉客户端"自己"是哪一个 peer，只能按 PeerID 比对。
	// PeerID 是 New() 时分配的本地 uuid，已经写在 JoinRoom 报文里。
	for _, info := range pkt.Peers {
		// 同一报文里"自己"的 nickName / vip / public_addr 也都是齐的——
		// 必须先 SetSelf 再 Upsert 其他人，否则前端用 self.id 过滤会失败。
		if info.ID == c.cfg.PeerID {
			c.peerMgr.SetSelf(info)
		} else {
			c.peerMgr.Upsert(info)
		}
	}

	c.log.Info("房间状态更新",
		"room", pkt.Room,
		"peers", len(pkt.Peers),
		"selfVIP", protocol.VIPToIP(pkt.LocalVIP),
	)

	// 先推 self，再推 peers——前端的 isMine 判断依赖 self 已就位。
	if c.onSelfUpdate != nil {
		c.onSelfUpdate(c.peerMgr.Self())
	}
	if c.onPeerUpdate != nil {
		c.onPeerUpdate(c.peerMgr.List())
	}

	// 对其他 peer 发起打洞。
	for _, info := range pkt.Peers {
		if info.ID == c.cfg.PeerID {
			continue
		}
		go c.punchPeer(info)
	}
}

func (c *Client) handlePeerAddress(raw []byte) {
	var pkt protocol.PeerAddressPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return
	}

	c.peerMgr.Upsert(pkt.Peer)
	c.log.Info("新 peer 加入", "peer", pkt.Peer.ID, "nick", pkt.Peer.NickName,
		"vip", protocol.VIPToIP(pkt.Peer.VirtualIP))

	if c.onPeerUpdate != nil {
		c.onPeerUpdate(c.peerMgr.List())
	}

	go c.punchPeer(pkt.Peer)
}

func (c *Client) handlePeerLeave(raw []byte) {
	var pkt protocol.PeerLeavePacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return
	}

	// 清理该 peer 的视频重组器（按 VIP 索引），避免 departed peer 的重组器惰性残留。
	if p := c.peerMgr.Get(pkt.PeerID); p != nil {
		c.videoMu.Lock()
		delete(c.videoReassemblers, p.VirtualIP)
		c.videoMu.Unlock()
	}
	c.peerMgr.Remove(pkt.PeerID)
	c.latencyMu.Lock()
	delete(c.lastEmittedLatency, pkt.PeerID)
	c.latencyMu.Unlock()
	c.channelsMu.Lock()
	if old, ok := c.channels[pkt.PeerID]; ok {
		old.Close()
		delete(c.channels, pkt.PeerID)
	}
	c.channelsMu.Unlock()

	c.log.Info("peer 退出", "peer", pkt.PeerID)

	if c.onPeerUpdate != nil {
		c.onPeerUpdate(c.peerMgr.List())
	}
}

func (c *Client) handleRelayData(raw []byte) {
	var pkt protocol.RelayDataPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return
	}
	if c.dataHandler != nil {
		c.dataHandler(pkt.SrcVIP, pkt.Payload)
	}
}

func (c *Client) handleGameData(raw []byte) {
	var pkt protocol.GameDataPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return
	}
	if c.dataHandler != nil {
		c.dataHandler(pkt.SrcVIP, pkt.Payload)
	}
}

func (c *Client) notifyPeers() {
	if c.onPeerUpdate != nil {
		c.onPeerUpdate(c.peerMgr.List())
	}
}

func (c *Client) handleChat(raw []byte) {
	var pkt protocol.ChatPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return
	}
	if c.chatHandler != nil {
		c.chatHandler(pkt.NickName, pkt.Message, pkt.Timestamp)
	}
}

// handlePeerStatus 收到他人语音状态变更：更新本地缓存的该 peer 状态并通知前端刷新信息卡。
func (c *Client) handlePeerStatus(raw []byte) {
	var pkt protocol.PeerStatusPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return
	}
	if pkt.PeerID == "" || pkt.PeerID == c.cfg.PeerID {
		return // 自己的状态以前端本地为准，忽略回环
	}
	if c.peerMgr.SetVoiceStatus(pkt.PeerID, pkt.VoiceOn, pkt.MicOn) {
		c.log.Debug("peer 状态更新", "peer", pkt.PeerID, "voiceOn", pkt.VoiceOn, "micOn", pkt.MicOn)
		if c.onPeerUpdate != nil {
			c.onPeerUpdate(c.peerMgr.List())
		}
	}
}

// handleIncomingPunch 处理打洞相关报文：
//   - 收到 punch_reply → 通知己方 punchPeer 打洞成功
//   - 收到 punch → 回复确认并建立被动 P2P 通道
//   - 其他 Ping（来自已建立通道的 peer）→ 忽略
func (c *Client) handleIncomingPunch(remote *net.UDPAddr, raw []byte) {
	var ping protocol.PingPacket
	if err := protocol.Decode(raw, &ping); err != nil {
		return
	}

	remoteStr := remote.String()
	c.clientLog("PUNCH", "收到Ping from=%s id=%s", remoteStr, ping.PeerID)

	switch ping.PeerID {
	case "punch_reply":
		c.clientLog("PUNCH", "收到punch_reply from=%s", remoteStr)
		c.pendingPunchesMu.Lock()
		if ch, ok := c.pendingPunches[remoteStr]; ok {
			delete(c.pendingPunches, remoteStr)
			close(ch)
			c.pendingPunchesMu.Unlock()
			c.clientLog("P2P", "打洞成功(主动) peer=%s", remoteStr)
		} else {
			c.pendingPunchesMu.Unlock()
			c.clientLog("WARN", "收到未知punch_reply remote=%s", remoteStr)
		}

	case "punch":
		c.clientLog("PUNCH", "收到打洞请求 from=%s", remoteStr)
		_ = c.conn.SendPunchReply(remote)
		c.createPassiveP2P(remote, remoteStr)

	default:
		c.clientLog("PUNCH", "收到普通Ping from=%s", remoteStr)
		c.createPassiveP2P(remote, remoteStr)
	}
}

func (c *Client) createPassiveP2P(remote *net.UDPAddr, remoteStr string) {
	c.clientLog("PUNCH", "尝试匹配peer remote=%s 候选=%d", remoteStr, len(c.peerMgr.List()))
	self := c.peerMgr.Self()
	for _, p := range c.peerMgr.List() {
		// 与 peer 任一候选地址匹配即认。双栈打洞下，远端可能先用 v4 或先用 v6 打过来。
		if !c.peerCandidatesMatch(p, remoteStr) {
			continue
		}
		c.channelsMu.Lock()
		if existing, exists := c.channels[p.ID]; exists && existing.Type() == netconn.ChannelP2P {
			c.channelsMu.Unlock()
			c.log.Debug("P2P 通道已存在", "peer", p.ID)
			return
		}
		// 替换可能已存在的 Relay 通道——记得 Close 旧的（虽然 Relay.Close 是空，
		// 这里也对 P2P 通道升级路径保持一致）。
		if old, ok := c.channels[p.ID]; ok {
			old.Close()
		}
		ch := netconn.NewP2PChannel(c.conn, remote, self.VirtualIP, p.VirtualIP)
		c.channels[p.ID] = ch
		c.channelsMu.Unlock()
		isV6 := protocol.IsIPv6Addr(remoteStr)
		if isV6 {
			c.clientLog("P2P", "通道建立(被动·IPv6) peer=%s addr=%s", p.ID, remoteStr)
		} else {
			c.clientLog("P2P", "通道建立(被动·IPv4) peer=%s addr=%s", p.ID, remoteStr)
		}
		c.notifyPeers()
		return
	}
	c.clientLog("WARN", "打洞: 未匹配到 peer 地址 remote=%s", remoteStr)
}

// peerCandidatesMatch 判断给定 peer 的任一候选公网端点是否与 remoteStr 相同。
// 候选包括：PublicAddress / PublicV4 / PublicV6。
func (c *Client) peerCandidatesMatch(p protocol.PeerInfo, remoteStr string) bool {
	if p.PublicAddress == remoteStr {
		return true
	}
	if p.PublicV4 != "" && p.PublicV4 == remoteStr {
		return true
	}
	if p.PublicV6 != "" && p.PublicV6 == remoteStr {
		return true
	}
	return false
}

// punchPeer 向对端执行 UDP hole punching。
//
// 双栈策略：peer 可能同时拥有 v4 和 v6 端点（IPv6 GUA + IPv4 NAT 双栈家宽很常见）。
// 我们对所有候选地址并行打洞，**先成功的那条**建立 P2P 通道，剩下的尝试 ctx 取消退出。
// 全部超时（15s）才回落到 Relay。
//
// 优先级：v6 通常无 NAT，建立后 RTT 更稳定；但首包延迟 v6 路径可能略大，
// 因此不是顺序"先 v6 后 v4"，而是真并行——服务端家用网络下 v4 / v6 谁快谁先成。
func (c *Client) punchPeer(info protocol.PeerInfo) {
	// 收集所有有效候选地址（去重）。
	seen := make(map[string]struct{})
	var candidates []string
	for _, a := range []string{info.PublicV4, info.PublicV6, info.PublicAddress} {
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		candidates = append(candidates, a)
	}
	if len(candidates) == 0 {
		c.log.Warn("peer 无任何公网端点，跳过打洞", "peer", info.ID)
		return
	}

	c.clientLog("PUNCH", "开始双栈打洞 peer=%s(%s) 候选=%v local=%s",
		info.ID, info.NickName, candidates, c.conn.LocalAddr())

	// 每个候选地址起一个 attempt；attemptCtx 取消时所有尝试都退出。
	attemptCtx, cancelAttempts := context.WithCancel(c.ctx)
	defer cancelAttempts()

	// 用 buffered chan 收集第一个成功的候选；后续成功者写不进去也无伤大雅（attemptCtx 已取消）。
	successCh := make(chan struct {
		addr   *net.UDPAddr
		addrSt string
	}, len(candidates))

	var wg sync.WaitGroup
	for _, addrStr := range candidates {
		peerAddr, err := net.ResolveUDPAddr("udp", addrStr)
		if err != nil {
			c.log.Warn("解析 peer 地址失败，跳过该候选", "peer", info.ID, "addr", addrStr, "err", err)
			continue
		}
		wg.Add(1)
		go func(addr *net.UDPAddr, addrS string) {
			defer wg.Done()
			if c.punchOne(attemptCtx, info, addr, addrS) {
				select {
				case successCh <- struct {
					addr   *net.UDPAddr
					addrSt string
				}{addr, addrS}:
				default:
				}
			}
		}(peerAddr, addrStr)
	}

	// 等第一个成功，或所有 attempt 都失败。
	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()

	select {
	case win := <-successCh:
		cancelAttempts() // 通知其它 attempt 退出
		self := c.peerMgr.Self()
		p2pCh := netconn.NewP2PChannel(c.conn, win.addr, self.VirtualIP, info.VirtualIP)
		c.channelsMu.Lock()
		if old, ok := c.channels[info.ID]; ok {
			old.Close()
		}
		c.channels[info.ID] = p2pCh
		c.channelsMu.Unlock()
		if protocol.IsIPv6Addr(win.addrSt) {
			c.clientLog("P2P", "通道建立成功(IPv6) peer=%s addr=%s", info.ID, win.addrSt)
		} else {
			c.clientLog("P2P", "通道建立成功(IPv4) peer=%s addr=%s", info.ID, win.addrSt)
		}
		c.notifyPeers()
		// 让 attempt goroutine 收尾，避免 leak。
		<-doneCh

	case <-doneCh:
		// 所有 attempt 都失败 → 回落 Relay。
		c.clientLog("RELAY", "双栈打洞均失败，使用中转 peer=%s", info.ID)
		self := c.peerMgr.Self()
		relay := netconn.NewRelayChannel(c.conn, c.serverAddr, self.VirtualIP, info.VirtualIP)
		c.channelsMu.Lock()
		if existing, ok := c.channels[info.ID]; ok && existing.Type() == netconn.ChannelP2P {
			// 极端时序：barrier 内某个 attempt 先回写了通道——优先保留 P2P。
			c.channelsMu.Unlock()
			return
		}
		if old, ok := c.channels[info.ID]; ok {
			old.Close()
		}
		c.channels[info.ID] = relay
		c.channelsMu.Unlock()
		c.notifyPeers()

	case <-c.ctx.Done():
		return
	}
}

// punchOne 向单一地址执行打洞，成功（收到 punch_reply）返回 true。
//
// 节奏：
//   - 前 3 秒每 200ms 一发（共 ~15 包），抢在对称 NAT 的端口预测窗口里穿
//   - 之后每 1 秒一发，总超时 15 秒
//
// 取消：ctx 取消时立即返回 false——上层用这个停止双栈里"另一边已经赢了"的剩余 attempt。
func (c *Client) punchOne(ctx context.Context, info protocol.PeerInfo, peerAddr *net.UDPAddr, addrStr string) bool {
	// 注册"等 punch_reply"的通知 chan，按 peerAddr 的 String() 索引。
	// handleIncomingPunch 收到 punch_reply 后查这张表把 chan close 掉。
	key := peerAddr.String()
	c.pendingPunchesMu.Lock()
	if _, exists := c.pendingPunches[key]; exists {
		c.pendingPunchesMu.Unlock()
		return false // 已经有同地址 attempt 在跑
	}
	notif := make(chan struct{})
	c.pendingPunches[key] = notif
	c.pendingPunchesMu.Unlock()

	defer func() {
		c.pendingPunchesMu.Lock()
		delete(c.pendingPunches, key)
		c.pendingPunchesMu.Unlock()
	}()

	c.clientLog("PUNCH", "开始打洞候选 peer=%s target=%s", info.ID, addrStr)

	fastTicker := time.NewTicker(200 * time.Millisecond)
	defer fastTicker.Stop()
	fastWindow := time.After(3 * time.Second)
	slowTicker := time.NewTicker(1 * time.Second)
	defer slowTicker.Stop()
	timeout := time.After(15 * time.Second)
	fastPhase := true

	_ = c.conn.Punch(peerAddr)

	for {
		select {
		case <-notif:
			return true

		case <-timeout:
			return false

		case <-fastWindow:
			fastPhase = false

		case <-fastTicker.C:
			if fastPhase {
				_ = c.conn.Punch(peerAddr)
			}

		case <-slowTicker.C:
			if !fastPhase {
				_ = c.conn.Punch(peerAddr)
			}

		case <-ctx.Done():
			return false
		}
	}
}
