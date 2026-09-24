// 编码：WebCodecs VideoEncoder (H.264/AVC)。与 voice/encode.js 同构。
//
// 首帧强制关键帧（解码器须从关键帧起播），之后每 keyframeInterval 帧一个关键帧（~2s），
// 供中途加入的观看者起播。
//
// 编码器在首帧到达时按实际分辨率 configure（屏幕经 capture 缩放后分辨率稳定）；
// 共享窗口被缩放导致分辨率变化时重新 configure（WebCodecs 不会自动适配尺寸变化），
// 重配会顺带产出关键帧，中途加入的观看者可据此起播。
//
// 码率可在运行中通过 setBitrate 动态调整：已 configure 时按当前尺寸即时 reconfigure，
// 已入队帧不受影响、后续帧按新码率编码。
//
// 输出严格 FIFO（与 AudioEncoder 一致），故 frameID 在 output 回调里顺序自增即可。

const H264_CODEC = 'avc1.640033' // H.264 High Profile Level 5.1：支持到 4K@24，覆盖 1080p/1440p/4K 全档（Level 3.1 仅支持到 720p，1080p 起 configure 会失败）
const H264_DEFAULT_BITRATE = 4_000_000 // 4 Mbps；1080p 屏幕投屏清晰度的默认折中，可在 UI 调高

export async function createEncoder(onEncoded, opts = {}) {
  if (typeof VideoEncoder === 'undefined') {
    console.warn('[screen] WebView2 不支持 WebCodecs VideoEncoder，投屏不可用。建议升级 WebView2 Runtime。')
    return null
  }

  let bitrate = opts.bitrate ?? H264_DEFAULT_BITRATE
  const fps = opts.fps ?? 24
  const keyframeInterval = opts.keyframeInterval ?? fps * 2 // ~2s 一个关键帧
  let frameIdx = 0
  let configured = false
  let curW = 0, curH = 0, curBitrate = 0

  const encoder = new VideoEncoder({
    output: (chunk) => {
      const data = new Uint8Array(chunk.byteLength)
      chunk.copyTo(data)
      onEncoded({
        keyframe: chunk.type === 'key',
        ts: chunk.timestamp, // 微秒，来自 VideoFrame.timestamp
        data,
      })
    },
    error: (e) => console.error('[screen] encoder error:', e),
  })

  function configure(w, h) {
    // avc.format='annexb'：SPS/PPS 内联进关键帧 NALU，接收端 configure({codec}) 即可起播，
    // 无需随帧附带 decoderConfig.description（自建传输最简）。
    try {
      encoder.configure({
        codec: H264_CODEC,
        bitrate,
        framerate: fps,
        width: w,
        height: h,
        avc: { format: 'annexb' },
      })
      configured = true
      curW = w
      curH = h
      curBitrate = bitrate
    } catch (e) {
      // level 不足/不支持当前分辨率时抛 NotSupportedError：打日志避免静默，configured 保持 false 跳过编码。
      console.error('[screen] encoder configure 失败:', e)
      configured = false
    }
  }

  function ensureConfig(w, h) {
    if (configured && w === curW && h === curH && curBitrate === bitrate) return
    configure(w, h)
  }

  return {
    encode(frame) {
      ensureConfig(frame.displayWidth, frame.displayHeight)
      if (!configured) return // 配置失败（如 level 不支持当前分辨率），跳过本帧
      const keyFrame = frameIdx % keyframeInterval === 0
      encoder.encode(frame, { keyFrame })
      frameIdx++
    },
    setBitrate(bps) {
      if (!bps || bps === bitrate) return
      bitrate = bps
      // 已 configure 则即时 reconfigure 生效；否则等首帧 ensureConfig 时按新 bitrate 配。
      if (configured) configure(curW, curH)
    },
    close() {
      try { encoder.close() } catch (e) {}
    },
  }
}
