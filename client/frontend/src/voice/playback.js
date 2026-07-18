// 解码 + 播放：按 srcVIP 维护独立播放流。
//
// 抖动缓冲用"调度式"：跟踪每个流的 nextPlayTime，收到解码 PCM 后 start(nextPlayTime)。
// 早到的包排队、迟到的包重置起播点--天然吸收网络抖动。
//
// 解码后顺带计算 RMS 回调 onPeerLevel(srcVIP, level)（成员音量条）。
// 每个 player 持有本地播放增益 vol（setPeerVolume 调，只影响自己听多大）。

import { decodeVoicePayload } from './protocol.js'

const OPUS = 1

export async function createPlayback(onPeerLevel) {
  const ctx = new AudioContext({ sampleRate: 48000 })
  // 与 capture 一致：suspended 时尝试 resume。autoStartVoice 走 self:update（非用户手势），
  // 不 resume 的话自动进房可能拿到一个挂起的 ctx -> 听不到对方声音。
  async function resume() {
    if (ctx.state === 'suspended') { try { await ctx.resume() } catch (e) {} }
  }
  await resume()
  const players = new Map() // srcVIP -> {decoder, nextPlayTime, codec, srcVIP, vol}

  function getPlayer(srcVIP, codec) {
    let p = players.get(srcVIP)
    if (p) {
      if (p.codec !== codec) {
        try { p.decoder && p.decoder.close() } catch (e) {}
        p.decoder = null
        p.codec = codec
        if (codec === OPUS) createOpusDecoder(p)
      }
      return p
    }
    p = { decoder: null, nextPlayTime: 0, codec, srcVIP, vol: 1 }
    players.set(srcVIP, p)
    if (codec === OPUS) createOpusDecoder(p)
    return p
  }

  function createOpusDecoder(p) {
    p.decoder = new AudioDecoder({
      output: (ad) => onDecoded(p, ad),
      error: (e) => console.error('[voice] decode error:', e),
    })
    p.decoder.configure({ codec: 'opus', sampleRate: 48000, numberOfChannels: 1 })
  }

  // rmsLevel：RMS 音量 0~1；低于噪声门视为 0（滤底噪）。
  function rmsLevel(ch) {
    let sum = 0
    for (let i = 0; i < ch.length; i++) sum += ch[i] * ch[i]
    const level = Math.min(1, Math.sqrt(sum / ch.length) * 2.5)
    return level < 0.08 ? 0 : level
  }

  function onDecoded(p, audioData) {
    const buf = ctx.createBuffer(1, audioData.numberOfFrames, audioData.sampleRate)
    const ch = buf.getChannelData(0)
    audioData.copyTo(ch, { planeIndex: 0 })
    audioData.close()
    if (onPeerLevel) onPeerLevel(p.srcVIP, rmsLevel(ch)) // 原始电平（不受 vol 影响）
    schedule(p, buf)
  }

  function schedule(p, buf) {
    // 应用该 peer 的本地播放音量（vol 0~1，默认 1）
    if (p.vol !== 1) {
      const ch = buf.getChannelData(0)
      for (let i = 0; i < ch.length; i++) ch[i] *= p.vol
    }
    const src = ctx.createBufferSource()
    src.buffer = buf
    src.connect(ctx.destination)
    const now = ctx.currentTime
    if (p.nextPlayTime < now || p.nextPlayTime > now + 0.3) {
      p.nextPlayTime = now + 0.05
    }
    src.start(p.nextPlayTime)
    p.nextPlayTime += buf.duration
  }

  function handleVoice(srcVIP, rawPayload) {
    const { codec, ts, audio } = decodeVoicePayload(rawPayload)
    if (!audio || audio.length === 0) return
    const p = getPlayer(srcVIP, codec)
    if (codec === OPUS) {
      const chunk = new EncodedAudioChunk({ type: 'key', timestamp: ts, data: audio })
      p.decoder.decode(chunk)
    } else {
      // audio 是 subarray 视图，byteOffset = VOICE_HEADER_SIZE（11，奇数），
      // 直接 new Int16Array(audio.buffer, byteOffset, ...) 会因未 2 字节对齐抛 RangeError，先拷贝到对齐 buffer。
      const bytes = new Uint8Array(audio)
      const i16 = new Int16Array(bytes.buffer)
      const f32 = new Float32Array(i16.length * 3)
      for (let i = 0; i < i16.length; i++) {
        const s = i16[i] / 0x8000
        f32[i * 3] = s
        f32[i * 3 + 1] = s
        f32[i * 3 + 2] = s
      }
      if (onPeerLevel) onPeerLevel(srcVIP, rmsLevel(f32))
      const buf = ctx.createBuffer(1, f32.length, 48000)
      buf.getChannelData(0).set(f32)
      schedule(p, buf)
    }
  }

  // setPeerVolume 设置某 peer 的本地播放增益。player 不存在时建占位（收到帧后补全 codec/decoder）。
  function setPeerVolume(srcVIP, vol) {
    let p = players.get(srcVIP)
    if (!p) {
      p = { decoder: null, nextPlayTime: 0, codec: 0, srcVIP, vol: 1 }
      players.set(srcVIP, p)
    }
    p.vol = vol
  }

  function removePeer(srcVIP) {
    const p = players.get(srcVIP)
    if (p) {
      try { p.decoder && p.decoder.close() } catch (e) {}
      players.delete(srcVIP)
    }
  }

  function close() {
    for (const [, p] of players) {
      try { p.decoder && p.decoder.close() } catch (e) {}
    }
    players.clear()
    try { ctx.close() } catch (e) {}
  }

  return { handleVoice, removePeer, close, setPeerVolume, resume }
}
