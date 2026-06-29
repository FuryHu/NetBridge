// Package signaling 实现 NetBridge 服务端的信令分发与房间业务逻辑。
//
// RoomHandler 实现了 server.Handler 接口，是阶段 2 的核心处理器：
//   - Ping / Pong：心跳保活与延迟测量
//   - JoinRoom：加入房间、分配虚拟 IP、广播成员变更
//   - 后台超时扫描：定时剔除无心跳 peer 并通知剩余成员
package signaling

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/FuryHu/netbridge/protocol"
	"github.com/FuryHu/netbridge/server/internal/peer"
	"github.com/FuryHu/netbridge/server/internal/relay"
	"github.com/FuryHu/netbridge/server/internal/room"
	"github.com/FuryHu/netbridge/server/internal/server"
)

// RoomHandler 实现 server.Handler，处理所有房间相关的信令。
type RoomHandler struct {
	srv   *server.Server
	mgr   *room.Manager
	relay *relay.Relay
	log   *slog.Logger
}

// NewRoomHandler 构造 RoomHandler 并启动后台超时扫描 goroutine。
// ctx 取消时扫描 goroutine 自动退出。
func NewRoomHandler(srv *server.Server, mgr *room.Manager, log *slog.Logger, ctx context.Context) *RoomHandler {
	if log == nil {
		log = slog.Default()
	}
	h := &RoomHandler{
		srv:   srv,
		mgr:   mgr,
		relay: relay.New(mgr, srv.Send, log),
		log:   log,
	}
	go h.scanLoop(ctx)
	return h
}

// Handle 按报文类型分发到具体处理函数。
func (h *RoomHandler) Handle(ctx context.Context, remote *net.UDPAddr, raw []byte, ptype protocol.PacketType) error {
	switch ptype {
	case protocol.TypePing:
		return h.handlePing(remote, raw)
	case protocol.TypeJoinRoom:
		return h.handleJoinRoom(remote, raw)
	case protocol.TypeRelayData:
		// 旧版 JSON Relay 帧，留作向后兼容。
		return h.relay.HandleRelayData(raw)
	case protocol.TypeCompactFrame:
		// 新版紧凑二进制帧：直接按帧头里的 DstVIP 转发。
		return h.relay.HandleCompactFrame(remote, raw)
	case protocol.TypeChat:
		return h.handleChat(raw)
	case protocol.TypePeerLeave:
		return h.handlePeerLeave(raw)
	default:
		h.log.Debug("未处理的报文类型", "type", ptype, "remote", remote)
		return nil
	}
}

// handlePing 解码心跳、刷新 peer 活跃时间并回复 Pong。
func (h *RoomHandler) handlePing(remote *net.UDPAddr, raw []byte) error {
	var ping protocol.PingPacket
	if err := protocol.Decode(raw, &ping); err != nil {
		return err
	}

	// 若 ping 携带房间信息，刷新对应 peer 的心跳。
	if ping.Room != "" && ping.PeerID != "" {
		if r := h.mgr.Get(ping.Room); r != nil {
			if p := r.GetPeer(ping.PeerID); p != nil {
				p.Touch()
			}
		}
	}

	pong := protocol.NewPong(ping)
	return h.srv.SendPacket(remote, pong)
}

// handleJoinRoom 处理客户端加入房间请求：注册 peer、分配 VIP、下发房间状态、广播新成员。
func (h *RoomHandler) handleJoinRoom(remote *net.UDPAddr, raw []byte) error {
	var req protocol.JoinRoomPacket
	if err := protocol.Decode(raw, &req); err != nil {
		return err
	}
	if req.Room == "" {
		h.log.Warn("JoinRoom 缺少房间号", "remote", remote)
		return nil
	}

	// 创建 peer（客户端需在上层自行生成 PeerID，放在信封中）。
	peerID := req.PeerID
	if peerID == "" {
		peerID = req.NickName // 兜底：用昵称当 ID（不推荐，生产环境由客户端生成 uuid）
	}
	nickName := req.NickName
	if nickName == "" {
		nickName = peerID
	}

	p := peer.New(peerID, nickName, remote)

	// 加入房间并分配虚拟 IP。
	r := h.mgr.GetOrCreate(req.Room)
	vip := r.Join(p)

	h.log.Info("peer 加入房间",
		"room", req.Room,
		"peer", peerID,
		"nick", nickName,
		"vip", protocol.VIPToIP(vip),
		"remote", remote,
	)

	// 1. 向新 peer 下发完整房间状态。
	//
	// 每个 PeerInfo 都填齐 v4 / v6 / PublicAddress——服务端只看到对方与自己建立连接
	// 用的那个族，因此另一个族字段会是空字符串。客户端会用非空字段并行打洞。
	peers := r.PeerList()
	peerInfos := make([]protocol.PeerInfo, 0, len(peers))
	localIndex := -1
	for i, rp := range peers {
		v4, v6 := protocol.NormalizeUDPAddr(rp.Addr)
		peerInfos = append(peerInfos, protocol.PeerInfo{
			ID:            rp.ID,
			NickName:      rp.NickName,
			VirtualIP:     rp.VirtualIP,
			PublicAddress: protocol.PreferredAddr(v4, v6),
			PublicV4:      v4,
			PublicV6:      v6,
		})
		if rp.ID == peerID {
			localIndex = i
		}
	}

	status := protocol.RoomStatusPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypeRoomStatus,
			Room:   req.Room,
			PeerID: peerID,
		},
		Peers:      peerInfos,
		LocalIndex: localIndex,
		LocalVIP:   vip,
	}
	if err := h.srv.SendPacket(remote, status); err != nil {
		h.log.Warn("发送 RoomStatus 失败", "remote", remote, "err", err)
	}

	// 2. 向房间内其他成员广播新 peer 的公网端点。
	v4New, v6New := protocol.NormalizeUDPAddr(remote)
	addrPkt := protocol.PeerAddressPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypePeerAddress,
			Room:   req.Room,
			PeerID: peerID,
		},
		Peer: protocol.PeerInfo{
			ID:            p.ID,
			NickName:      p.NickName,
			VirtualIP:     vip,
			PublicAddress: protocol.PreferredAddr(v4New, v6New),
			PublicV4:      v4New,
			PublicV6:      v6New,
		},
	}
	for _, rp := range peers {
		if rp.ID == peerID {
			continue // 不发给新 peer 自己
		}
		if err := h.srv.SendPacket(rp.Addr, addrPkt); err != nil {
			h.log.Warn("广播 PeerAddress 失败", "target", rp.ID, "err", err)
		}
	}

	return nil
}

