package protocol

import "encoding/binary"

// 语音帧 payload 子格式（紧接在 12 字节紧凑帧头之后）。
//
// 语音每秒 50 包/人（20ms/帧），与游戏数据同理走紧凑二进制而非 JSON：
// JSON+base64 膨胀且解析开销大，50pps 下会显著拖累 dispatch。
//
// 为什么单独有 voice 子头而非裸 opus：timestamp 喂给解码器维持单调时间戳；
// seq 预留给未来的丢包重排 / PLC--当前播放端用 nextPlayTime 调度式抖动缓冲，
// 不消费 seq，但保留 2 字节以便后续加 PLC / 重排时无需改协议。
//
// 为什么不在 payload 里重复发言人：帧头的 srcVIP 已经标识了发言人。
// 这是为未来 SFU 留的口子--服务端可按 srcVIP 识别流并扇出，无需改协议。
//
// 子头布局（小端，固定 11 字节 + 音频数据）：
//
//	[0]       codec     0=PCM 降级 / 1=Opus 主路径
//	[1..2]    seqNum    uint16，序列号（预留：未来丢包重排 / PLC，当前未消费）
//	[3..10]   timestamp uint64，采集时间戳（微秒），喂给解码器维持单调时间戳
//	[11..]    audioData Opus 字节或裸 PCM
const (
	VoiceCodecPCM  byte = 0 // 降级：裸 PCM（WebView2 不支持 WebCodecs 时）
	VoiceCodecOpus byte = 1 // 主路径：Opus

	VoiceHeaderSize = 11
)

// EncodeVoicePayload 把一帧音频封装成 voice 子格式 payload。
// 返回值应塞进紧凑帧（FrameVoice）的 payload 段后整体发送。
func EncodeVoicePayload(codec byte, seqNum uint16, ts uint64, audio []byte) []byte {
	buf := make([]byte, VoiceHeaderSize+len(audio))
	buf[0] = codec
	binary.LittleEndian.PutUint16(buf[1:3], seqNum)
	binary.LittleEndian.PutUint64(buf[3:11], ts)
	copy(buf[VoiceHeaderSize:], audio)
	return buf
}

// DecodeVoicePayload 解析 voice 子格式 payload（零拷贝：audio 引用 data 尾段）。
// 调用方若要跨 goroutine 保留 audio 需自行 copy。
func DecodeVoicePayload(data []byte) (codec byte, seqNum uint16, ts uint64, audio []byte, err error) {
	if len(data) < VoiceHeaderSize {
		err = ErrShortFrame
		return
	}
	codec = data[0]
	seqNum = binary.LittleEndian.Uint16(data[1:3])
	ts = binary.LittleEndian.Uint64(data[3:11])
	audio = data[VoiceHeaderSize:]
	return
}
