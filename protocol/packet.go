package protocol

// PacketType 报文类型。
// MVP 阶段用字符串常量，便于 JSON 调试；后期换 protobuf 时集中改这里即可。
type PacketType string

const (
	// TypePing / TypePong 心跳保活 + 延迟测量（双向）。
	TypePing PacketType = "ping"
	TypePong PacketType = "pong"

	// TypeJoinRoom 客户端请求加入房间（C -> S）。
	TypeJoinRoom PacketType = "join_room"

	// TypeRoomStatus 服务端下发房间成员列表与虚拟 IP 分配（S -> C）。
	TypeRoomStatus PacketType = "room_status"

	// TypePeerAddress 服务端广播新成员公网端点，协助打洞（S -> C）。
	TypePeerAddress PacketType = "peer_address"

	// TypePeerLeave 通知某成员退出（S -> C）。
	TypePeerLeave PacketType = "peer_leave"

	// TypeRelayData P2P 失败时的流量中转（C <-> S <-> C）。
	TypeRelayData PacketType = "relay_data"

	// TypeGameData P2P 直连的游戏/应用流量（C <-> C）。
	TypeGameData PacketType = "game_data"

	// TypeChat 房间内聊天消息（C <-> S <-> C），服务端广播给所有人。
	TypeChat PacketType = "chat"

	// TypeCompactFrame 表示一条紧凑二进制数据帧（P2P / Relay）。
	// 这个值不会出现在 JSON 报文里，仅供服务端 / 客户端 dispatch 内部分流使用。
	TypeCompactFrame PacketType = "__frame__"
)

// Packet 是所有报文的外层信封。
// 收发时先解码出 Type，再按类型解码具体 Payload。
type Packet struct {
	Type   PacketType `json:"type"`             // 报文类型，必填
	Room   string     `json:"room,omitempty"`    // 房间号，按报文需要携带
	PeerID string     `json:"peer_id,omitempty"` // 发送方 peer ID
}