// handleChat 聊天消息广播：收到一条 Chat，向房间内所有其他成员转发。
func (h *RoomHandler) handleChat(raw []byte) error {
	var pkt protocol.ChatPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return err
	}
	if pkt.Room == "" {
		return nil
	}

	rm := h.mgr.Get(pkt.Room)
	if rm == nil {
		return nil
	}

	// 编码后向房间内所有 peer 广播（含发送者自己，方便前端统一展示）。
	data, err := protocol.Encode(pkt)
	if err != nil {
		return err
	}
	for _, p := range rm.PeerList() {
		if err := h.srv.Send(p.Addr, data); err != nil {
			h.log.Warn("广播 Chat 失败", "target", p.ID, "err", err)
		}
	}
	return nil
}

// handlePeerLeave 处理客户端主动退出：移除 peer 并广播给同房间其他人。
func (h *RoomHandler) handlePeerLeave(raw []byte) error {
	var pkt protocol.PeerLeavePacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return err
	}
	if pkt.Room == "" || pkt.PeerID == "" {
		return nil
	}

	rm := h.mgr.Get(pkt.Room)
	if rm == nil {
		return nil
	}

	rm.Leave(pkt.PeerID)
	h.log.Info("peer 主动退出", "room", pkt.Room, "peer", pkt.PeerID)

	// 向剩余成员广播退出通知。
	leavePkt := protocol.PeerLeavePacket{
		Packet: protocol.Packet{
			Type:   protocol.TypePeerLeave,
			Room:   pkt.Room,
			PeerID: pkt.PeerID,
		},
		PeerID: pkt.PeerID,
	}
	for _, rp := range rm.PeerList() {
		if err := h.srv.SendPacket(rp.Addr, leavePkt); err != nil {
			h.log.Warn("广播 PeerLeave 失败", "target", rp.ID, "err", err)
		}
	}

	// 空房间回收。
	if rm.IsEmpty() {
		h.mgr.Remove(pkt.Room)
		h.log.Info("房间已回收", "room", pkt.Room)
	}
	return nil
}

// scanLoop 后台定时扫描所有房间的超时 peer，移除并广播 PeerLeave。
func (h *RoomHandler) scanLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(protocol.HeartbeatInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.log.Debug("超时扫描 goroutine 退出")
			return
		case <-ticker.C:
			h.scanAndClean()
		}
	}
}

// scanAndClean 执行一次超时扫描，移除僵死 peer 并广播退出通知。
func (h *RoomHandler) scanAndClean() {
	timeout := time.Duration(protocol.PeerTimeout) * time.Second
	results := h.mgr.ScanTimeout(timeout)

	for _, r := range results {
		h.log.Info("peer 心跳超时，移除",
			"room", r.RoomID,
			"peer", r.Peer.ID,
			"lastSeen", r.Peer.LastSeen().Format(time.RFC3339),
		)

		// 向同房间其余成员广播 PeerLeave。
		room := h.mgr.Get(r.RoomID)
		if room == nil {
			continue // 房间已被回收
		}
		leavePkt := protocol.PeerLeavePacket{
			Packet: protocol.Packet{
				Type:   protocol.TypePeerLeave,
				Room:   r.RoomID,
				PeerID: r.Peer.ID,
			},
			PeerID: r.Peer.ID,
		}
		for _, rp := range room.PeerList() {
			if err := h.srv.SendPacket(rp.Addr, leavePkt); err != nil {
				h.log.Warn("广播 PeerLeave 失败", "target", rp.ID, "err", err)
			}
		}
	}
}
