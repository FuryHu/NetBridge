package netconn

import (
	"net"
	"sync"
	"time"

	"github.com/FuryHu/netbridge/protocol"
)

// ChannelType 通道类型。
type ChannelType int

const (
	ChannelNone  ChannelType = iota
	ChannelP2P               // P2P 直连
	ChannelRelay             // 服务端中转
)

// Channel 抽象网络通道，客户端通过它发送数据给对端。
type Channel interface {
	// Send 发送原始字节到对端（默认帧类型：P2P 直连用 FrameP2P，Relay 用 FrameRelay）。
	Send(data []byte) error
	// SendTyped 用指定帧类型发送。语音走 FrameVoice 时用，避免与游戏数据混入同一帧类型。
	SendTyped(frameType byte, data []byte) error
	// Type 返回通道类型。
	Type() ChannelType
	// Close 释放底层资源（心跳 goroutine 等）。可多次调用。
	Close()
}

// keepaliveInterval 是 P2P 通道用于维持 NAT 表项的心跳间隔。
//
// 国内家用路由器 UDP NAT 表项老化时间普遍 30–60 秒；
// 设 15 秒能在大多数路由器上稳住一条 P2P 路径。若发现仍有 NAT 老化掉线问题，
// 可调到 10 秒，但代价是更多无效流量。
const keepaliveInterval = 15 * time.Second

// P2PChannel 直连通道：把负载封进紧凑二进制帧后发到对端公网地址。
//
// 之所以必须封一层帧而不是裸发：对端的 UDP 读循环 dispatch 是同一个 socket，
// 既要接服务端的 JSON 信令、又要接 peer 的数据，必须有 magic 让分流逻辑识别。
//
// 通道创建时启动后台 keepalive goroutine，每 keepaliveInterval 秒往对端发一个
// 0 长度的紧凑 P2P 帧维持 NAT 表项。Close 时停止。
type P2PChannel struct {
	conn     *UDPConn
	peerAddr *net.UDPAddr
	srcVIP   uint32
	dstVIP   uint32

	closeOnce sync.Once
	stopCh    chan struct{}
}

// NewP2PChannel 创建 P2P 直连通道并启动 keepalive。
func NewP2PChannel(conn *UDPConn, peerAddr *net.UDPAddr, srcVIP, dstVIP uint32) *P2PChannel {
	c := &P2PChannel{
		conn:     conn,
		peerAddr: peerAddr,
		srcVIP:   srcVIP,
		dstVIP:   dstVIP,
		stopCh:   make(chan struct{}),
	}
	go c.keepaliveLoop()
	return c
}

// SendTyped 用指定帧类型发送（语音用 FrameVoice，游戏数据用 FrameP2P）。
func (c *P2PChannel) SendTyped(frameType byte, data []byte) error {
	frame := protocol.EncodeFrame(frameType, c.srcVIP, c.dstVIP, data)
	return c.conn.SendRaw(c.peerAddr, frame)
}

func (c *P2PChannel) Send(data []byte) error {
	return c.SendTyped(protocol.FrameP2P, data)
}

func (c *P2PChannel) Type() ChannelType { return ChannelP2P }

func (c *P2PChannel) Close() {
	c.closeOnce.Do(func() {
		close(c.stopCh)
	})
}

// PeerAddr 返回对端地址，供上层判断是否需要更新。
func (c *P2PChannel) PeerAddr() *net.UDPAddr { return c.peerAddr }

// keepaliveLoop 周期性向对端发空载 P2P 帧——既维持 NAT 表项，
// 也能让对端在 P2P 路径意外失效时尽快观测到（结合 SetReadDeadline 监控时可用）。
//
// 空载帧 payload 为空，对端 handleCompactFrame 会因 len(payload)==0 直接丢弃，
// 不会进入 dataHandler，对上层透明。
func (c *P2PChannel) keepaliveLoop() {
	t := time.NewTicker(keepaliveInterval)
	defer t.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-t.C:
			_ = c.Send(nil)
		}
	}
}

// RelayChannel 中转通道：数据经由服务端转发到目标 peer。
//
// Relay 帧同样使用紧凑二进制格式，服务端按头部里的 DstVIP 路由后原样转发，
// 不再解析 JSON Payload，避免大包的 base64 膨胀。
//
// Relay 不需要 keepalive——客户端本身的 5 秒心跳已经维持 client↔server 的 NAT 表项。
type RelayChannel struct {
	conn       *UDPConn
	serverAddr *net.UDPAddr
	srcVIP     uint32
	dstVIP     uint32
}

// NewRelayChannel 创建中转通道。
func NewRelayChannel(conn *UDPConn, serverAddr *net.UDPAddr, srcVIP, dstVIP uint32) *RelayChannel {
	return &RelayChannel{
		conn:       conn,
		serverAddr: serverAddr,
		srcVIP:     srcVIP,
		dstVIP:     dstVIP,
	}
}

func (c *RelayChannel) SendTyped(frameType byte, data []byte) error {
	frame := protocol.EncodeFrame(frameType, c.srcVIP, c.dstVIP, data)
	return c.conn.SendRaw(c.serverAddr, frame)
}

func (c *RelayChannel) Send(data []byte) error {
	return c.SendTyped(protocol.FrameRelay, data)
}

func (c *RelayChannel) Type() ChannelType { return ChannelRelay }

func (c *RelayChannel) Close() {}
