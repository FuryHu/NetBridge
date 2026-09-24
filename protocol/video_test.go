package protocol

import (
	"bytes"
	"math/rand"
	"testing"
)

// decodeCopy 解码分片并复制 Data，模拟 client.handleCompactFrame 对 payload 的零拷贝保护。
func decodeCopy(f []byte) VideoFragment {
	d, err := DecodeVideoFragment(f)
	if err != nil {
		panic(err)
	}
	d.Data = append([]byte(nil), d.Data...)
	return d
}

// makeFrame 生成确定内容的伪帧字节。
func makeFrame(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

// TestVideoFragmentRoundTrip 验证单分片编解码对称。
func TestVideoFragmentRoundTrip(t *testing.T) {
	data := []byte{0x00, 0x01, 0x65, 0x88, 0x00, 0x00, 0xAF, 0xE0}
	frag := EncodeVideoFragment(VideoCodecH264, VideoFlagKeyframe, 42, 3, 1, 1719400000, data)

	got, err := DecodeVideoFragment(frag)
	if err != nil {
		t.Fatalf("DecodeVideoFragment 失败: %v", err)
	}
	if got.Codec != VideoCodecH264 || got.Flags != VideoFlagKeyframe {
		t.Fatalf("codec/flags 不一致: %+v", got)
	}
	if got.FrameID != 42 || got.FragCount != 3 || got.FragIdx != 1 {
		t.Fatalf("frameID/fragCount/fragIdx 不一致: %+v", got)
	}
	if got.Timestamp != 1719400000 {
		t.Fatalf("timestamp 不一致: %d", got.Timestamp)
	}
	if !bytes.Equal(got.Data, data) {
		t.Fatalf("data 不一致: got %v want %v", got.Data, data)
	}
}

// TestVideoFragmentShort 验证短 payload 错误路径。
func TestVideoFragmentShort(t *testing.T) {
	if _, err := DecodeVideoFragment(make([]byte, VideoFragHeaderSize-1)); err == nil {
		t.Fatalf("短 payload 应返回 ErrShortFrame")
	}
}

// TestVideoFragmentsReassemble 验证切分->逐片解码->顺序重组还原完整帧。
func TestVideoFragmentsReassemble(t *testing.T) {
	frame := makeFrame(8192)
	frags := EncodeVideoFragments(VideoCodecH264, VideoFlagKeyframe, 7, 123456, frame, 1372)

	r := NewVideoReassembler()
	for i, f := range frags {
		assembled, codec, flags, ts, ok := r.Push(decodeCopy(f))
		if ok {
			if i != len(frags)-1 {
				t.Fatalf("提前在第 %d 片完成重组", i)
			}
			if codec != VideoCodecH264 || flags != VideoFlagKeyframe {
				t.Fatalf("重组帧 codec/flags 不一致: %d/%d", codec, flags)
			}
			if ts != 123456 {
				t.Fatalf("timestamp 不一致: %d", ts)
			}
			if !bytes.Equal(assembled, frame) {
				t.Fatalf("重组帧字节不一致: got %d bytes want %d", len(assembled), len(frame))
			}
		}
	}
}

// TestVideoReassembleOutOfOrder 验证乱序到达仍能正确重组。
func TestVideoReassembleOutOfOrder(t *testing.T) {
	frame := makeFrame(4096)
	frags := EncodeVideoFragments(VideoCodecH264, 0, 1, 999, frame, 1000)

	perm := rand.Perm(len(frags))
	r := NewVideoReassembler()
	var assembled []byte
	for _, i := range perm {
		a, _, _, _, ok := r.Push(decodeCopy(frags[i]))
		if ok {
			assembled = a
		}
	}
	if assembled == nil {
		t.Fatalf("乱序投递后应完成重组")
	}
	if !bytes.Equal(assembled, frame) {
		t.Fatalf("乱序重组字节不一致")
	}
}

// TestVideoReassembleMissingFragment 验证丢一片后该帧永不完成，且不阻塞后续完整帧。
func TestVideoReassembleMissingFragment(t *testing.T) {
	frame1 := makeFrame(2048)
	frame2 := makeFrame(1024)
	frags1 := EncodeVideoFragments(VideoCodecH264, VideoFlagKeyframe, 1, 100, frame1, 500) // 5 片
	frags2 := EncodeVideoFragments(VideoCodecH264, VideoFlagKeyframe, 2, 200, frame2, 500) // 3 片

	r := NewVideoReassembler()
	// 投 frame1 但跳过第 2 片（模拟丢包），该帧应永不完成。
	for i, f := range frags1 {
		if i == 2 {
			continue
		}
		if _, _, _, _, ok := r.Push(decodeCopy(f)); ok {
			t.Fatalf("丢片的帧不应完成重组")
		}
	}
	// 投 frame2 全部，应正常完成且字节正确。
	var got []byte
	for _, f := range frags2 {
		a, _, _, _, ok := r.Push(decodeCopy(f))
		if ok {
			got = a
		}
	}
	if got == nil || !bytes.Equal(got, frame2) {
		t.Fatalf("后续完整帧应正常重组")
	}
}

// TestVideoReassembleDuplicateFragment 验证重复分片被忽略、不重复计数（否则 got 会超 fragCount 永不完成）。
func TestVideoReassembleDuplicateFragment(t *testing.T) {
	frame := makeFrame(1500)
	frags := EncodeVideoFragments(VideoCodecH264, 0, 9, 5, frame, 500) // 3 片
	r := NewVideoReassembler()

	r.Push(decodeCopy(frags[0]))
	r.Push(decodeCopy(frags[1]))
	r.Push(decodeCopy(frags[0])) // 重复第 0 片，应被忽略，got 不增
	assembled, _, _, _, ok := r.Push(decodeCopy(frags[2]))

	if !ok {
		t.Fatalf("含重复分片时最后一片应触发重组完成")
	}
	if !bytes.Equal(assembled, frame) {
		t.Fatalf("重组字节不一致")
	}
}
