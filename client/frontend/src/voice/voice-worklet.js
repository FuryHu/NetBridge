// AudioWorklet processor：在独立线程从麦克风读 PCM。
// 攒满一帧（20ms @ 48kHz = 960 samples）后用 transferable ArrayBuffer 发出，零拷贝。
// 同时计算该帧 RMS 音量，随帧一起 postMessage，供 UI 显示本地电平条。
//
// 之所以用 AudioWorklet 而非废弃的 ScriptProcessor：前者跑在独立线程，
// 不会被主线程卡顿拖累采集，避免 mic stutter。

class VoiceProcessor extends AudioWorkletProcessor {
  constructor() {
    super()
    this.frameSize = 960 // 20ms @ 48kHz
    this.buf = new Float32Array(this.frameSize)
    this.offset = 0
  }

  process(inputs) {
    const input = inputs[0]
    if (!input || !input[0]) return true
    const ch = input[0]
    let i = 0
    while (i < ch.length) {
      const need = this.frameSize - this.offset
      const take = Math.min(need, ch.length - i)
      this.buf.set(ch.subarray(i, i + take), this.offset)
      this.offset += take
      i += take
      if (this.offset >= this.frameSize) {
        // RMS 音量：sqrt(mean(x^2))，归一化到 0~1（×2.5 增益便于观察）。
        let sum = 0
        for (let k = 0; k < this.frameSize; k++) sum += this.buf[k] * this.buf[k]
        let level = Math.min(1, Math.sqrt(sum / this.frameSize) * 2.5)
        if (level < 0.12) level = 0 // 噪声门：滤电底噪（0.12 可调，仍闪调高，滤说话调低）
        const out = new Float32Array(this.buf)
        this.port.postMessage({ pcm: out, level }, [out.buffer])
        this.offset = 0
      }
    }
    return true
  }
}

registerProcessor('voice-processor', VoiceProcessor)
