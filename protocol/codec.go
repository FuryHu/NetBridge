package protocol

import "encoding/json"

// Encode 将任意报文结构序列化为字节切片。
// MVP 使用 JSON，便于调试；后期换 protobuf 时只需替换本文件实现，调用方不变。
func Encode(p any) ([]byte, error) {
	return json.Marshal(p)
}

// MustEncode 编码失败时 panic，仅用于确定不会失败的常量报文（如内部构造）。
func MustEncode(p any) []byte {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return b
}

// Decode 将字节切片反序列化到目标结构。
func Decode(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// PeekType 仅解码报文外层信封，取出 Type 字段，用于分发。
// 这一步避免一次性 Unmarshal 到具体类型导致类型耦合。
//
// 注意：仅适用于 JSON 信令报文。数据通道的紧凑帧请先用 IsCompactFrame 判断。
func PeekType(data []byte) (PacketType, error) {
	var p Packet
	if err := json.Unmarshal(data, &p); err != nil {
		return "", err
	}
	return p.Type, nil
}
