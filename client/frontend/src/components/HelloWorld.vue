<script setup>
import {reactive, onMounted, onUnmounted, nextTick, ref, computed, watch} from 'vue'
import {Connect, Disconnect, JoinRoom, LeaveRoom, GetPeers, GetSelf, GetStatus, SendChat, OpenURL} from '../../wailsjs/go/main/App'
import {EventsOn, EventsOff} from '../../wailsjs/runtime/runtime'
import {startVoice} from '../voice'
import {locale, t, setLocale, LANGS} from '../i18n'
import MicIcon from './MicIcon.vue'

// GitHub 仓库链接（顶栏图标按钮打开）。换成自己的仓库地址即可。
const GITHUB_URL = 'https://github.com/FuryHu/netbridge'

const STORAGE_KEY = 'netbridge_history'
function loadHistory() {
  try { return JSON.parse(localStorage.getItem(STORAGE_KEY)) || {} } catch { return {} }
}
function saveHistory(h) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(h)) } catch {}
}
const hist = loadHistory()

const data = reactive({
  serverAddr: hist.serverAddr || '',
  room: hist.room || '',
  nickName: hist.nickName || '',
  status: '未连接',
  self: {id: '', nickName: '', vip: '', publicAddr: '', v4: '', v6: '', isIPv6: false},
  allPeers: [],
  connected: false,
  connecting: false,
  connectError: '',
  joined: false,
  tunActive: false,
  chat: [],
  log: [],
  micOn: false,
  voice: null,
  activePeerRect: null,
  voiceEnabled: (typeof localStorage !== 'undefined' && localStorage.getItem('netbridge_voice') === 'on'),
  speaking: {},
  micLevel: 0,
  micGainPct: 100,
  showMicPopover: false,
  peerVols: {},
  peerMutes: {},
  activePeerVIP: null,
})
const showLog = ref(false)
const chatMsg = ref('')
const chatEl = ref(null)
const logEl = ref(null)
let autoTried = false
let refreshTimer = null
let peerLevelTimers = {}

// 自定义确认弹窗（传 i18n key，模板里再 t() 取文案，随语言切换实时更新）
const confirmKey = ref('')
const confirmAction = ref(null)
function showConfirm(key, action) {
  confirmKey.value = key
  confirmAction.value = action
}
function doConfirm() {
  if (confirmAction.value) confirmAction.value()
  confirmKey.value = ''
  confirmAction.value = null
}
function cancelConfirm() {
  confirmKey.value = ''
  confirmAction.value = null
}

// 语言切换浮窗
const showLang = ref(false)
function pickLocale(code) {
  setLocale(code)
  showLang.value = false
}
// GitHub 图标：走后端 BrowserOpenURL 在系统浏览器打开（webview 内 <a href> 不可靠）
function openGitHub() {
  OpenURL(GITHUB_URL)
}
// 进/出房间时收起语言浮窗，避免离开房间后凭残留 showLang 重新弹出
watch(() => data.joined, () => { showLang.value = false })

const others = computed(() => data.allPeers)

// 兼容服务端返回的两种 IPv6 标识：通道是否走 v6 / peer 是否暴露 v6 端点。
const isPeerV6 = (p) => !!(p.isIPv6 || p.IsIPv6)
const peerHasV6 = (p) => !!(p.v6 || p.V6)

// 后端 State.String() 返回中文状态串；前端先映射成稳定 key，再做 i18n 与配色。
const STATUS_KEY = {
  '未连接': 'disconnected',
  '连接中': 'connecting',
  '已连接': 'connected',
  '打洞中': 'punching',
  'P2P直连': 'p2p',
  '服务器中转': 'relay',
}
function statusKey(s) { return STATUS_KEY[s] || 'disconnected' }

// channel 文案（hover title 用）；不再依赖 emoji 在主视图里表达。
function peerTitle(p) {
  const ch = p.channel === 'p2p' ? t('peer.channelP2P') : p.channel === 'relay' ? t('peer.channelRelay') : t('peer.channelPending')
  const proto = isPeerV6(p) ? ' · IPv6' : (p.channel === 'p2p' ? ' · IPv4' : '')
  const v6 = peerHasV6(p) && !isPeerV6(p) ? '\n' + t('peer.candidateV6') + (p.v6 || p.V6) : ''
  return `${p.nickName} · VIP: ${p.vip} · ${ch}${proto}${v6}`
}
// 把后端的 status 字符串映射成视觉颜色 token——使用规范里的 4 个状态色之一。
function statusColor(s) {
  const k = statusKey(s)
  if (k === 'connected' || k === 'p2p') return 'success'
  if (k === 'connecting') return 'info'
  if (k === 'punching' || k === 'relay') return 'warning'
  return 'muted'
}

function addLog(msg) {
  data.log.push(`[${new Date().toLocaleTimeString()}] ${msg}`)
  if (data.log.length > 200) data.log.shift()
}
function addChat(nick, msg, ts) {
  data.chat.push({nick, msg, ts})
  if (data.chat.length > 200) data.chat.shift()
  nextTick(() => scrollBottom(chatEl))
}
function scrollBottom(el) { if (el?.value) { el.value.scrollTop = el.value.scrollHeight } }
function isMine(nick) { return nick === data.self.nickName || nick === data.nickName }

// ---- 操作 ----

async function doConnect() {
  const addr = data.serverAddr.trim()
  if (!addr || data.connecting) return
  data.connecting = true
  data.connectError = ''
  addLog(`连接 ${addr} ...`)
  try {
    await Connect(addr)
    data.connected = true
    saveHistory({ serverAddr: addr, room: data.room, nickName: data.nickName })
    addLog('✓ 已连接')
    refreshStatus()
  } catch (e) {
    data.connectError = String(e)
    addLog('✗ 连接失败: ' + e)
  } finally {
    data.connecting = false
  }
}

