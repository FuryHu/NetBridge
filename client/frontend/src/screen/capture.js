// 屏幕采集：getDisplayMedia 取屏幕流 -> 隐藏 <video> -> canvas 等比缩放 -> VideoFrame。
//
// 不用 MediaStreamTrackProcessor（部分 WebView2 版本未必开启），改走 canvas 路径，
// 仅依赖 getDisplayMedia / <video> / drawImage / VideoFrame(canvas)，兼容性最稳。
// 顺带在 canvas 阶段等比缩放到 maxWidth，从源头限制编码分辨率与码率。
//
// 采集循环用 setInterval 驱动（非 requestAnimationFrame）：窗口最小化时
// document.visibilityState 变 hidden，rAF 回调会被完全暂停，导致采集中断、对端画面卡死；
// setInterval 配合活跃的 getDisplayMedia 流不受此限，最小化仍可持续采集发送。

export async function createCapture(onFrame, onEnded, opts = {}) {
  const frameRate = opts.frameRate ?? 24
  let maxWidth = opts.maxWidth ?? 1920 // 采集缩放上界（宽），可运行中 setMaxWidth 动态调整

  const stream = await navigator.mediaDevices.getDisplayMedia({
    video: { frameRate },
    audio: false,
  })
  const track = stream.getVideoTracks()[0]

  const videoEl = document.createElement('video')
  videoEl.srcObject = stream
  videoEl.muted = true
  videoEl.playsInline = true
  await videoEl.play()

  const canvas = document.createElement('canvas')
  const ctx = canvas.getContext('2d', { alpha: false })

  let timerId = 0
  let lastTs = 0
  const intervalUs = 1e6 / frameRate // 微秒，节流用
  const intervalMs = 1000 / frameRate

  // 用户点了浏览器/系统的"停止共享"条 -> 通知上层（UI 回到未投屏态）。
  track.addEventListener('ended', () => {
    if (onEnded) onEnded()
  })

  function tick() {
    if (videoEl.readyState < 2) return
    const now = performance.now() * 1000
    if (now - lastTs < intervalUs) return // 防抖动重入
    lastTs = now

    const sw = videoEl.videoWidth
    const sh = videoEl.videoHeight
    if (sw === 0 || sh === 0) return
    const scale = Math.min(1, maxWidth / sw)
    const dw = Math.max(2, Math.round(sw * scale))
    const dh = Math.max(2, Math.round(sh * scale))
    if (canvas.width !== dw || canvas.height !== dh) {
      canvas.width = dw
      canvas.height = dh
    }
    ctx.drawImage(videoEl, 0, 0, dw, dh)
    const frame = new VideoFrame(canvas, { timestamp: now })
    onFrame(frame)
    frame.close()
  }
  timerId = setInterval(tick, intervalMs)

  return {
    setMaxWidth(w) { maxWidth = w || 1920 },
    close() {
      clearInterval(timerId)
      try { track.stop() } catch (e) {}
      videoEl.srcObject = null
    },
  }
}
