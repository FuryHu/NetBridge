// 前端版视频帧封装（envelope）编解码，与 protocol/video.go 的 EncodeVideoEnvelope 严格对齐（小端）。
//
// 这是"前端 -> Go"的 IPC 边界格式，与线上的分片子格式不同：前端每产出一帧完整编码视频，
// 连同元信息打包成 envelope 经 Wails 传给 Go，由 Go 切分成多个 FrameVideo 分片发送。
// 接收侧不需要解 envelope--Go 重组后已把 keyframe/ts 作为独立字段随 screen:data 事件下发。

export const VIDEO_CODEC_H264 = 0
export const VIDEO_FLAG_KEYFRAME = 1

const ENVELOPE_HEADER_SIZE = 12

// encodeVideoEnvelope 组装一帧视频的 envelope：12 字节头 + 编码视频字节。
//   [0] codec / [1] flags(bit0=关键帧) / [2..3] frameID / [4..11] ts(微秒) / [12..] frame
export function encodeVideoEnvelope(codec, flags, frameID, ts, frame) {
  const buf = new Uint8Array(ENVELOPE_HEADER_SIZE + frame.length)
  const dv = new DataView(buf.buffer)
  dv.setUint8(0, codec)
  dv.setUint8(1, flags)
  dv.setUint16(2, frameID, true)
  dv.setBigUint64(4, BigInt(ts), true)
  buf.set(frame, ENVELOPE_HEADER_SIZE)
  return buf
}