async function doDisconnect() {
  showConfirm('modal.confirmDisconnect', async () => {
    await Disconnect()
    if (data.voice) { data.voice.stop(); data.voice = null }
    data.micOn = false
    data.micLevel = 0
    data.speaking = {}
    data.peerVols = {}
    data.peerMutes = {}
    data.activePeerVIP = null
    for (const k in peerLevelTimers) clearTimeout(peerLevelTimers[k])
    peerLevelTimers = {}
    data.connected = false
    data.joined = false
    data.allPeers = []
    data.chat = []
    data.self = {id: '', nickName: '', vip: '', publicAddr: '', v4: '', v6: '', isIPv6: false}
    addLog('已断开连接')
  })
}

async function doJoinRoom() {
  const room = data.room.trim() || 'default'
  const name = data.nickName.trim() || 'Player' + Math.floor(Math.random() * 1000)
  data.room = room
  data.nickName = name
  addLog(`加入 ${room} (${name}) ...`)
  try {
    await JoinRoom(room, name)
    data.joined = true
    // 立即占位显示，避免"自己名字过一会儿才出现"——
    // RoomStatus 到达后 self:update 事件会把这块替换为带 VIP/公网端点的完整数据。
    data.self = {id: '', nickName: name, vip: '', publicAddr: '', v4: '', v6: '', isIPv6: false}
    saveHistory({ serverAddr: data.serverAddr, room, nickName: name })
    addLog('✓ 已加入房间')
  } catch (e) {
    addLog('✗ 加入失败: ' + e)
  }
}

function doLeaveRoom() {
  showConfirm('modal.confirmLeaveRoom', () => {
    LeaveRoom()
    if (data.voice) { data.voice.stop(); data.voice = null }
    data.micOn = false
    data.micLevel = 0
    data.speaking = {}
    data.peerVols = {}
    data.peerMutes = {}
    data.activePeerVIP = null
    for (const k in peerLevelTimers) clearTimeout(peerLevelTimers[k])
    peerLevelTimers = {}
    data.joined = false
    data.allPeers = []
    data.chat = []
    data.self = {id: '', nickName: '', vip: '', publicAddr: '', v4: '', v6: '', isIPv6: false}
    addLog('已退出房间')
  })
}

// 网卡的开启/关闭已由后端在 onSelfUpdate / LeaveRoom 时自动管理——
// 前端只需订阅 tun:active 事件维护顶栏 TUN 徽标的显示。

function doSendChat() {
  const m = chatMsg.value.trim()
  if (!m) return
  SendChat(m).catch(e => addLog('✗ 发送失败: ' + e))
  chatMsg.value = ''
}

// ---- 语音 ----

// peerVIPNum 从展示用的 "10.66.0.3" 取出主机号 3，用于匹配语音帧的 srcVIP（uint32）。
function peerVIPNum(p) {
  if (!p || !p.vip) return -1
  const parts = String(p.vip).split('.')
  return parts.length ? parseInt(parts[parts.length - 1], 10) : -1
}
function isSpeaking(p) {
  return !!data.speaking[String(peerVIPNum(p))]
}
// 本地麦克风发送增益：滑块 / 滚轮调节，0~100% -> setMicGain(0~1)
function onMicGainInput() {
  if (data.voice) data.voice.setMicGain(data.micGainPct / 100)
}
function onMicWheel(e) {
  e.preventDefault()
  const step = e.deltaY < 0 ? 5 : -5
  data.micGainPct = Math.max(0, Math.min(100, data.micGainPct + step))
  if (data.voice) data.voice.setMicGain(data.micGainPct / 100)
}

// 语音回调：对方音量用 peak hold + 1.5s 归零，避免随底噪闪烁、停说话后平滑消失
function voiceCallbacks() {
  return {
    onMicLevel: (level) => { data.micLevel = Math.max(level, data.micLevel * 0.97) }, // peak hold + 慢衰减，避免抖动
    onPeerLevel: (srcVIP, level) => {
      // 只在实质声音触发"正在说话"（level>0，底噪已被噪声门滤除），避免闪烁
      if (level > 0) {
        const key = String(srcVIP)
        data.speaking[key] = true
        if (peerLevelTimers[key]) clearTimeout(peerLevelTimers[key])
        peerLevelTimers[key] = setTimeout(() => { data.speaking[key] = false }, 600)
      }
    },
  }
}

// autoStartVoice 进房间自动启动语音通路。playback + 监听常驻 = 未开麦也能听见别人。
// micOn 默认 true，自动尝试开麦（getUserMedia 需用户手势，失败则提示手动点麦克风）。
async function autoStartVoice() {
  if (data.voice || !data.joined || !data.voiceEnabled) return
  try {
    const cb = voiceCallbacks()
    data.voice = await startVoice(cb.onMicLevel, cb.onPeerLevel)
    data.voice.setMicGain(data.micGainPct / 100)
    // 默认闭麦：不自动开麦（getUserMedia 需用户手势），用户手动点麦克风
  } catch (e) {
    addLog('✗ 语音启动失败: ' + e)
  }
}

// toggleVoiceEnabled 全局语音开关（缓存）：关 = 不参与语音（不听不发），开 = 进房自动听
function toggleVoiceEnabled() {
  data.voiceEnabled = !data.voiceEnabled
  try { localStorage.setItem('netbridge_voice', data.voiceEnabled ? 'on' : 'off') } catch (e) {}
  if (!data.voiceEnabled && data.voice) {
    data.voice.stop(); data.voice = null
    data.micOn = false; data.micLevel = 0
    data.speaking = {}; data.peerVols = {}; data.peerMutes = {}; data.activePeerVIP = null
  } else if (data.voiceEnabled && data.joined && !data.voice) {
    autoStartVoice()
  }
  addLog(data.voiceEnabled ? '语音已启用' : '语音已关闭')
}

