package protocol

// PeerInfo 描述房间内一个成员。
//
// PublicAddress 是历史"主路径"字段——它要么是 v4 端点，要么是 v6 端点，
// 取决于该 peer 与服务端建立连接走的是哪个族。新版客户端同时读 V4 / V6 两个
// 双栈字段，并行打洞——先建立的那条用于 P2P，另一条留作冗余。
type PeerInfo struct {
	ID            string `json:"id"`                 // peer 唯一 ID（uuid）
	NickName      string `json:"name"`               // 昵称
	VirtualIP     uint32 `json:"vip"`                // 分配的虚拟 IP（主机字节序整数，如 10.66.0.2）
	PublicAddress string `json:"pub_addr,omitempty"` // 主路径公网端点（v4 或 v6）
	PublicV4      string `json:"v4,omitempty"`       // 公网 IPv4 端点（若可知）
	PublicV6      string `json:"v6,omitempty"`       // 公网 IPv6 端点（若可知）
}

// JoinRoomPacket 加入房间请求（C -> S）。
type JoinRoomPacket struct {
	Packet
	Room     string `json:"room"` // 房间号 / 暗号
	NickName string `json:"name"` // 昵称
}

// RoomStatusPacket 房间状态（S -> C）。
type RoomStatusPacket struct {
	Packet
	Peers      []PeerInfo `json:"peers"`        // 当前房间全部成员
	LocalIndex int        `json:"local_index"`  // 自己在 Peers 中的下标
	LocalVIP   uint32     `json:"local_vip"`    // 分配给自己的虚拟 IP
}

// PeerAddressPacket 新成员公网端点广播（S -> C），协助打洞。
type PeerAddressPacket struct {
	Packet
	Peer PeerInfo `json:"peer"` // 新加入或更新的 peer 信息
}

// PeerLeavePacket 成员退出通知（S -> C）。
type PeerLeavePacket struct {
	Packet
	PeerID string `json:"peer_id"` // 退出的 peer ID
}

// RelayDataPacket 流量中转（C <-> S <-> C）。
// Payload 为原始 IP 包字节，服务端不解析内容，仅按 DstVIP 路由。
type RelayDataPacket struct {
	Packet
	DstVIP  uint32 `json:"dst_vip"`            // 目标虚拟 IP
	SrcVIP  uint32 `json:"src_vip"`            // 源虚拟 IP
	Payload []byte `json:"payload,omitempty"`  // 原始 IP 包
}

// GameDataPacket P2P 直连流量（C <-> C）。
type GameDataPacket struct {
	Packet
	SrcVIP  uint32 `json:"src_vip"`
	DstVIP  uint32 `json:"dst_vip"`
	Payload []byte `json:"payload,omitempty"`
}

// PingPacket / PongPacket 心跳。Timestamp 用于 RTT 计算（毫秒时间戳）。
type PingPacket struct {
	Packet
	Timestamp int64 `json:"ts"`
}

// PongPacket 复用 PingPacket 结构，Type 固定为 TypePong。
type PongPacket = PingPacket

// NewPing 构造一个 Ping 报文。
func NewPing(peerID string, ts int64) PingPacket {
	return PingPacket{
		Packet:   Packet{Type: TypePing, PeerID: peerID},
		Timestamp: ts,
	}
}

// NewPong 根据收到的 Ping 生成对应 Pong（回填原时间戳以便算 RTT）。
func NewPong(in PingPacket) PongPacket {
	return PongPacket{
		Packet:   Packet{Type: TypePong},
		Timestamp: in.Timestamp,
	}
}

// ChatPacket 聊天消息（C <-> S <-> C）。客户端发给服务端，由服务端向房间内所有人广播。
type ChatPacket struct {
	Packet
	NickName  string `json:"name"` // 发送者昵称
	Message   string `json:"msg"`  // 消息内容
	Timestamp int64  `json:"ts"`   // 发送时间（毫秒）
}
