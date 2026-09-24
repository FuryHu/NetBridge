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

// startScreen 创建投屏会话。canvas 用于渲染远端画面。
// onRemoteFrame(srcVIP) 在每收到一帧远端画面时回调（供 UI 显示观看面板）。
// onStopped() 在本端采集被系统"停止共享"条中断时回调。
// onIdle() 在远端画面消失（源离开或超时无帧）时回调（供 UI 隐藏画面区）。
export async function startScreen(canvas, opts = {}, onRemoteFrame, onStopped, onIdle) {
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