// 对方音量：每个 peer 独立的本地播放增益（0~1，默认 1）+ 静音开关（记原值）
function peerKey(p) { return String(peerVIPNum(p)) }
function peerVolPct(p) { return Math.round((data.peerVols[peerKey(p)] ?? 1) * 100) }
function isPeerMuted(p) { return !!data.peerMutes[peerKey(p)] }
function isPeerPopover(p) { return data.activePeerVIP === peerKey(p) }
function effectivePeerVol(p) { return isPeerMuted(p) ? 0 : (data.peerVols[peerKey(p)] ?? 1) }
function applyPeerVol(p) {
  if (data.voice) data.voice.setPeerVolume(peerVIPNum(p), effectivePeerVol(p))
}
function onPeerVolInput(p, e) {
  data.peerVols[peerKey(p)] = Number(e.target.value) / 100
  applyPeerVol(p)
}
function togglePeerMute(p) {
  data.peerMutes[peerKey(p)] = !data.peerMutes[peerKey(p)]
  applyPeerVol(p)
}
function togglePeerPopover(p, e) {
  const k = peerKey(p)
  if (data.activePeerVIP === k) { data.activePeerVIP = null; return }
  data.activePeerVIP = k
  // fixed 定位：用点击的 li 坐标算位置，不受父容器 overflow 裁剪
  const r = e.currentTarget.getBoundingClientRect()
  const above = r.top > 60
  data.activePeerRect = { left: r.left, width: r.width, top: above ? r.top - 6 : r.bottom + 6, above }
}
function peerPopoverStyle(p) {
  if (!isPeerPopover(p) || !data.activePeerRect) return { display: 'none' }
  const r = data.activePeerRect
  return {
    position: 'fixed',
    left: r.left + 'px',
    top: r.top + 'px',
    width: r.width + 'px',
    transform: r.above ? 'translateY(-100%)' : 'none',
  }
}

// toggleMic 切换麦克风：首次点击启动语音通路（必须在用户手势内调 getUserMedia），
// 之后点击切换静音。离开房间 / 断开时由对应逻辑 stop。
async function toggleMic() {
  if (!data.joined) {
    addLog('请先加入房间')
    return
  }
  if (!data.voiceEnabled) {
    addLog('语音已关闭，请在加入房间页开启语音')
    return
  }
  if (!data.voice) {
    try {
      const cb = voiceCallbacks()
      data.voice = await startVoice(cb.onMicLevel, cb.onPeerLevel)
      data.voice.setMicGain(data.micGainPct / 100)
    } catch (e) {
      addLog('✗ 语音启动失败: ' + e)
      return
    }
  }
  // 用户手势兜底：autoConnect 无手势进房时 playback ctx 可能仍挂起，借这次点击唤醒。
  if (data.voice) data.voice.resume()
  data.micOn = !data.micOn
  try {
    await data.voice.setMicOn(data.micOn)
    addLog(data.micOn ? '麦克风已开' : '麦克风已静音')
  } catch (e) {
    data.micOn = !data.micOn
    addLog('✗ 麦克风切换失败: ' + e)
  }
}

// onKeydown F2 快捷开关麦克风。键盘事件属用户手势，getUserMedia 允许。
function onKeydown(e) {
  if (e.key === 'F2') {
    e.preventDefault()
    toggleMic()
  }
  // Ctrl+Shift+L：切换日志面板（顶栏日志按钮已隐藏，改用快捷键触发）
  if (e.ctrlKey && e.shiftKey && (e.key === 'l' || e.key === 'L')) {
    e.preventDefault()
    showLog.value = !showLog.value
  }
}

async function refreshStatus() {
  try {
    data.status = await GetStatus()
    const self = await GetSelf()
    // 后端没有 self 信息时返回空 PeerView——不要用它覆盖我们已经占位的 nickName。
    if (self && (self.id || self.vip || self.publicAddr)) {
      data.self = { ...data.self, ...self }
    }
    data.allPeers = await GetPeers()
  } catch (e) {}
}

// ---- 自动连接 ----

async function autoConnect() {
  if (autoTried || !hist.serverAddr) return
  autoTried = true
  data.connecting = true
  addLog('自动连接 ' + hist.serverAddr)
  try {
    await Connect(hist.serverAddr)
    data.connected = true
    addLog('✓ 已连接')
    await refreshStatus()
    // 如果上次有房间缓存，自动加入
    if (hist.room) {
      data.room = hist.room
      data.nickName = hist.nickName || ''
      addLog('自动加入 ' + hist.room)
      try {
        await JoinRoom(hist.room, hist.nickName || 'Player')
        data.joined = true
        addLog('✓ 已加入房间')
        await refreshStatus()
      } catch (e) { addLog('自动加入失败: ' + e) }
    }
  } catch (e) {
    data.connected = false
    data.connectError = String(e)
    addLog('✗ 自动连接失败: ' + e)
  } finally {
    data.connecting = false
  }
}

// ---- 事件 ----

