// 投屏模块统一入口。与 voice/index.js 同构。
//
// 采集发送（startSharing/stopSharing）与接收播放（screen:data 监听）解耦：
// 进房间即启动 playback + 监听，未投屏也能看见别人；投屏由 startSharing 控制。
//
// 发送：capture -> encode -> envelope -> SendVideoToAll（Go 侧切分+P2P 闸门）。
// 接收：screen:data {srcVIP, data(base64), keyframe, ts} -> playback.handleVideo。

import { createCapture } from './capture.js'
import { createEncoder } from './encode.js'
import { createPlayback } from './playback.js'
import { encodeVideoEnvelope, VIDEO_CODEC_H264, VIDEO_FLAG_KEYFRAME } from './protocol.js'
import { SendVideoToAll } from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'

function base64ToBytes(b64) {
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes
}

// screenSupported：本平台能否投屏。发送侧要 getDisplayMedia 采集屏幕，接收侧要
// WebCodecs 的 VideoDecoder 解码，两者缺一整个通路都不成立：
//   - macOS 的 WKWebView 完全没有 getDisplayMedia（屏幕采集得另写原生链路，
//     见 macOS 移植计划），所以 Mac 端第一期不做投屏；
//   - 老版本 WebView2 缺 WebCodecs，同样落到这里——顺带修掉了"收到 screen:data
//     直接 new VideoDecoder 抛异常"的老问题。
export const screenSupported =
  typeof VideoDecoder !== 'undefined' &&
  typeof navigator !== 'undefined' &&
  !!navigator.mediaDevices &&
  typeof navigator.mediaDevices.getDisplayMedia === 'function'

// idleSession 返回一个"哑会话"：接口与真会话一致，但什么都不做。
// 调用方（进房自动启动、退房清理、成员离开清理）因此无需平台判断。
function idleSession() {
  const noop = () => {}
  return {
    startSharing: async () => false,
    stopSharing: noop,
    sharing: false,
    setBitrate: noop,
    setResolution: noop,
    removePeer: noop,
    stop: noop,
  }
}

// startScreen 创建投屏会话。canvas 用于渲染远端画面。
// onRemoteFrame(srcVIP) 在每收到一帧远端画面时回调（供 UI 显示观看面板）。
// onStopped() 在本端采集被系统"停止共享"条中断时回调。
// onIdle() 在远端画面消失（源离开或超时无帧）时回调（供 UI 隐藏画面区）。
//
// 平台不支持时返回哑会话且不注册 screen:data 监听——不支持的平台上收到帧也没法解。
export async function startScreen(canvas, opts = {}, onRemoteFrame, onStopped, onIdle) {
  if (!screenSupported) {
    console.warn('[screen] 当前平台不支持投屏（缺 getDisplayMedia 或 WebCodecs），投屏通路未启动')
    return idleSession()
  }

  const playback = await createPlayback(canvas, onIdle)

  const onVideo = (ev) => {
    if (!ev || ev.data == null) return
    const raw = base64ToBytes(ev.data)
    playback.handleVideo(ev.srcVIP, raw, ev.keyframe, ev.ts)
    if (onRemoteFrame) onRemoteFrame(ev.srcVIP)
  }
  EventsOn('screen:data', onVideo)

  let capture = null
  let encoder = null
  let frameID = 0

  // startSharing 开始投屏本端屏幕。返回 true 表示成功启动。
  async function startSharing() {
    if (capture) return true
    encoder = await createEncoder((frame) => {
      const env = encodeVideoEnvelope(
        VIDEO_CODEC_H264,
        frame.keyframe ? VIDEO_FLAG_KEYFRAME : 0,
        frameID,
        frame.ts,
        frame.data,
      )
      frameID = (frameID + 1) & 0xffff
      SendVideoToAll(Array.from(env)).catch((e) => console.warn('[screen] send err', e))
    }, opts)
    if (!encoder) return false // WebCodecs 不可用

    try {
      capture = await createCapture(
        (vf) => encoder.encode(vf),
        () => { stopSharing(); if (onStopped) onStopped() }, // 用户点了系统"停止共享"条 -> 同步 UI 回到未投屏态
        opts,
      )
    } catch (e) {
      console.warn('[screen] 采集失败:', e)
      encoder.close()
      encoder = null
      return false
    }
    return true
  }

  function stopSharing() {
    if (capture) { capture.close(); capture = null }
    if (encoder) { encoder.close(); encoder = null }
  }

  return {
    startSharing,
    stopSharing,
    get sharing() { return capture != null },
    setBitrate(bps) { if (encoder) encoder.setBitrate(bps) },
    setResolution(w) { if (capture) capture.setMaxWidth(w) },
    removePeer(srcVIP) { playback.removePeer(srcVIP) },
    stop() {
      EventsOff('screen:data')
      stopSharing()
      playback.close()
    },
  }
}
