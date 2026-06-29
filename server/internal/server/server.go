// Package server 实现 NetBridge 服务端的 UDP 收发主循环与报文分发。
//
// 设计要点：
//   - 单个 UDP socket 同时承担信令与中转，与 NAT 映射端口保持一致
//   - 主循环只做「读包 -> 解析 Type -> 分发到 Handler」，不含业务逻辑
//   - Handler 是接口，阶段 1 仅实现 Ping/Pong，阶段 2 注入房间/信令逻辑
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"

	"github.com/FuryHu/netbridge/protocol"
)

// Handler 处理一条入站报文。
// remote 为发送方公网端点（含 NAT 映射端口），用于打洞协助与回包。
// 返回的错误仅用于日志，不会中断主循环。
type Handler interface {
	Handle(ctx context.Context, remote *net.UDPAddr, raw []byte, ptype protocol.PacketType) error
}

// Server 是 UDP 服务核心。
type Server struct {
	conn    *net.UDPConn
	handler Handler
	wg      sync.WaitGroup
	log     *slog.Logger

	// sendMu 保护多 goroutine 并发写 UDP socket。
	// （net.UDPConn.WriteToUDP 本身是并发安全的，这里保留扩展位）
	sendMu sync.Mutex
}

// New 创建服务实例。handler 可在 Serve 前通过 SetHandler 注入（避免与 handler 包相互引用）。
func New(conn *net.UDPConn, handler Handler, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{conn: conn, handler: handler, log: log}
}

// SetHandler 在 Serve 前注入业务处理器。
func (s *Server) SetHandler(h Handler) { s.handler = h }

// Serve 阻塞运行收包主循环，直到 ctx 取消或连接关闭。
func (s *Server) Serve(ctx context.Context) error {
	s.log.Info("NetBridge server 开始收包", "addr", s.conn.LocalAddr())

	// ctx 取消时关闭连接，使 ReadFromUDP 解除阻塞并退出循环。
	go func() {
		<-ctx.Done()
		_ = s.conn.Close()
	}()

	buf := make([]byte, protocol.ReadBufferSize)
	for {
		n, remote, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				s.wg.Wait()
				return nil // 正常退出
			}
			if errors.Is(err, net.ErrClosed) {
				s.wg.Wait()
				return nil
			}
			s.log.Error("ReadFromUDP 失败", "err", err)
			continue
		}

		// 复制一份交给 handler，避免下一轮覆盖 buf。
		data := make([]byte, n)
		copy(data, buf[:n])

		s.wg.Add(1)
		go func(raw []byte, r *net.UDPAddr) {
			defer s.wg.Done()
			s.dispatch(ctx, r, raw)
		}(data, remote)
	}
}

// dispatch 解析报文 Type 并转发到 handler。
func (s *Server) dispatch(ctx context.Context, remote *net.UDPAddr, raw []byte) {
	// 紧凑二进制帧（P2P/Relay 数据通道）走特殊 Type，让 handler 直接转发，不走 JSON 解码。
	if protocol.IsCompactFrame(raw) {
		if err := s.handler.Handle(ctx, remote, raw, protocol.TypeCompactFrame); err != nil {
			s.log.Warn("处理紧凑帧失败", "remote", remote, "err", err)
		}
		return
	}

	ptype, err := protocol.PeekType(raw)
	if err != nil {
		s.log.Debug("无法解析报文类型，丢弃", "remote", remote, "err", err)
		return
	}
	if err := s.handler.Handle(ctx, remote, raw, ptype); err != nil {
		s.log.Warn("处理报文失败", "type", ptype, "remote", remote, "err", err)
	}
}

// Send 向指定端点发送原始字节。
func (s *Server) Send(addr *net.UDPAddr, data []byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	_, err := s.conn.WriteToUDP(data, addr)
	return err
}

// SendPacket 编码并发送一个报文。
func (s *Server) SendPacket(addr *net.UDPAddr, p any) error {
	data, err := protocol.Encode(p)
	if err != nil {
		return err
	}
	return s.Send(addr, data)
}