onMounted(() => {
  EventsOn('status:change', (s) => {
    data.status = s
    if (statusKey(s) === 'connected') {
      data.joined = true; refreshStatus()
      if (!refreshTimer) refreshTimer = setInterval(refreshStatus, 2000)
    }
    // 注意：不在「连接中」时置 connected=true——握手期间后端状态先到「连接中」，
    // 若此时翻页会过早进入房间页。connected 由 doConnect/autoConnect 握手成功后显式置位。
    if (statusKey(s) === 'disconnected') {
      data.connected = false; data.joined = false
      if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null }
    }
  })
  EventsOn('peer:update', (peers) => {
    const list = peers || []
    // 清理已离开 peer 的播放解码器，避免 players Map 累积泄漏 AudioDecoder（直到退房才整体 close）。
    if (data.voice) {
      const stay = new Set(list.map(peerVIPNum))
      for (const p of data.allPeers) {
        const v = peerVIPNum(p)
        if (!stay.has(v)) data.voice.removePeer(v)
      }
    }
    data.allPeers = list
  })
  EventsOn('self:update', (self) => {
    if (!self) {
      // 退房 / 断开时后端推空 self——清空展示。
      data.self = {id: '', nickName: '', vip: '', publicAddr: '', v4: '', v6: '', isIPv6: false}
      return
    }
    // 服务端把 VIP / 公网端点带回来后，立即合并到本地占位昵称之上。
    data.self = {
      id: self.id || data.self.id || '',
      nickName: self.nickName || data.self.nickName || '',
      vip: self.vip || '',
      publicAddr: self.publicAddr || '',
      v4: self.v4 || '',
      v6: self.v6 || '',
      isIPv6: !!self.isIPv6,
    }
    if (self.vip && data.joined && !data.voice) autoStartVoice()
  })
  EventsOn('chat:message', (c) => { addChat(c.nickName, c.message, c.timestamp) })
  EventsOn('log:message', (msg) => { addLog(msg) })
  EventsOn('tun:active', (active) => { data.tunActive = !!active })
  // F2 快捷开关麦克风
  window.addEventListener('keydown', onKeydown)
  // 尝试自动连接
  setTimeout(autoConnect, 500)
})
onUnmounted(() => {
  if (data.voice) { data.voice.stop(); data.voice = null }
  window.removeEventListener('keydown', onKeydown)
  EventsOff('status:change')
  EventsOff('peer:update')
  EventsOff('self:update')
  EventsOff('chat:message')
  EventsOff('log:message')
  EventsOff('tun:active')
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<template>
  <main class="app">
    <!-- 顶栏：左侧产品名+状态信息；右侧动作按钮 -->
    <header class="topbar">
      <div class="topbar-left">
        <!-- 房间内显示房间号（更有上下文价值），其他场景显示产品名 -->
        <span class="brand">{{ data.joined ? data.room : 'NetBridge' }}</span>
        <span class="status" :class="'status-' + statusColor(data.status)">
          <span class="status-dot"></span>
          <span class="status-text">{{ t('status.' + statusKey(data.status)) }}</span>
        </span>
        <span v-if="data.self.vip" class="badge badge-mono" :title="t('topbar.vipTip')">{{ data.self.vip }}</span>
        <span v-if="data.tunActive" class="badge badge-info" :title="t('topbar.tunTip')">TUN</span>
        <span v-if="data.self.isIPv6" class="badge badge-primary" :title="t('topbar.ipv6Tip')">IPv6</span>
        <span v-else-if="data.self.v4" class="badge badge-ghost" :title="t('topbar.ipv4Tip')">IPv4</span>
      </div>
      <div class="topbar-right">
        <!-- 语言切换：文/A 翻译图标 + 下拉（仅连接前 / 进房前显示；语言已缓存，进房后无需再切） -->
        <div v-if="!data.joined" class="lang-switch">
          <button @click="showLang = !showLang" class="btn btn-ghost btn-sm icon-btn"
                  :class="{ 'is-active': showLang }"
                  :title="t('lang.label')" :aria-label="t('lang.label')">
            <svg class="ico" viewBox="0 0 24 24" fill="currentColor">
              <path d="M12.87 15.07l-2.54-2.51.03-.03c1.74-1.94 2.98-4.17 3.71-6.53H17V4h-7V2H8v2H1v1.99h11.17C11.5 7.92 10.44 9.75 9 11.35 8.07 10.32 7.3 9.19 6.69 8h-2c.73 1.63 1.73 3.17 2.98 4.56l-5.09 5.02L4 19l5-5 3.11 3.11.76-2.04zM18.5 10h-2L12 22h2l1.12-3h4.75L21 22h2l-4.5-12zm-2.62 7l1.62-4.33L19.12 17h-3.24z"/>
            </svg>
          </button>
          <div v-if="showLang" class="lang-backdrop" @click="showLang = false"></div>
          <div class="lang-popover" v-show="showLang" @click.stop>
            <button v-for="l in LANGS" :key="l.code" @click="pickLocale(l.code)"
                    class="lang-option" :class="{ 'is-active': locale === l.code }">
              <span class="lang-check">{{ locale === l.code ? '✓' : '' }}</span>
              <span class="lang-name">{{ l.label }}</span>
            </button>
          </div>
        </div>
        <!-- GitHub 仓库链接（仅连接前 / 进房前显示；房间内隐藏，保持工作区清爽） -->
        <button v-if="!data.joined" @click="openGitHub" class="btn btn-ghost btn-sm icon-btn github-btn" title="GitHub" aria-label="GitHub">
          <svg class="ico" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 .5C5.37.5 0 5.78 0 12.29c0 5.2 3.44 9.61 8.21 11.16.6.11.82-.25.82-.56 0-.28-.01-1.02-.02-2-3.34.71-4.04-1.58-4.04-1.58-.55-1.36-1.34-1.72-1.34-1.72-1.09-.73.08-.72.08-.72 1.21.08 1.84 1.22 1.84 1.22 1.07 1.8 2.81 1.28 3.5.98.11-.76.42-1.28.76-1.57-2.67-.3-5.47-1.3-5.47-5.78 0-1.28.47-2.32 1.23-3.14-.12-.3-.53-1.5.12-3.13 0 0 1-.32 3.3 1.2a11.6 11.6 0 0 1 6 0c2.3-1.52 3.3-1.2 3.3-1.2.65 1.63.24 2.83.12 3.13.77.82 1.23 1.86 1.23 3.14 0 4.49-2.81 5.48-5.49 5.77.43.36.81 1.08.81 2.18 0 1.57-.01 2.84-.01 3.23 0 .31.21.68.83.56A12.04 12.04 0 0 0 24 12.29C24 5.78 18.63.5 12 .5z"/>
          </svg>
        </button>
        <button v-if="data.joined" @click="doLeaveRoom" class="btn btn-ghost btn-sm">{{ t('topbar.leaveRoom') }}</button>
        <button v-if="data.connected" @click="doDisconnect" class="btn btn-ghost btn-sm btn-danger-ghost">{{ t('topbar.disconnect') }}</button>
      </div>
    </header>

    <!-- 日志面板：可折叠的辅助信息区（顶栏日志按钮已隐藏，Ctrl+Shift+L 切换） -->
    <div v-if="showLog" class="log-panel">
      <button class="log-close" @click="showLog = false" :title="t('log.close')" aria-label="×">×</button>
      <div class="log-panel-body" ref="logEl">
        <div v-for="(m, i) in data.log" :key="i" class="log-line">{{ m }}</div>
        <div v-if="data.log.length === 0" class="log-empty">{{ t('log.empty') }}</div>
      </div>
    </div>

    <!-- 连接服务器页 -->
    <div v-if="!data.connected" class="centered">
      <section class="card auth-card">
        <h2 class="card-title">{{ t('connect.title') }}</h2>
        <input v-model="data.serverAddr"
               @keyup.enter="doConnect"
               :disabled="data.connecting"
               :placeholder="t('connect.placeholder')"
               class="input"/>
        <button @click="doConnect"
                :disabled="data.connecting"
                class="btn btn-primary btn-block">
          {{ data.connecting ? t('connect.connecting') : t('connect.button') }}
        </button>
        <p v-if="data.connectError" class="auth-error">{{ t('connect.failed') }}{{ data.connectError }}</p>
      </section>
    </div>

    <!-- 加入房间页 -->
    <div v-else-if="!data.joined" class="centered">
      <section class="card auth-card">
        <h2 class="card-title">{{ t('join.title') }}</h2>
        <input v-model="data.room" @keyup.enter="doJoinRoom" :placeholder="t('join.roomPlaceholder')" class="input"/>
        <input v-model="data.nickName" @keyup.enter="doJoinRoom" :placeholder="t('join.namePlaceholder')" class="input"/>
        <button @click="toggleVoiceEnabled" class="voice-toggle" :class="{ 'is-off': !data.voiceEnabled }" :title="data.voiceEnabled ? t('voice.enabledTip') : t('voice.disabledTip')">
          <svg class="vt-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M11 5 L6 9 H3 V15 H6 L11 19 Z"/>
            <path d="M15.5 8.5 a5 5 0 0 1 0 7"/>
            <path d="M18 6 a9 9 0 0 1 0 12"/>
            <line v-if="!data.voiceEnabled" x1="4" y1="4" x2="20" y2="20"/>
          </svg>
          <span class="vt-txt">{{ data.voiceEnabled ? t('voice.on') : t('voice.off') }}</span>
        </button>
        <button @click="doJoinRoom" class="btn btn-primary btn-block">{{ t('join.button') }}</button>
      </section>
    </div>

    <!-- 主聊天页：左成员 + 右聊天 -->
    <section v-else class="room">
      <!-- 左侧成员列表 -->
      <aside class="members">
        <div class="members-head">
          <span class="members-title">{{ t('members.title') }}</span>
          <span class="members-count">{{ others.length + 1 }}</span>
        </div>
        <ul class="member-list">
          <!-- 自己 -->
          <li class="member member-self">
            <span class="member-dot dot-self"></span>
            <span class="member-name">{{ data.self.nickName || '...' }}</span>
            <span class="member-tag">{{ t('members.self') }}</span>
          </li>
          <!-- 其他人 -->
          <li v-for="p in others"
              :key="p.id"
              class="member"
              :class="{ 'member-speaking': isSpeaking(p), 'member-active': isPeerPopover(p) }"
              :title="peerTitle(p)"
              @click="togglePeerPopover(p, $event)">
            <span class="member-dot"
                  :class="p.channel === 'p2p' ? 'dot-p2p' : p.channel === 'relay' ? 'dot-relay' : 'dot-pending'"></span>
            <span class="member-name">{{ p.nickName }}</span>
            <span v-if="isSpeaking(p)" class="member-speaking-dot" :title="t('voice.speaking')"></span>
            <MicIcon v-if="isPeerMuted(p)" muted class="member-mute-ico" :title="t('voice.mutedForYou')"/>
            <span v-if="p.channel === 'p2p'" class="member-channel ch-p2p">P2P</span>
            <span v-else-if="p.channel === 'relay'" class="member-channel ch-relay">{{ t('peer.badgeRelay') }}</span>
            <span v-else class="member-channel ch-pending">…</span>
            <div class="peer-popover" v-show="isPeerPopover(p)" :style="peerPopoverStyle(p)" @click.stop>
              <input type="range" min="0" max="100" :value="peerVolPct(p)" @input="onPeerVolInput(p, $event)" class="peer-slider"/>
              <button @click="togglePeerMute(p)" class="peer-mute-btn" :class="{ 'is-on': isPeerMuted(p) }" :title="t('voice.muteToggleTip')">
                <MicIcon :muted="isPeerMuted(p)" class="peer-mute-ico"/>
              </button>
            </div>
          </li>
        </ul>
        <div class="voice-bar" v-if="data.joined"
             @mouseenter="data.showMicPopover = data.voiceEnabled"
             @mouseleave="data.showMicPopover = false"
             @wheel="onMicWheel">
          <button @click="toggleVoiceEnabled" class="voice-global" :class="{ 'is-off': !data.voiceEnabled }" :title="data.voiceEnabled ? t('voice.enabledTip') : t('voice.disabledTip')">
            <svg class="vg-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M11 5 L6 9 H3 V15 H6 L11 19 Z"/>
              <path d="M15.5 8.5 a5 5 0 0 1 0 7"/>
              <path d="M18 6 a9 9 0 0 1 0 12"/>
              <line v-if="!data.voiceEnabled" x1="4" y1="4" x2="20" y2="20"/>
            </svg>
          </button>
          <div class="mic-popover" v-show="data.showMicPopover" @click.stop>
            <input type="range" min="0" max="100" v-model.number="data.micGainPct"
                   @input="onMicGainInput" @wheel.stop="onMicWheel" class="mic-slider"/>
          </div>
          <button @click="toggleMic" class="voice-btn" :class="{ 'is-on': data.micOn, 'is-disabled': !data.voiceEnabled }" :disabled="!data.voiceEnabled" :title="data.micOn ? t('voice.micOnTip') : t('voice.micOffTip')">
            <span class="voice-fill" :style="{ transform: 'scaleX(' + data.micLevel + ')' }"></span>
            <MicIcon :muted="!data.micOn" class="voice-ico"/>
            <span class="voice-txt">{{ data.micOn ? t('voice.micOn') : t('voice.micOff') }}</span>
            <span class="voice-f2">F2</span>
          </button>
        </div>
      </aside>

      <!-- 中央聊天区 -->
      <section class="chat">
        <div class="chat-msgs" ref="chatEl">
          <div v-for="(c, i) in data.chat"
               :key="i"
               class="chat-row"
               :class="{ mine: isMine(c.nick) }">
            <div class="chat-meta">
              <span class="chat-nick">{{ c.nick }}</span>
              <span class="chat-time">{{ new Date(c.ts).toLocaleTimeString() }}</span>
            </div>
            <div class="chat-bubble">{{ c.msg }}</div>
          </div>
          <div v-if="data.chat.length === 0" class="chat-empty">{{ t('chat.empty') }}</div>
        </div>
        <div class="chat-input-row">
          <input v-model="chatMsg" @keyup.enter="doSendChat" :placeholder="t('chat.placeholder')" class="input"/>
          <button @click="doSendChat" class="btn btn-primary">{{ t('chat.send') }}</button>
        </div>
      </section>
    </section>

    <!-- 确认弹窗 -->
    <div v-if="confirmKey" class="modal-overlay" @click.self="cancelConfirm">
      <div class="modal">
        <p class="modal-text">{{ t(confirmKey) }}</p>
        <div class="modal-actions">
          <button @click="cancelConfirm" class="btn btn-ghost">{{ t('modal.cancel') }}</button>
          <button @click="doConfirm" class="btn btn-primary">{{ t('modal.confirm') }}</button>
        </div>
      </div>
    </div>
  </main>
</template>

<style scoped>
/* ===== 容器与基础 ===== */
.app {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--color-bg);
  color: var(--color-text);
}

/* ===== 顶栏 ===== */
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 44px;
  padding: 0 16px;
  background: var(--color-bg-secondary);
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}
.topbar-left,
.topbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.brand {
  font-weight: 600;
  font-size: 14px;
  letter-spacing: 0.01em;
  color: var(--color-text);
  margin-right: 4px;
}

