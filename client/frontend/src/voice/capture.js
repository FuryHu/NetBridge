// 麦克风采集：getUserMedia + AudioWorklet，每 20ms 产出一帧 Float32 PCM（960 samples @ 48kHz）+ 该帧音量。
//
// 浏览器内置 echoCancellation / noiseSuppression / autoGainControl 默认开启--
// 这三个自研质量远不如浏览器，本项目不自研。
//
// AudioWorkletNode 不连 destination：避免把自己的麦克风回放出来形成回路。

export async function createCapture(onFrame) {
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: {
      echoCancellation: true,
      noiseSuppression: true,
      autoGainControl: true,
      channelCount: 1,
    },
    video: false,
  })

  const ctx = new AudioContext({ sampleRate: 48000 })
  if (ctx.state === 'suspended') await ctx.resume()
  await ctx.audioWorklet.addModule(new URL('./voice-worklet.js', import.meta.url))

  const src = ctx.createMediaStreamSource(stream)
  const node = new AudioWorkletNode(ctx, 'voice-processor')
  src.connect(node)
  node.port.onmessage = (e) => onFrame(new Float32Array(e.data.pcm), e.data.level)

  return {
    close() {
      node.port.onmessage = null
      try { node.disconnect() } catch (e) {}
      try { src.disconnect() } catch (e) {}
      stream.getTracks().forEach((t) => t.stop())
      try { ctx.close() } catch (e) {}
    },
  }
}
