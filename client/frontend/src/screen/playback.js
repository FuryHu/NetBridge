// 解码 + 渲染：按 srcVIP 维护独立 VideoDecoder，解码后 drawImage 到共用 canvas。
//
// 与 voice/playback.js 同构，但视频无需抖动缓冲--直接绘最新帧即可（迟到的帧丢弃更省）。
// 解码器须等关键帧才能起播：未配置或非关键帧时丢弃，直到首个关键帧到达。
//
// 单 canvas 共用：典型"一人投屏、多人观看"场景只有一个发言人，够用；
// 多人同时投屏时后到帧覆盖前者（已知局限，演示场景可接受）。
//
// 末帧清理：投屏方停止发送时无结束信令，观看端会停在最后一帧。用超时检测（STALE_MS
// 内无新帧）清空 canvas 并 onIdle 通知上层隐藏画面区；源离开（removePeer）时同样清理。

const H264_CODEC = 'avc1.640033'
const STALE_MS = 2500 // 超过此时长无新帧视为该源已停止

export async function createPlayback(canvas, onIdle) {
  const ctx = canvas.getContext('2d', { alpha: false })
  const decoders = new Map() // srcVIP -> VideoDecoder
  let activeSrc = null // 当前正在显示画面的源
  let lastFrameAt = 0

  function clearCanvas() {
    if (canvas.width > 0 && canvas.height > 0) {
      ctx.fillStyle = '#000'
      ctx.fillRect(0, 0, canvas.width, canvas.height)
    }
  }

  function getDecoder(srcVIP) {
    let d = decoders.get(srcVIP)
    if (d) return d
    d = { decoder: null, seenKey: false }
    d.decoder = new VideoDecoder({
      output: (frame) => {
        drawFrame(frame)
        frame.close()
      },
      error: (e) => {
        // 缺关键帧参考等解码错误会使解码器进入坏态：重置+重配，等下一个关键帧重新起播。
        console.error('[screen] decode error:', e)
        try { d.decoder.reset(); d.decoder.configure({ codec: H264_CODEC }) } catch (_) {}
        d.seenKey = false
      },
    })
    d.decoder.configure({ codec: H264_CODEC })
    decoders.set(srcVIP, d)
    return d
  }

  function drawFrame(frame) {
    if (canvas.width !== frame.displayWidth) canvas.width = frame.displayWidth
    if (canvas.height !== frame.displayHeight) canvas.height = frame.displayHeight
    ctx.drawImage(frame, 0, 0, canvas.width, canvas.height)
  }

  function handleVideo(srcVIP, data, keyframe, ts) {
    if (!data || data.length === 0) return
    const d = getDecoder(srcVIP)
    // 起播前丢弃 delta：解码器须等首个关键帧才能正确解码（中途加入的观看者）。
    if (!d.seenKey && !keyframe) return
    if (keyframe) d.seenKey = true
    if (d.decoder.decodeQueueSize > 8 && !keyframe) return // 积压过多且非关键帧，丢帧防堆积
    const chunk = new EncodedVideoChunk({ type: keyframe ? 'key' : 'delta', timestamp: ts, data })
    try {
      d.decoder.decode(chunk)
    } catch (e) {
      console.warn('[screen] decode 抛错（可能缺关键帧）:', e)
      return
    }
    activeSrc = srcVIP
    lastFrameAt = performance.now()
  }

  function removePeer(srcVIP) {
    const d = decoders.get(srcVIP)
    if (d) {
      try { d.decoder.close() } catch (e) {}
      decoders.delete(srcVIP)
    }
    // 移除的正是当前显示源：清掉卡住的末帧，通知上层回到无画面态。
    if (activeSrc === srcVIP) {
      activeSrc = null
      clearCanvas()
      if (onIdle) onIdle()
    }
  }

  // 超时检测：投屏方停止发送（无结束信令）后清掉末帧，避免画面卡死在最后一帧。
  const staleTimer = setInterval(() => {
    if (activeSrc != null && performance.now() - lastFrameAt > STALE_MS) {
      const d = decoders.get(activeSrc)
      if (d) {
        try { d.decoder.close() } catch (e) {}
        decoders.delete(activeSrc)
      }
      activeSrc = null
      clearCanvas()
      if (onIdle) onIdle()
    }
  }, 1000)

  function close() {
    clearInterval(staleTimer)
    for (const [, d] of decoders) {
      try { d.decoder.close() } catch (e) {}
    }
    decoders.clear()
  }

  return { handleVideo, removePeer, close }
}
