// 前端版 voice 子格式编解码，与 protocol/voice.go 严格对齐（小端）。
//
// 复用帧头的 srcVIP 标识发言人，payload 内只放 codec/seq/ts/audio。
// 这是为未来 SFU 留的口子：服务端可按 srcVIP 识别并扇出语音流，协议不动。

export const VOICE_CODEC_PCM = 0
export const VOICE_CODEC_OPUS = 1

const VOICE_HEADER_SIZE = 11

// encodeVoicePayload 组装一帧 voice payload：11 字节子头 + 音频数据。
// ts 为微秒（uint64），用 BigInt 写入避免 32 位约 71 分钟回绕。
export function encodeVoicePayload(codec, seqNum, ts, audio) {
  const buf = new Uint8Array(VOICE_HEADER_SIZE + audio.length)
  const dv = new DataView(buf.buffer)
  dv.setUint8(0, codec)
  dv.setUint16(1, seqNum, true)
  dv.setBigUint64(3, BigInt(ts), true)
  buf.set(audio, VOICE_HEADER_SIZE)
  return buf
}

// decodeVoicePayload 解析 voice payload。audio 是 data 的尾段视图（零拷贝）。
// data 可能是 subarray（byteOffset 非 0），DataView 必须带上 byteOffset/length。
export function decodeVoicePayload(data) {
  if (!data || data.length < VOICE_HEADER_SIZE) {
    return { codec: 0, seqNum: 0, ts: 0, audio: new Uint8Array(0) }
  }
  const dv = new DataView(data.buffer, data.byteOffset, data.length)
  const codec = dv.getUint8(0)
  const seqNum = dv.getUint16(1, true)
  // 转回 Number（微秒）供 EncodedAudioChunk.timestamp 使用，实际帧序远不会触及 Number 精度上限。
  const ts = Number(dv.getBigUint64(3, true))
  const audio = data.subarray(VOICE_HEADER_SIZE)
  return { codec, seqNum, ts, audio }
}
