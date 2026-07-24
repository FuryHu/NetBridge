package protocol

import (
	"encoding/binary"
	"errors"
)

// 紧凑二进制帧用于 P2P / Relay 的数据通道。
//
// 信令报文继续走 JSON（数量少、调试方便），但数据通道每秒成千上万包，
// 用 JSON+base64 会让裸 IP 包膨胀 ~35%，1400 字节 MTU 立刻就超了，
// 直接表现为大量分片 + 丢包（Civ 6 的回合同步包尤其敏感）。
//
// 帧布局（小端，固定 12 字节头 + 负载）：
//   [0..1]   magic = 'N','B'
//   [2]      frameType（FrameP2P / FrameRelay）
//   [3]      reserved，目前总是 0
//   [4..7]   srcVIP（uint32，主机字节序的虚拟 IP 主机号）
//   [8..11]  dstVIP（uint32）
//   [12..]   payload（裸 IP 包字节）
//
// 之所以 magic 选两个不会出现在 JSON 起始字节的可打印 ASCII：
// 这样 dispatch 只看头 2 字节就能 O(1) 区分「紧凑帧」与「JSON 报文」。
// JSON 起始永远是 '{'(0x7B)，绝不会撞上 'N'(0x4E)。

const (
	FrameMagic0 byte = 'N'
	FrameMagic1 byte = 'B'

	FrameP2P   byte = 1
	FrameRelay byte = 2

	// FrameVoice 语音帧：payload 为 voice 子格式（codec/seq/timestamp/audio，见 voice.go）。
	// 走 P2P 直连时直接发对端，走 Relay 时由服务端按 dstVIP 透传（与 FrameRelay 同路径）。
	// 发言人标识复用帧头 srcVIP，不在 payload 重复——这也是为未来 SFU 留的口子：
	// 服务端可按 srcVIP 识别并扇出某人的语音流，无需改协议。
	FrameVoice byte = 3

	// FramePing / FramePong P2P 延迟探测：payload 为 8 字节小端 int64 毫秒时间戳。
	//   - ping 携带发送方本地时间戳；
	//   - pong 回填同一时间戳，发送方据此算 RTT。
	// 仅走 P2P 直连（服务端 relay 不转发这两类帧）；中转 peer 的延迟用"双方 SRTT 之和"
	// 估算，见 PeerMetricsPacket。每条 P2P 通道的 ping 同时充当 NAT keepalive——
	// 故 ping 间隔即 keepalive 间隔，pong 回包也顺带维持反向 NAT。
	FramePing byte = 4
	FramePong byte = 5

	FrameHeaderSize = 12
)

// PingPayloadSize 延迟探测帧 payload 长度：8 字节小端 int64 毫秒时间戳。
const PingPayloadSize = 8

// EncodePingTS 把毫秒时间戳编码为延迟探测帧 payload。
func EncodePingTS(ts int64) []byte {
	b := make([]byte, PingPayloadSize)
	binary.LittleEndian.PutUint64(b, uint64(ts))
	return b
}

// DecodePingTS 从延迟探测帧 payload 解出时间戳。长度不足返回 ok=false。
func DecodePingTS(b []byte) (ts int64, ok bool) {
	if len(b) < PingPayloadSize {
		return 0, false
	}
	return int64(binary.LittleEndian.Uint64(b[:PingPayloadSize])), true
}

// ErrShortFrame 表示帧长度不足以容纳头部。
var ErrShortFrame = errors.New("frame too short")

// ErrBadMagic 表示头部 magic 不匹配。
var ErrBadMagic = errors.New("bad frame magic")

// IsCompactFrame 判断一段字节是否为紧凑二进制帧。
// 仅看前两字节，不验证长度——dispatch 用它作快速分流。
func IsCompactFrame(data []byte) bool {
	return len(data) >= 2 && data[0] == FrameMagic0 && data[1] == FrameMagic1
}

// EncodeFrame 把负载封装成紧凑帧。返回值可直接写入 UDP。
// 调用方应预先保证 payload 长度合理（MTU 内）。
func EncodeFrame(frameType byte, srcVIP, dstVIP uint32, payload []byte) []byte {
	buf := make([]byte, FrameHeaderSize+len(payload))
	buf[0] = FrameMagic0
	buf[1] = FrameMagic1
	buf[2] = frameType
	buf[3] = 0
	binary.LittleEndian.PutUint32(buf[4:8], srcVIP)
	binary.LittleEndian.PutUint32(buf[8:12], dstVIP)
	copy(buf[FrameHeaderSize:], payload)
	return buf
}

// DecodeFrame 解析紧凑帧的头部并返回 payload 视图（零拷贝）。
// payload 直接引用 data 的尾段，调用方若要保留需自行 copy。
func DecodeFrame(data []byte) (frameType byte, srcVIP, dstVIP uint32, payload []byte, err error) {
	if len(data) < FrameHeaderSize {
		err = ErrShortFrame
		return
	}
	if data[0] != FrameMagic0 || data[1] != FrameMagic1 {
		err = ErrBadMagic
		return
	}
	frameType = data[2]
	srcVIP = binary.LittleEndian.Uint32(data[4:8])
	dstVIP = binary.LittleEndian.Uint32(data[8:12])
	payload = data[FrameHeaderSize:]
	return
}