/* ===== 状态指示 ===== */
.status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--color-text-muted);
}
.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-text-disabled);
  flex-shrink: 0;
}
.status-success .status-dot { background: var(--color-success); }
.status-warning .status-dot { background: var(--color-warning); }
.status-info    .status-dot { background: var(--color-accent); }
.status-muted   .status-dot { background: var(--color-text-disabled); }
.status-text { color: var(--color-text-muted); }

/* ===== 徽标（胶囊） ===== */
.badge {
  display: inline-flex;
  align-items: center;
  height: 18px;
  padding: 0 8px;
  font-size: 11px;
  font-weight: 500;
  border-radius: var(--radius-pill);
  letter-spacing: 0.02em;
  line-height: 1;
}
.badge-mono {
  font-family: 'JetBrains Mono', Consolas, 'Courier New', monospace;
  background: transparent;
  color: var(--color-success);
  border: 1px solid var(--color-border-strong);
}
.badge-info {
  background: var(--color-accent-soft);
  color: var(--color-accent);
  border: 1px solid transparent;
}
.badge-primary {
  background: var(--color-accent);
  color: #fff;
}
.badge-ghost {
  background: transparent;
  color: var(--color-text-muted);
  border: 1px solid var(--color-border-strong);
}

/* ===== 按钮 ===== */
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 32px;
  padding: 0 14px;
  font-size: 13px;
  font-weight: 500;
  /* 关键：固定 line-height，避免全局 body line-height:1.5 把固定高度按钮里的文字撑偏 */
  line-height: 1;
  border-radius: var(--radius-sm);
  border: 1px solid transparent;
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
  transition: background-color 120ms, border-color 120ms, color 120ms;
  white-space: nowrap;
  /* 提供给某些字体在 Windows 上的字形微调，让水平视觉中心更稳 */
  font-feature-settings: 'tnum' 1;
}
.btn:focus { outline: none; }
.btn:focus-visible {
  /* 键盘焦点可见性，鼠标点击不显示 */
  box-shadow: 0 0 0 2px var(--color-accent);
}
.btn-sm {
  height: 26px;
  padding: 0 10px;
  font-size: 12px;
}
.btn-block {
  width: 100%;
  height: 36px;
}

