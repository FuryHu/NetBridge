// Package relay 实现 NetBridge 服务端的流量中转兜底。
//
// 当 P2P 打洞失败时，客户端将数据包装为 RelayData / 紧凑帧发送到服务端，
// 服务端按目标虚拟 IP 查找到对应 peer 后原样转发，不解析 Payload 内容。
package relay

import (
	"log/slog"
	"net"

	"github.com/FuryHu/netbridge/protocol"
	"github.com/FuryHu/netbridge/server/internal/room"
)

// Relay 处理中转报文的路由与转发。
type Relay struct {
	mgr      *room.Manager
	sendFunc func(addr *net.UDPAddr, data []byte) error
	log      *slog.Logger
}

// New 创建 Relay 实例。
// sendFunc 由调用方注入（通常为 server.Server.Send），避免 import 循环。
func New(mgr *room.Manager, sendFunc func(addr *net.UDPAddr, data []byte) error, log *slog.Logger) *Relay {
	if log == nil {
		log = slog.Default()
	}
	return &Relay{mgr: mgr, sendFunc: sendFunc, log: log}
}

// HandleCompactFrame 处理紧凑二进制 Relay 帧：解析头部，按 DstVIP 找到目标 peer 后整帧转发。
//
// 头部里没有 Room 信息（节省字节），这里通过发送方公网端点反查所属房间。
// 一个 peer 同一时刻只在一个房间，反查代价对中转量级足够低。
func (r *Relay) HandleCompactFrame(remote *net.UDPAddr, raw []byte) error {
	frameType, _, dstVIP, _, err := protocol.DecodeFrame(raw)
	if err != nil {
		return err
	}
	if frameType != protocol.FrameRelay && frameType != protocol.FrameVoice {
		// 紧凑 P2P 帧不该到达服务端，丢弃即可。Relay 与 Voice 都按 DstVIP 透传，
		// 服务端不解析 payload（仅原样转发字节）。注意这并不等于机密性：relay 运营方
		// 或链路抓包者仍能拿到原始 Opus 字节自行解码，当前路径无端到端加密。
		r.log.Debug("收到非 Relay/Voice 类型的紧凑帧", "type", frameType, "remote", remote)
		return nil
	}

	rm := r.mgr.LookupByAddr(remote)
	if rm == nil {
		r.log.Debug("Relay 帧来源不在任何房间", "remote", remote)
		return nil
	}
	target := rm.GetPeerByVIP(dstVIP)
	if target == nil {
		r.log.Debug("Relay 帧目标 peer 不存在", "dstVIP", dstVIP)
		return nil
	}

	// 直接转发原始字节，零拷贝零解码。
	return r.sendFunc(target.Addr, raw)
}

// HandleRelayData 解码 RelayData JSON 报文（旧版兼容路径），按 DstVIP 路由并转发。
// 返回 error 仅用于日志，不中断主循环。
func (r *Relay) HandleRelayData(raw []byte) error {
	var pkt protocol.RelayDataPacket
	if err := protocol.Decode(raw, &pkt); err != nil {
		return err
	}

	rm := r.mgr.Get(pkt.Room)
	if rm == nil {
		r.log.Debug("RelayData 目标房间不存在", "room", pkt.Room)
		return nil
	}

	target := rm.GetPeerByVIP(pkt.DstVIP)
	if target == nil {
		r.log.Debug("RelayData 目标 peer 不存在", "room", pkt.Room, "dstVIP", pkt.DstVIP)
		return nil
	}

	forward := protocol.RelayDataPacket{
		Packet: protocol.Packet{
			Type:   protocol.TypeRelayData,
			Room:   pkt.Room,
			PeerID: pkt.PeerID,
		},
		SrcVIP:  pkt.SrcVIP,
		DstVIP:  pkt.DstVIP,
		Payload: pkt.Payload,
	}

	data, err := protocol.Encode(forward)
	if err != nil {
		return err
	}

	if err := r.sendFunc(target.Addr, data); err != nil {
		return err
	}

	r.log.Debug("RelayData 已转发",
		"room", pkt.Room,
		"srcVIP", pkt.SrcVIP,
		"dstVIP", pkt.DstVIP,
		"target", target.Addr,
		"size", len(pkt.Payload),
	)
	return nil
}
