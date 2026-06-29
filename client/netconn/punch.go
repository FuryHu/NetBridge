package netconn

import (
	"net"
	"time"

	"github.com/FuryHu/netbridge/protocol"
)

// Punch 向对端发送一个打洞包（Ping 标记）。
// 打洞成功与否由上层通过事件通知判断，本方法仅负责发送。
// 与 Start() 读循环协作，不直接调用 ReadPacket 避免竞争。
func (u *UDPConn) Punch(peerAddr *net.UDPAddr) error {
	punchPkt := protocol.PingPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypePing,
			PeerID: "punch",
		},
		Timestamp: time.Now().UnixMilli(),
	}
	return u.SendPacket(peerAddr, punchPkt)
}

// SendPunchReply 向打洞发起方回复确认包（被叫方收到打洞请求后调用）。
func (u *UDPConn) SendPunchReply(peerAddr *net.UDPAddr) error {
	reply := protocol.PingPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypePing,
			PeerID: "punch_reply",
		},
		Timestamp: time.Now().UnixMilli(),
	}
	return u.SendPacket(peerAddr, reply)
}
