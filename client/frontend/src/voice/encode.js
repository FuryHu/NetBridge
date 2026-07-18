// 编码：WebCodecs AudioEncoder (Opus) 优先；能力检测降级为裸 PCM（16kHz 16bit）。
//
// 降级路径带宽约 256kbps（Opus 32kbps 的 8 倍），仅作 WebView2 旧版兜底，
// 并打日志提示用户升级 WebView2 Runtime 以获得 Opus。
//
// Opus 编码是异步的，但 output 严格按 encode 调用顺序产出（FIFO），
// 因此用 pending 队列配对入队/出队的 seq、ts，保证确定性。

const OPUS_BITRATE = 32000

export async function createEncoder(onEncoded) {
  // onEncoded({codec, seq, ts, data})
  if (typeof AudioEncoder !== 'undefined') {
    try {
      return createOpusEncoder(onEncoded)
    } catch (e) {
      console.warn('[voice] Opus 编码器创建失败，降级 PCM:', e)
    }
  } else {
    console.warn('[voice] WebView2 不支持 WebCodecs，降级裸 PCM（~256kbps）。建议升级 WebView2 Runtime 以启用 Opus。')
  }
  return createPcmEncoder(onEncoded)
}

function createOpusEncoder(onEncoded) {
  let seq = 0
  let frameIdx = 0
  const pending = []

  const encoder = new AudioEncoder({
    output: (chunk) => {
      const item = pending.shift()
      if (!item) return
      const data = new Uint8Array(chunk.byteLength)
      chunk.copyTo(data)
      onEncoded({ codec: 1, seq: item.seq, ts: item.ts, data })
    },
    error: (e) => console.error('[voice] encoder error:', e),
  })
  encoder.configure({
    codec: 'opus',
    sampleRate: 48000,
    numberOfChannels: 1,
    bitrate: OPUS_BITRATE,
  })

  return {
    codec: 1,
    encode(pcmFloat32) {
      const ts = frameIdx * 20000 // 微秒，20ms/帧
      frameIdx++
      pending.push({ seq, ts })
      const ad = new AudioData({
        format: 'f32-planar',
        sampleRate: 48000,
        numberOfFrames: pcmFloat32.length,
        numberOfChannels: 1,
        timestamp: ts,
        data: pcmFloat32,
      })
      encoder.encode(ad, { keyFrame: false })
      ad.close()
      seq++
    },
    close() {
      try { encoder.close() } catch (e) {}
    },
  }
}

function createPcmEncoder(onEncoded) {
  let seq = 0
  let frameIdx = 0
  return {
    codec: 0,
    encode(pcmFloat32) {
      // 降采样 48k -> 16k（每 3 样本取 1），16bit 单声道。
      const n = Math.floor(pcmFloat32.length / 3)
      const i16 = new Int16Array(n)
      for (let i = 0; i < n; i++) {
        let s = pcmFloat32[i * 3]
        s = Math.max(-1, Math.min(1, s))
        i16[i] = s < 0 ? s * 0x8000 : s * 0x7fff
      }
      onEncoded({ codec: 0, seq: seq++, ts: frameIdx * 20000, data: new Uint8Array(i16.buffer) })
      frameIdx++
    },
    close() {},
  }
}
