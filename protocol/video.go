package protocol

import (
	"bytes"
	"encoding/binary"
)

// 视频分片子格式（紧接在 12 字节紧凑帧头 FrameVideo 之后）。
//
// 语音 Opus 一帧 ~80 字节，单个 UDP 包足够；视频则不同--一帧 H.264 关键帧常达
// 数十 KB，远超 DefaultMTU(1400)。若整帧塞进一个 UDP 包会被 IP 层分片，而 IP
// 分片在公网丢包率极高（任一分片丢失则整帧作废）。故一帧编码视频由发送端拆成
// 多个分片，每个分片单独封进一个 FrameVideo 包发出，接收端按 frameID 重组。
//
// 子头布局（小端，固定 16 字节 + 分片数据）：
//
//	[0]      codec     0=H.264(AVC)
//	[1]      flags     bit0 = 关键帧（keyframe）；解码器须从关键帧起播
//	[2..3]   frameID   uint16，同一逻辑帧的所有分片共用，重组依据
//	[4..5]   fragCount uint16，该帧总分片数
//	[6..7]   fragIdx   uint16，本分片序号（0 起）
//	[8..15]  timestamp uint64，采集时间戳（微秒），喂给解码器维持单调时间戳
//	[16..]   fragData  本分片承载的编码视频字节
const (
	VideoCodecH264 byte = 0 // 主路径：H.264/AVC（WebView2 硬件支持最广）

	VideoFlagKeyframe byte = 1 << 0 // 关键帧标志；新成员中途加入需等待关键帧才能起播

	VideoFragHeaderSize = 16

	// VideoMaxFragData 单个分片承载的编码视频上限：留出紧凑帧头(FrameHeaderSize)与
	// 视频子头(VideoFragHeaderSize)的余量，使单个 FrameVideo 包不超过 DefaultMTU，
	// 避免 IP 层分片丢包。= 1400 - 12 - 16 = 1372。
	VideoMaxFragData = DefaultMTU - FrameHeaderSize - VideoFragHeaderSize
)

// VideoFragment 是一个视频分片的解析结果。Data 为零拷贝视图，跨 goroutine 保留需自行 copy。
type VideoFragment struct {
	Codec     byte
	Flags     byte
	FrameID   uint16
	FragCount uint16
	FragIdx   uint16
	Timestamp uint64
	Data      []byte
}

// EncodeVideoFragment 把一个分片封装成视频子格式 payload。
// 返回值应塞进紧凑帧（FrameVideo）的 payload 段后整体发送。
func EncodeVideoFragment(codec, flags byte, frameID, fragCount, fragIdx uint16, ts uint64, fragData []byte) []byte {
	buf := make([]byte, VideoFragHeaderSize+len(fragData))
	buf[0] = codec
	buf[1] = flags
	binary.LittleEndian.PutUint16(buf[2:4], frameID)
	binary.LittleEndian.PutUint16(buf[4:6], fragCount)
	binary.LittleEndian.PutUint16(buf[6:8], fragIdx)
	binary.LittleEndian.PutUint64(buf[8:16], ts)
	copy(buf[VideoFragHeaderSize:], fragData)
	return buf
}

// DecodeVideoFragment 解析视频子格式 payload（零拷贝：Data 引用 data 尾段）。
func DecodeVideoFragment(data []byte) (VideoFragment, error) {
	if len(data) < VideoFragHeaderSize {
		return VideoFragment{}, ErrShortFrame
	}
	return VideoFragment{
		Codec:     data[0],
		Flags:     data[1],
		FrameID:   binary.LittleEndian.Uint16(data[2:4]),
		FragCount: binary.LittleEndian.Uint16(data[4:6]),
		FragIdx:   binary.LittleEndian.Uint16(data[6:8]),
		Timestamp: binary.LittleEndian.Uint64(data[8:16]),
		Data:      data[VideoFragHeaderSize:],
	}, nil
}

// EncodeVideoFragments 把一帧完整的编码视频按 maxFragSize 切成多个分片 payload。
// frameID 由调用方分配（逐帧递增），同帧所有分片共用。maxFragSize 是单分片数据上限，
// 应小于 (DefaultMTU - FrameHeaderSize - VideoFragHeaderSize) 以保证不超 UDP MTU。
// 返回的每个 []byte 各自封进一个 FrameVideo 包发出；空帧也至少产出 1 个分片。
func EncodeVideoFragments(codec, flags byte, frameID uint16, ts uint64, frame []byte, maxFragSize int) [][]byte {
	if maxFragSize <= 0 {
		maxFragSize = 1
	}
	n := (len(frame) + maxFragSize - 1) / maxFragSize
	if n == 0 {
		n = 1
	}
	frags := make([][]byte, n)
	for i := 0; i < n; i++ {
		start := i * maxFragSize
		end := start + maxFragSize
		if end > len(frame) {
			end = len(frame)
		}
		frags[i] = EncodeVideoFragment(codec, flags, frameID, uint16(n), uint16(i), ts, frame[start:end])
	}
	return frags
}