/* Primary：纯色紫蓝 */
.btn-primary {
  background: var(--color-accent);
  color: #fff;
}
.btn-primary:hover {
  background: var(--color-accent-hover);
}

/* Ghost：透明 + 边框，工具栏次要操作 */
.btn-ghost {
  background: transparent;
  border-color: var(--color-border-strong);
  color: var(--color-text-muted);
}
.btn-ghost:hover {
  background: var(--color-hover-overlay);
  color: var(--color-text);
  border-color: var(--color-border-strong);
}
.btn-ghost.is-active {
  background: var(--color-hover-overlay);
  color: var(--color-text);
}

/* Danger ghost：透明红 */
.btn-danger-ghost {
  color: var(--color-danger);
  border-color: var(--color-border-strong);
}
.btn-danger-ghost:hover {
  background: var(--color-danger);
  color: #fff;
  border-color: var(--color-danger);
}

/* ===== 卡片 ===== */
.card {
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 20px;
}
.card-title {
  margin: 0 0 16px;
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text);
  letter-spacing: 0.01em;
}

/* ===== 输入框 ===== */
.input {
  display: block;
  width: 100%;
  height: 36px;
  padding: 0 12px;
  font-size: 13px;
  color: var(--color-text);
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  box-sizing: border-box;
  transition: border-color 120ms;
}
.input:focus {
  outline: none;
  border-color: var(--color-accent);
}
.input::placeholder {
  color: var(--color-text-disabled);
}

