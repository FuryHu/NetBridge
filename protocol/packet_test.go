package protocol

import (
	"bytes"
	"testing"
)

// TestEncodeDecodeRoundTrip 验证各类报文的序列化往返。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   any
		out  any // 与 in 同类型的零值指针
	}{
		{
			name: "ping",
			in:   NewPing("peer-A", 1719400000000),
			out:  &PingPacket{},
		},
		{
			name: "join_room",
			in: JoinRoomPacket{
				Packet:   Packet{Type: TypeJoinRoom},
				Room:     "game666",
				NickName: "alice",
			},
			out: &JoinRoomPacket{},
		},
		{
			name: "room_status",
			in: RoomStatusPacket{
				Packet:     Packet{Type: TypeRoomStatus, Room: "game666"},
				Peers:      []PeerInfo{{ID: "A", NickName: "alice", VirtualIP: 0x0A420002}},
				LocalIndex: 0,
				LocalVIP:   0x0A420002,
			},
			out: &RoomStatusPacket{},
		},
		{
			name: "relay_data",
			in: RelayDataPacket{
				Packet:  Packet{Type: TypeRelayData, Room: "game666"},
				DstVIP:  0x0A420003,
				SrcVIP:  0x0A420002,
				Payload: []byte{0x45, 0x00, 0x00, 0x14},
			},
			out: &RelayDataPacket{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := Encode(c.in)
			if err != nil {
				t.Fatalf("Encode 失败: %v", err)
			}
			if err := Decode(data, c.out); err != nil {
				t.Fatalf("Decode 失败: %v", err)
			}
		})
	}
}

// TestPingPongRTT 验证 Ping -> Pong 的时间戳回填语义。
func TestPingPongRTT(t *testing.T) {
	ts := int64(1719400000000)
	ping := NewPing("peer-A", ts)
	pong := NewPong(ping)

	if pong.Type != TypePong {
		t.Fatalf("期望 TypePong, 得到 %s", pong.Type)
	}
	if pong.Timestamp != ts {
		t.Fatalf("Pong 应回填原时间戳, 期望 %d 得到 %d", ts, pong.Timestamp)
	}
}

// TestPeekType 验证仅解码外层信封取 Type 的能力（分发场景）。
func TestPeekType(t *testing.T) {
	pk := JoinRoomPacket{
		Packet:   Packet{Type: TypeJoinRoom},
		Room:     "game666",
		NickName: "alice",
	}
	data, err := Encode(pk)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}

	got, err := PeekType(data)
	if err != nil {
		t.Fatalf("PeekType 失败: %v", err)
	}
	if got != TypeJoinRoom {
		t.Fatalf("期望 %s, 得到 %s", TypeJoinRoom, got)
	}
}

// TestPeerInfoPayloadNotEmpty 验证携带原始字节的报文编解码后字节一致。
func TestRelayDataPayloadIntact(t *testing.T) {
	payload := []byte("hello-from-game")
	in := RelayDataPacket{
		Packet:  Packet{Type: TypeRelayData},
		Payload: payload,
	}
	data, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	out := &RelayDataPacket{}
	if err := Decode(data, out); err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}
	if !bytes.Equal(out.Payload, payload) {
		t.Fatalf("Payload 往返不一致: got %v want %v", out.Payload, payload)
	}
}

// TestCompactFrameRoundTrip 验证紧凑二进制帧编解码对称。
func TestCompactFrameRoundTrip(t *testing.T) {
	payload := []byte{0x45, 0x00, 0x00, 0x54, 0xab, 0xcd, 0x00, 0x00, 0x40, 0x01}
	frame := EncodeFrame(FrameP2P, 0x00000002, 0x00000003, payload)

	if !IsCompactFrame(frame) {
		t.Fatalf("IsCompactFrame 应识别自编帧")
	}

	ft, src, dst, p, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("DecodeFrame 失败: %v", err)
	}
	if ft != FrameP2P {
		t.Fatalf("frameType 不一致: got %d want %d", ft, FrameP2P)
	}
	if src != 0x00000002 || dst != 0x00000003 {
		t.Fatalf("VIP 不一致: src=%x dst=%x", src, dst)
	}
	if !bytes.Equal(p, payload) {
		t.Fatalf("payload 不一致: got %v want %v", p, payload)
	}
}

// TestCompactFrameMagicVsJSON 验证 magic 不会与 JSON 起始字节冲突——
// dispatch 的 O(1) 分流靠这条不变量。
func TestCompactFrameMagicVsJSON(t *testing.T) {
	jsonPacket, _ := Encode(NewPing("x", 1))
	if len(jsonPacket) < 1 || jsonPacket[0] != '{' {
		t.Fatalf("JSON 报文应以 { 开头，实际首字节 %#x", jsonPacket[0])
	}
	if IsCompactFrame(jsonPacket) {
		t.Fatalf("JSON 报文不应被识别为紧凑帧")
	}
}

// TestCompactFrameShort 验证短帧/坏 magic 的错误路径。
func TestCompactFrameShort(t *testing.T) {
	if _, _, _, _, err := DecodeFrame([]byte{'N'}); err == nil {
		t.Fatalf("短帧应返回 ErrShortFrame")
	}
	bad := make([]byte, FrameHeaderSize)
	bad[0], bad[1] = 'X', 'Y'
	if _, _, _, _, err := DecodeFrame(bad); err == nil {
		t.Fatalf("坏 magic 应返回 ErrBadMagic")
	}
}