// 视频帧封装（VideoEnvelope）：前端 -> Go 的 IPC 边界格式，与线上的分片子格式不同。
//
// 前端每产出一帧完整编码视频，连同元信息打包成 envelope 经 Wails 传给 Go，由 Go 在发送侧
// 切分成多个 FrameVideo 分片。这与语音的"一帧一次 SendVoiceToAll"对齐：高频 IPC 只发生在
// 每帧一次，而非每分片一次。
//
// 布局（小端，固定 12 字节头 + 帧数据）：
//
//	[0]      codec     0=H.264
//	[1]      flags     bit0 = 关键帧
//	[2..3]   frameID   uint16（逐帧递增，接收端重组依据）
//	[4..11]  ts        uint64（微秒，采集时间戳）
//	[12..]   frame     完整编码视频字节
const VideoEnvelopeHeaderSize = 12

// EncodeVideoEnvelope 把一帧完整视频 + 元信息封装成 envelope payload（前端 -> Go）。
func EncodeVideoEnvelope(codec, flags byte, frameID uint16, ts uint64, frame []byte) []byte {
	buf := make([]byte, VideoEnvelopeHeaderSize+len(frame))
	buf[0] = codec
	buf[1] = flags
	binary.LittleEndian.PutUint16(buf[2:4], frameID)
	binary.LittleEndian.PutUint64(buf[4:12], ts)
	copy(buf[VideoEnvelopeHeaderSize:], frame)
	return buf
}

// DecodeVideoEnvelope 解析 envelope（零拷贝：frame 引用 data 尾段）。
func DecodeVideoEnvelope(data []byte) (codec, flags byte, frameID uint16, ts uint64, frame []byte, err error) {
	if len(data) < VideoEnvelopeHeaderSize {
		err = ErrShortFrame
		return
	}
	codec = data[0]
	flags = data[1]
	frameID = binary.LittleEndian.Uint16(data[2:4])
	ts = binary.LittleEndian.Uint64(data[4:12])
	frame = data[VideoEnvelopeHeaderSize:]
	return
}

// VideoReassembler 按 frameID 收集分片，收齐后产出完整帧。
// 每个接收端 peer 应持有一个独立实例。非线程安全--调用方须串行化（如单 goroutine 消费）。
type VideoReassembler struct {
	frames map[uint16]*inFlightFrame
}

type inFlightFrame struct {
	codec     byte
	flags     byte
	ts        uint64
	fragCount uint16
	frags     [][]byte
	got       int
}

const (
	videoMaxInFlight = 64 // 最多同时重组的帧数，超限触发陈旧清理
	videoStaleWindow = 32 // frameID 落后当前超过此值视为陈旧丢失，丢弃
)

// NewVideoReassembler 创建重组器。
func NewVideoReassembler() *VideoReassembler {
	return &VideoReassembler{frames: make(map[uint16]*inFlightFrame)}
}

// Push 投入一个分片。当该 frameID 的全部分片到齐时，返回 ok=true 与重组后的完整帧字节
// （bytes.Join 后为独立新切片，与各分片视图无关）。乱序到达允许；重复分片忽略；fragIdx 越界丢弃。
func (r *VideoReassembler) Push(frag VideoFragment) (assembled []byte, codec, flags byte, ts uint64, ok bool) {
	f, exists := r.frames[frag.FrameID]
	if !exists {
		// 新帧到达前先清理明显已丢失的旧帧，防内存膨胀。
		r.evictStale(frag.FrameID)
		if frag.FragCount == 0 {
			return // 非法
		}
		f = &inFlightFrame{
			codec:     frag.Codec,
			flags:     frag.Flags,
			ts:        frag.Timestamp,
			fragCount: frag.FragCount,
			frags:     make([][]byte, frag.FragCount),
		}
		r.frames[frag.FrameID] = f
	}
	if int(frag.FragIdx) >= len(f.frags) {
		return // 越界分片，丢弃
	}
	if f.frags[frag.FragIdx] == nil {
		f.frags[frag.FragIdx] = frag.Data
		f.got++
	}
	if f.got == int(f.fragCount) {
		assembled = bytes.Join(f.frags, nil)
		codec, flags, ts = f.codec, f.flags, f.ts
		delete(r.frames, frag.FrameID)
		ok = true
	}
	return
}

// evictStale 丢弃明显落后于当前 frameID 的在途帧。int16(current-fid) 在 uint16 序列空间
// 中做 wraparound 感知的"先后"比较（TCP 序号风格）：正值表示 fid 落后 current。
func (r *VideoReassembler) evictStale(current uint16) {
	for fid := range r.frames {
		if fid != current && int16(current-fid) > videoStaleWindow {
			delete(r.frames, fid)
		}
	}
	// 极端丢包场景仍超限：除当前外整体清空，代价是丢一批在途帧，但避免无限增长。
	if len(r.frames) >= videoMaxInFlight {
		for fid := range r.frames {
			if fid != current {
				delete(r.frames, fid)
			}
		}
	}
}