/* ===== 居中容器 ===== */
.centered {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}
.auth-card {
  width: 100%;
  max-width: 360px;
}
.auth-card .input + .input,
.auth-card .input + .btn,
.auth-card .btn + .input {
  margin-top: 10px;
}
.auth-error {
  margin: 12px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--color-danger);
  word-break: break-word;
}

/* ===== 房间布局 ===== */
.room {
  flex: 1;
  display: flex;
  overflow: hidden;
  min-height: 0;
}

/* 左侧成员栏 */
.members {
  width: 200px;
  background: var(--color-bg-secondary);
  border-right: 1px solid var(--color-border);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}
.members-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 36px;
  padding: 0 14px;
  font-size: 12px;
  color: var(--color-text-muted);
  border-bottom: 1px solid var(--color-border);
}
.members-title {
  font-weight: 500;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.members-count {
  color: var(--color-text-muted);
}
.member-list {
  list-style: none;
  margin: 0;
  padding: 6px 0;
  overflow-y: auto;
  flex: 1;
  min-height: 0;
}
.member {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 28px;
  padding: 0 14px;
  font-size: 13px;
  color: var(--color-text);
  cursor: default;
}
.member:hover {
  background: var(--color-hover-overlay);
}
.member-self {
  /* 自己始终在最上，无 hover 加重 */
}
.member-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--color-text-disabled);
}
.dot-self    { background: var(--color-success); }
.dot-p2p     { background: var(--color-success); }
.dot-relay   { background: var(--color-warning); }
.dot-pending { background: var(--color-text-disabled); }

/* 正在说话：左侧绿色细条 + 末端脉动点 */
.member-speaking {
  background: var(--color-success-soft);
  box-shadow: inset 2px 0 0 var(--color-success);
}
.member-speaking-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-success);
  flex-shrink: 0;
  animation: voice-pulse 0.8s ease-in-out infinite;
}
@keyframes voice-pulse {
  0%, 100% { opacity: 0.4; transform: scale(0.8); }
  50% { opacity: 1; transform: scale(1.1); }
}

/* 成员栏底部语音控制条：按钮即音量条（fill 叠在按钮内，scaleX 随音量伸缩） */
.voice-bar {
  position: relative;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  border-top: 1px solid var(--color-border);
  background: var(--color-bg-secondary);
  flex-shrink: 0;
}
/* 本地麦克风音量浮窗：hover 按钮区显示，滚轮 / 滑块调节发送增益 */
.mic-popover {
  position: absolute;
  bottom: calc(100% + 4px);
  left: 12px;
  right: 12px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.25);
  z-index: 20;
}
.mic-slider {
  width: 100%;
  accent-color: var(--color-success);
  cursor: pointer;
}
.voice-btn {
  position: relative;
  overflow: hidden;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  flex: 1;
  height: 36px;
  padding: 0 12px;
  font-size: 13px;
  font-weight: 500;
  line-height: 1;
  border-radius: var(--radius-sm);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  transition: background-color 120ms, border-color 120ms, color 120ms;
}
.voice-btn:hover {
  background: var(--color-hover-overlay);
  color: var(--color-text);
}
.voice-btn.is-on {
  /* 不改背景色：让 voice-fill 在透明底上伸缩更明显，只留绿边框 + 绿字标识开麦 */
  border-color: var(--color-success);
  color: var(--color-success);
}
/* 音量填充层：绝对定位铺满按钮，scaleX 随音量；实色浅绿，边界清晰 */
.voice-fill {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 100%;
  background: var(--color-success-soft);
  transform-origin: left center;
  transform: scaleX(0);
  transition: transform 300ms ease-out;
  z-index: 0;
  pointer-events: none;
}
.voice-ico {
  position: relative;
  z-index: 1;
  width: 16px;
  height: 16px;
  flex-shrink: 0;
}
.voice-txt {
  position: relative;
  z-index: 1;
  flex: 1;
  text-align: left;
}
.voice-f2 {
  position: relative;
  z-index: 1;
  font-size: 12px;
  font-weight: 600;
  color: currentColor;
  opacity: 0.55;
  letter-spacing: 0.03em;
  flex-shrink: 0;
}
/* voice-bar 全局语音开关（扬声器小图标，状态缓存于 localStorage） */
.voice-global {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  padding: 0;
  border-radius: var(--radius-sm);
  border: 1px solid var(--color-success);
  background: transparent;
  color: var(--color-success);
  cursor: pointer;
  flex-shrink: 0;
  transition: color 120ms, border-color 120ms;
}
.voice-global.is-off {
  color: var(--color-text-disabled);
  border-color: var(--color-border-strong);
}
.vg-ico {
  width: 18px;
  height: 18px;
}
.voice-btn.is-disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
/* 加入房间页语音预配置（图标 + 文字按钮，和房间内开关同步） */
.voice-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  width: 100%;
  height: 32px;
  margin-top: 10px;
  margin-bottom: 10px;
  font-size: 13px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--color-success);
  background: transparent;
  color: var(--color-success);
  cursor: pointer;
  transition: color 120ms, border-color 120ms;
}
.voice-toggle.is-off {
  color: var(--color-text-disabled);
  border-color: var(--color-border-strong);
}
.vt-ico {
  width: 16px;
  height: 16px;
}
.vt-txt {
  font-size: 13px;
}
/* 成员项整体作为对方音量条（fill 铺满背景，scaleX 随音量） */
.member {
  position: relative;
  cursor: pointer;
}
.member-active {
  background: var(--color-hover-overlay);
}
.member-mute-ico {
  width: 12px;
  height: 12px;
  color: var(--color-text-disabled);
  flex-shrink: 0;
}
/* 成员音量浮窗：点击成员项显示，调本地播放增益 + 静音 */
.peer-popover {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.25);
  z-index: 30;
}
.peer-slider {
  flex: 1;
  accent-color: var(--color-success);
  cursor: pointer;
}
.peer-mute-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  border-radius: 3px;
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  flex-shrink: 0;
}
.peer-mute-btn.is-on {
  color: var(--color-danger);
  border-color: var(--color-danger);
}
.peer-mute-ico {
  width: 14px;
  height: 14px;
}
.member-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.member-tag {
  font-size: 10px;
  color: var(--color-text-disabled);
  letter-spacing: 0.05em;
}
.member-channel {
  font-size: 10px;
  font-weight: 500;
  padding: 2px 6px;
  border-radius: var(--radius-pill);
  letter-spacing: 0.03em;
  line-height: 1;
}
.ch-p2p {
  background: var(--color-success-soft);
  color: var(--color-success);
}
.ch-relay {
  background: var(--color-warning-soft);
  color: var(--color-warning);
}
.ch-pending {
  color: var(--color-text-disabled);
}

