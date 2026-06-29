package server

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/FuryHu/netbridge/protocol"
)

// PingHandler 阶段 1 的最小业务处理器：收到 Ping 立即回 Pong。
// 阶段 2 会被 RoomHandler 取代，这里独立保留便于分阶段联调。
type PingHandler struct {
	srv *Server
	log *slog.Logger
}

// NewPingHandler 构造 PingHandler。
func NewPingHandler(srv *Server, log *slog.Logger) *PingHandler {
	if log == nil {
		log = slog.Default()
	}
	return &PingHandler{srv: srv, log: log}
}

// Handle 按报文类型分发。
func (h *PingHandler) Handle(ctx context.Context, remote *net.UDPAddr, raw []byte, ptype protocol.PacketType) error {
	switch ptype {
	case protocol.TypePing:
		return h.handlePing(remote, raw)
	default:
		// 阶段 1 暂不处理的类型，记录后忽略。
		h.log.Debug("阶段1忽略的报文类型", "type", ptype, "remote", remote)
		return nil
	}
}

// handlePing 解码 Ping 并回填 Pong（保留原时间戳，便于客户端算 RTT）。
func (h *PingHandler) handlePing(remote *net.UDPAddr, raw []byte) error {
	var ping protocol.PingPacket
	if err := protocol.Decode(raw, &ping); err != nil {
		return err
	}
	pong := protocol.NewPong(ping)
	if err := h.srv.SendPacket(remote, pong); err != nil {
		return err
	}
	h.log.Info("Ping -> Pong", "remote", remote, "rtt_ts", ping.Timestamp, "now_ms", time.Now().UnixMilli())
	return nil
}
