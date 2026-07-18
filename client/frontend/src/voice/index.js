// 语音模块统一入口。
//
// capture（采集发送）与 playback（接收播放）解耦：进房间即启动 playback + 监听，
// 未开麦也能听见别人。capture 由 setMicOn 控制（闭麦 = 关 capture，无 muted 中间态）。
//
// "正在说话" 由 playback 解码后 onPeerLevel(level) 决定，只在 level>0（底噪已被
// 噪声门滤除）时触发，避免对方底噪导致脉动点闪烁。

import { createCapture } from './capture.js'
import { createEncoder } from './encode.js'
import { createPlayback } from './playback.js'
import { encodeVoicePayload } from './protocol.js'
import { SendVoiceToAll } from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'

function base64ToBytes(b64) {
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes
}

export async function startVoice(onMicLevel, onPeerLevel) {
  const playback = await createPlayback(onPeerLevel)
  const encoder = await createEncoder((frame) => {
    const payload = encodeVoicePayload(frame.codec, frame.seq, frame.ts, frame.data)
    SendVoiceToAll(Array.from(payload)).catch((e) => console.warn('[voice] send err', e))
  })

  const onVoice = (ev) => {
    if (!ev || ev.data == null) return
    const raw = base64ToBytes(ev.data)
    playback.handleVoice(ev.srcVIP, raw)
  }
  EventsOn('voice:data', onVoice)

  let capture = null
  let micGain = 1

  async function setMicOn(on) {
    if (on) {
      if (capture) return
      capture = await createCapture((pcmFloat32, level) => {
        if (onMicLevel) onMicLevel(level * micGain)
        if (micGain === 0) return // 0 增益视作静音，不发空帧（避免 50pps×N 浪费带宽）
        if (micGain !== 1) {
          for (let i = 0; i < pcmFloat32.length; i++) pcmFloat32[i] *= micGain
        }
        encoder.encode(pcmFloat32)
      })
    } else {
      if (capture) { capture.close(); capture = null }
      if (onMicLevel) onMicLevel(0)
    }
  }

  return {
    setMicGain(g) { micGain = g },
    setMicOn,
    setPeerVolume(srcVIP, vol) { playback.setPeerVolume(srcVIP, vol) },
    removePeer(srcVIP) { playback.removePeer(srcVIP) },
    resume() { return playback.resume() },
    stop() {
      EventsOff('voice:data')
      if (capture) capture.close()
      encoder.close()
      playback.close()
    },
  }
}