/* 中央聊天区 */
.chat {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: var(--color-bg);
  min-width: 0;
}
.chat-msgs {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.chat-empty {
  text-align: center;
  color: var(--color-text-disabled);
  font-size: 12px;
  margin-top: 24px;
}
.chat-row {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  max-width: 70%;
}
.chat-row.mine {
  align-self: flex-end;
  align-items: flex-end;
}
.chat-meta {
  display: flex;
  gap: 6px;
  font-size: 11px;
  color: var(--color-text-disabled);
  margin-bottom: 4px;
}
.chat-row.mine .chat-meta {
  flex-direction: row-reverse;
}
.chat-nick {
  color: var(--color-text-muted);
  font-weight: 500;
}
.chat-row.mine .chat-nick {
  color: var(--color-accent);
}
.chat-bubble {
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  color: var(--color-text);
  padding: 8px 12px;
  border-radius: var(--radius-md);
  font-size: 13px;
  line-height: 1.45;
  word-break: break-word;
  white-space: pre-wrap;
}
.chat-row.mine .chat-bubble {
  background: var(--color-accent);
  border-color: var(--color-accent);
  color: #fff;
}

.chat-input-row {
  display: flex;
  gap: 8px;
  padding: 12px 16px;
  border-top: 1px solid var(--color-border);
  background: var(--color-bg-secondary);
  flex-shrink: 0;
}
.chat-input-row .input {
  flex: 1;
}

/* ===== 日志面板 ===== */
.log-panel {
  position: relative;
  background: var(--color-bg-secondary);
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}
/* 日志面板关闭按钮（顶栏日志按钮已隐藏，面板内提供关闭入口） */
.log-close {
  position: absolute;
  top: 4px;
  right: 8px;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--color-text-disabled);
  font-size: 16px;
  line-height: 1;
  cursor: pointer;
  z-index: 1;
}
.log-close:hover {
  color: var(--color-text);
}
.log-panel-body {
  max-height: 140px;
  overflow-y: auto;
  padding: 8px 16px;
  font-family: 'JetBrains Mono', Consolas, 'Courier New', monospace;
  font-size: 11px;
  color: var(--color-text-muted);
}
.log-line {
  padding: 1px 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.log-empty {
  color: var(--color-text-disabled);
  text-align: center;
  padding: 4px 0;
}

/* ===== 确认弹窗 ===== */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}
.modal {
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 20px;
  min-width: 320px;
  max-width: 420px;
}
.modal-text {
  margin: 0 0 16px;
  font-size: 13px;
  color: var(--color-text);
  line-height: 1.5;
}
.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

/* ===== 滚动条（极简风格的轻量自定义） ===== */
:deep(::-webkit-scrollbar) {
  width: 8px;
  height: 8px;
}
:deep(::-webkit-scrollbar-track) {
  background: transparent;
}
:deep(::-webkit-scrollbar-thumb) {
  background: var(--color-border-strong);
  border-radius: 4px;
}
:deep(::-webkit-scrollbar-thumb:hover) {
  background: var(--color-text-disabled);
}

/* ===== 顶栏：图标按钮 / 语言切换 / GitHub ===== */
.icon-btn {
  width: 26px;
  padding: 0;
  flex-shrink: 0;
}
.ico {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
}
/* 语言切换浮窗：文/A 翻译按钮 + 下拉 + 透明 backdrop 捕获外部点击关闭 */
.lang-switch {
  position: relative;
  display: inline-flex;
}
.lang-backdrop {
  position: fixed;
  inset: 0;
  z-index: 40;
}
.lang-popover {
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  z-index: 50;
  min-width: 132px;
  padding: 4px;
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.3);
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.lang-option {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 28px;
  padding: 0 10px;
  font-size: 13px;
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  border-radius: 4px;
  cursor: pointer;
  text-align: left;
  transition: background-color 120ms, color 120ms;
}
.lang-option:hover {
  background: var(--color-hover-overlay);
  color: var(--color-text);
}
.lang-option.is-active {
  color: var(--color-accent);
}
.lang-check {
  width: 14px;
  font-size: 12px;
  color: var(--color-accent);
  flex-shrink: 0;
}
.lang-name {
  flex: 1;
}
/* GitHub 标记是实心 path，略缩一点更协调 */
.github-btn .ico {
  width: 15px;
  height: 15px;
}
</style>
