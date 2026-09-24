<script setup>
import {reactive, onMounted, onUnmounted, nextTick, ref, computed, watch} from 'vue'
import {Connect, Disconnect, JoinRoom, LeaveRoom, GetPeers, GetSelf, GetStatus, SendChat, OpenURL, SetTrayMenuState, SetVoiceStatus, TestServer} from '../../wailsjs/go/main/App'
import {EventsOn, EventsOff, Environment, WindowHide, ClipboardGetText, WindowFullscreen, WindowUnfullscreen, WindowIsFullscreen} from '../../wailsjs/runtime/runtime'
import {startVoice} from '../voice'
import {startScreen, screenSupported} from '../screen'
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
  // 服务器测试：testing 进行中、testOk 成功态、testResult 展示串（"{ms} 毫秒" 或 "失败: ..."）。
  testing: false,
  testOk: false,
  testResult: '',
  // 房间框粘贴 netbridge:// 邀请时填充，供提示条展示。
  inviteParsed: null,
  // v2 剪贴板自检：检测到可解析邀请时填充，弹出 toast。
  clipInvite: null,
  // 去重：上次检查过的剪贴板串，相同不重复弹。
  lastClipChecked: '',
  joined: false,
  chat: [],
  log: [],
  micOn: false,
  voice: null,
  activePeerRect: null,
  voiceEnabled: (typeof localStorage !== 'undefined' && localStorage.getItem('netbridge_voice') === 'on'),
  speaking: {},
  selfSpeaking: false,
  micLevel: 0,
  micGainPct: 100,
  showMicPopover: false,
  peerVols: {},
  peerMutes: {},
  activePeerVIP: null,
  peerMenu: null,
  // 投屏：screen 会话对象；screenSharing 本端是否在投屏；screenActive 是否有远端画面可看。
  screen: null,
  screenSharing: false,
  screenActive: false,
  // 投屏时聊天降级为右侧浮层：chatOpen 抽屉开关，chatUnread 未读数；theater 窗口内全屏；sysFs 系统全屏。
  chatOpen: false,
  chatUnread: 0,
  theater: false,
  sysFs: false,
  // 投屏画质：码率(bps) + 采集分辨率上界(宽)，仅投屏方有意义，UI 面板选择；持久化到 localStorage。
  screenBitrate: (typeof localStorage !== 'undefined' && parseInt(localStorage.getItem('netbridge_screen_bitrate'), 10)) || 8000000,
  screenResolution: (typeof localStorage !== 'undefined' && parseInt(localStorage.getItem('netbridge_screen_resolution'), 10)) || 1920,
  showQualityMenu: false,
})
const showLog = ref(false)
const chatMsg = ref('')
const chatEl = ref(null)
const logEl = ref(null)
const screenCanvasEl = ref(null)
const nameEl = ref(null)
let autoTried = false
let refreshTimer = null
let peerLevelTimers = {}
let selfSpeakTimer = null

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

// 信息卡 / 右键菜单的目标 peer：按 activePeerVIP / peerMenu.key 反查，peer 离开列表即自动消失。
const activePeer = computed(() => data.allPeers.find(p => peerKey(p) === data.activePeerVIP) || null)
const menuPeer = computed(() => {
  const k = data.peerMenu && data.peerMenu.key
  return k ? (data.allPeers.find(p => peerKey(p) === k) || null) : null
})

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
  const lat = p.latency >= 0 ? ' · ' + p.latency + 'ms' : ''
  return `${p.nickName} · VIP: ${p.vip} · ${ch}${proto}${lat}${v6}`
}
// 把后端的 status 字符串映射成视觉颜色 token——使用规范里的 4 个状态色之一。
function statusColor(s) {
  const k = statusKey(s)
  if (k === 'connected' || k === 'p2p') return 'success'
  if (k === 'connecting') return 'info'
  if (k === 'punching' || k === 'relay') return 'warning'
  return 'muted'
}

// statusTitle：状态文案 + 协议栈，作为状态徽标的 hover title。
// 协议栈信息从原 IPv4/IPv6 徽标下沉到这里，不再常驻顶栏。
const statusTitle = computed(() => {
  let s = t('status.' + statusKey(data.status))
  if (data.self.isIPv6) s += ' · IPv6'
  else if (data.self.v4) s += ' · IPv4'
  return s
})

function addLog(msg) {
  data.log.push(`[${new Date().toLocaleTimeString()}] ${msg}`)
  if (data.log.length > 200) data.log.shift()
}
function addChat(nick, msg, ts) {
  data.chat.push({nick, msg, ts})
  if (data.chat.length > 200) data.chat.shift()
  // 投屏布局下聊天收起为浮层，未打开则计未读，按钮上显示红点。
  if (screenLayout.value && !data.chatOpen) data.chatUnread++
  nextTick(() => scrollBottom(chatEl))
}
function scrollBottom(el) { if (el?.value) { el.value.scrollTop = el.value.scrollHeight } }
function isMine(nick) { return nick === data.self.nickName || nick === data.nickName }

// ---- 操作 ----

// doJoin 合并原"连接服务器 + 加入房间"两步为一次操作。
// 未进房时表单点击加入：必要时先断开旧连接（后端 Disconnect 已含退房，且避免
// Connect 覆盖未关 socket 泄漏），再 Connect -> JoinRoom。仿 autoConnect 序列。
async function doJoin() {
  if (data.connecting) return
  const server = data.serverAddr.trim()
  if (!server) { data.connectError = '请填写服务器地址'; return }
  const room = data.room.trim() || 'default'
  const name = data.nickName.trim() || 'Player' + Math.floor(Math.random() * 1000)
  data.room = room
  data.nickName = name
  data.connecting = true
  data.connectError = ''
  addLog(`加入 ${server} / ${room} (${name}) ...`)
  try {
    if (data.connected) {
      await Disconnect()
      data.connected = false
    }
    await Connect(server)
    data.connected = true
    addLog('✓ 已连接')
    await refreshStatus()
    await JoinRoom(room, name)
    data.joined = true
    // 占位显示，self:update 到达后替换为带 VIP/公网端点的完整数据。
    data.self = {id: '', nickName: name, vip: '', publicAddr: '', v4: '', v6: '', isIPv6: false}
    saveHistory({ serverAddr: server, room, nickName: name })
    addLog('✓ 已加入房间')
  } catch (e) {
    data.connectError = String(e)
    addLog('✗ 加入失败: ' + e)
  } finally {
    data.connecting = false
  }
}

// 房间框既是房间号输入、也接受粘贴 netbridge:// 邀请串：命中即填 server/room + 提示条。
function onRoomInput(e) {
  const inv = parseInvite(e.target.value)
  if (inv) {
    data.serverAddr = inv.server
    data.inviteParsed = inv
    // 等 v-model 把 URL 写入 data.room 后，再替换为解析出的房间名。
    nextTick(() => { data.room = inv.room })
  } else {
    data.inviteParsed = null
  }
}

// doTestServer 一次性探测服务器连通性（不建持久连接），显示 RTT 或失败。
async function doTestServer() {
  const addr = data.serverAddr.trim()
  if (!addr || data.testing) return
  data.testing = true
  data.testOk = false
  data.testResult = ''
  try {
    const ms = await TestServer(addr)
    data.testOk = true
    data.testResult = t('join.testOk', {ms})
    addLog(`✓ 服务器可达 ${addr} (${ms}ms)`)
  } catch (e) {
    data.testOk = false
    data.testResult = t('join.testFail') + ': ' + e
    addLog('✗ 服务器测试失败: ' + e)
  } finally {
    data.testing = false
  }
}

async function doDisconnect() {
  showConfirm('modal.confirmDisconnect', async () => {
    await Disconnect()
    if (data.voice) { data.voice.stop(); data.voice = null }
    if (data.screen) { data.screen.stop(); data.screen = null }
    data.screenSharing = false
    data.screenActive = false
    data.chatOpen = false
    data.chatUnread = 0
    data.theater = false
    data.micOn = false
    data.micLevel = 0
    data.selfSpeaking = false
    data.speaking = {}
    data.peerVols = {}
    data.peerMutes = {}
    data.activePeerVIP = null
    for (const k in peerLevelTimers) clearTimeout(peerLevelTimers[k])
    peerLevelTimers = {}
    if (selfSpeakTimer) { clearTimeout(selfSpeakTimer); selfSpeakTimer = null }
    data.connected = false
    data.joined = false
    data.allPeers = []
    data.chat = []
    data.self = {id: '', nickName: '', vip: '', publicAddr: '', v4: '', v6: '', isIPv6: false}
    addLog('已断开连接')
  })
}

// 网卡的开启/关闭已由后端在 onSelfUpdate / LeaveRoom 时自动管理——
// 其状态恒等于「已进房」无信息量，创建失败已由后端写入日志，前端不再展示徽标。

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
// 自己正在说话：micLevel 触发（底噪已滤除），与成员同款 600ms 保持，不闪烁
function isSelfSpeaking() {
  return data.micOn && data.selfSpeaking
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
    // 本地音量条与"自己正在说话"分两路：音量条要跟手（快衰减），说话点亮要稳（600ms 保持，不闪烁）。
    onMicLevel: (level) => {
      // 音量条：快衰减（0.8，约 200ms 释放）紧跟实时电平；原 0.97 约 1.5s 尾巴，滞后明显。
      data.micLevel = Math.max(level, data.micLevel * 0.8)
      // 自己正在说话：level>0（底噪已被噪声门滤除）即点亮，600ms 保持后熄灭，与成员一致。
      if (level > 0) {
        data.selfSpeaking = true
        if (selfSpeakTimer) clearTimeout(selfSpeakTimer)
        selfSpeakTimer = setTimeout(() => { data.selfSpeaking = false }, 600)
      }
    },
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
    data.micOn = false; data.micLevel = 0; data.selfSpeaking = false
    if (selfSpeakTimer) { clearTimeout(selfSpeakTimer); selfSpeakTimer = null }
    data.speaking = {}; data.peerVols = {}; data.peerMutes = {}; data.activePeerVIP = null
  } else if (data.voiceEnabled && data.joined && !data.voice) {
    autoStartVoice()
  }
  addLog(data.voiceEnabled ? '语音已启用' : '语音已关闭')
  reportVoiceStatus()
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
// reportVoiceStatus 上报本地语音状态（开语音 / 开麦），供其他成员信息卡展示。仅在进房后有意义。
function reportVoiceStatus() {
  if (data.joined) SetVoiceStatus(data.voiceEnabled, data.micOn).catch(() => {})
}
function channelLabel(p) {
  return p.channel === 'p2p' ? t('peer.channelP2P') : p.channel === 'relay' ? t('peer.channelRelay') : t('peer.channelPending')
}

// ---- 投屏 ----
// 仅 P2P 直连成员能收到画面（Go 侧 SendVideo 的 P2P 闸门保证）；无 P2P peer 时投屏无观众。
const hasP2PPeer = computed(() => data.allPeers.some(p => p.channel === 'p2p'))
// 投屏平台闸门：macOS 第一期不做投屏（WKWebView 没有屏幕采集，见 screen/index.js 的
// screenSupported 注释）。不并进能力检测——即使某个 WebKit 版本碰巧暴露了
// getDisplayMedia / WebCodecs，未在真机上验证过的采集链路也不放出来。
//
// 初值 true：Environment() 是本地 IPC，毫秒级返回；万一它失败，也不该把 Windows
// 的投屏功能关掉。平台信息来自 Go 侧 runtime.GOOS（见 wails 的 runtime.Environment），
// 比 UA 嗅探可靠。
const screenPlatformOK = ref(true)
// 投屏布局触发：本端在投屏或有远端画面时，均采用"画面为主+聊天浮层"布局
// （投屏方画面区显示占位提示 + 码率控件，观看方显示远端画面 + 全屏控件）。
const screenLayout = computed(() => data.screenActive || data.screenSharing)
// 投屏画质档位：分辨率上界(宽) + 码率(bps)，投屏方在画面区右下角面板选择，即时生效。
const RESOLUTION_PRESETS = [
  { label: '720p', value: 1280 },
  { label: '1080p', value: 1920 },
  { label: '1440p', value: 2560 },
  { label: '4K', value: 3840 },
]
const BITRATE_PRESETS = [
  { label: '4 Mbps', value: 4000000 },
  { label: '8 Mbps', value: 8000000 },
  { label: '12 Mbps', value: 12000000 },
  { label: '16 Mbps', value: 16000000 },
]

// autoStartScreen 进房间即启动播放通路 + screen:data 监听（未投屏也能看见别人）。
async function autoStartScreen() {
  if (data.screen || !data.joined) return
  // 不支持的平台（macOS）直接短路：连 screen:data 监听都不注册。
  if (!screenSupported || !screenPlatformOK.value) { addLog(t('screen.unsupported')); return }
  await nextTick()
  if (!screenCanvasEl.value) return
  try {
    data.screen = await startScreen(screenCanvasEl.value, { bitrate: data.screenBitrate, maxWidth: data.screenResolution }, (srcVIP) => {
      data.screenActive = true
    }, () => {
      // 系统"停止共享"条触发：同步本端投屏态回正。
      data.screenSharing = false
      addLog(t('screen.stopped'))
    }, () => {
      // 远端画面消失（源离开 / 超时无帧）：回到无画面态。
      data.screenActive = false
    })
  } catch (e) {
    addLog('✗ 投屏通路启动失败: ' + e)
  }
}

// toggleScreenShare 切换本端投屏。getDisplayMedia 需用户手势，必须在点击内调。
async function toggleScreenShare() {
  if (!data.screen) return
  // 顶栏按钮在不支持的平台不会出现，这里兜住其它触发路径：
  // 否则会落到 startSharing() 返回 false，报出误导性的"投屏启动失败"。
  if (!screenSupported || !screenPlatformOK.value) { addLog(t('screen.unsupported')); return }
  if (data.screenSharing) {
    data.screen.stopSharing()
    data.screenSharing = false
    addLog(t('screen.stopped'))
  } else {
    if (!hasP2PPeer.value) addLog(t('screen.noP2P'))
    const ok = await data.screen.startSharing()
    data.screenSharing = ok
    addLog(ok ? t('screen.started') : t('screen.startFail'))
  }
}
// toggleChat 切换投屏时的聊天浮层；打开时清未读。
function toggleChat() {
  data.chatOpen = !data.chatOpen
  if (data.chatOpen) data.chatUnread = 0
}
// toggleTheater 切换"窗口内全屏"：画面覆盖整个主窗体（含 members），ESC 退出。
function toggleTheater() {
  data.theater = !data.theater
}
// toggleSysFullscreen 切换系统全屏；以 Wails 真实状态为准，避免与框架 ESC 退出不同步。
async function toggleSysFullscreen() {
  try {
    const cur = await WindowIsFullscreen()
    if (cur) { WindowUnfullscreen(); data.sysFs = false }
    else { WindowFullscreen(); data.sysFs = true }
  } catch (e) { addLog('全屏切换失败: ' + e) }
}
// setScreenBitrate 设置投屏码率并即时生效（encoder reconfigure）；持久化到 localStorage。
function setScreenBitrate(bps) {
  data.screenBitrate = bps
  try { localStorage.setItem('netbridge_screen_bitrate', String(bps)) } catch (e) {}
  if (data.screen) data.screen.setBitrate(bps)
}
// setScreenResolution 设置采集分辨率上界并即时生效（capture 下一帧按新尺寸缩放，encoder 检测尺寸变化重配）；持久化。
function setScreenResolution(w) {
  data.screenResolution = w
  try { localStorage.setItem('netbridge_screen_resolution', String(w)) } catch (e) {}
  if (data.screen) data.screen.setResolution(w)
}
// 延迟徽标颜色档：<80ms 绿 / <150ms 黄 / 否则红。
function latencyClass(ms) {
  if (ms < 80) return 'lat-good'
  if (ms < 150) return 'lat-ok'
  return 'lat-bad'
}
async function copyText(text) {
  if (!text) return false
  try { await navigator.clipboard.writeText(text); return true } catch (e) { return false }
}
// VIP 复制：成功后徽标短暂切到「✓ 已复制」绿色态，1.2s 后恢复显示 IP。
const vipCopied = ref(false)
let vipCopiedTimer = null
async function copyVIP() {
  const v = data.self.vip
  if (!v || !(await copyText(v))) return
  vipCopied.value = true
  if (vipCopiedTimer) clearTimeout(vipCopiedTimer)
  vipCopiedTimer = setTimeout(() => { vipCopied.value = false }, 1200)
}

// ---- 邀请串 ----
// netbridge://join?s=<server>&r=<room>（s 必带；暂无默认服务器故不可省）。
// 用此格式以便将来 v3 深链（URL scheme + 单实例）零返工消费同一串。
function buildInvite(server, room) {
  return `netbridge://join?s=${encodeURIComponent(server)}&r=${encodeURIComponent(room)}`
}
function parseInvite(text) {
  if (typeof text !== 'string') return null
  const m = text.trim().match(/^netbridge:\/\/join\?(.*)$/)
  if (!m) return null
  const params = new URLSearchParams(m[1])
  const server = params.get('s')
  const room = params.get('r')
  if (!server || !room) return null
  return {server, room}
}

// 点击房间名复制邀请链接：复用 copyVIP 的"✓ 已复制"短暂高亮。
const roomCopied = ref(false)
let roomCopiedTimer = null
async function copyRoom() {
  if (!data.joined) return
  const v = data.room
  if (!v || !data.serverAddr || !(await copyText(buildInvite(data.serverAddr, v)))) return
  roomCopied.value = true
  if (roomCopiedTimer) clearTimeout(roomCopiedTimer)
  roomCopiedTimer = setTimeout(() => { roomCopied.value = false }, 1200)
}

// ---- v2 剪贴板邀请自检 ----
// 启动后（autoConnect 结算后）与托盘恢复窗口时各检查一次：剪贴板含可解析邀请且
// 未进房 -> 弹 toast 一键加入。lastClipChecked 去重，避免同一串反复弹。
async function checkClipboardInvite() {
  if (data.joined || data.connecting) return
  try {
    const text = await ClipboardGetText()
    if (!text || text === data.lastClipChecked) return
    data.lastClipChecked = text
    const inv = parseInvite(text)
    if (inv) data.clipInvite = inv
  } catch {}
}
function dismissClipInvite() { data.clipInvite = null }
async function joinFromClipboard() {
  const inv = data.clipInvite
  data.clipInvite = null
  if (!inv) return
  data.serverAddr = inv.server
  data.room = inv.room
  data.inviteParsed = inv
  // 名字已填 -> 直接进房（极致便捷，回访用户 nickName 存于 localStorage）；
  // 名字为空 -> 只填表单并聚焦名字框，待用户输入后回车或点"加入"进房。
  // 避免点 toast 时未输名字就用随机 PlayerXXX 进房。
  if (data.nickName.trim()) {
    await doJoin()
  } else {
    await nextTick()
    nameEl.value?.focus()
  }
}
// 信息卡（左键）：只读展示对方连接信息 + 一键复制 IP/端点。再点同一项即收起。
function openPeerInfo(p, e) {
  data.peerMenu = null
  const k = peerKey(p)
  if (data.activePeerVIP === k) { data.activePeerVIP = null; return }
  data.activePeerVIP = k
  // fixed 定位：用点击的 li 坐标算位置，不受父容器 overflow 裁剪；近顶则向上展开。
  const r = e.currentTarget.getBoundingClientRect()
  const above = r.top > 240
  data.activePeerRect = { left: r.left, top: above ? r.top - 6 : r.bottom + 6, above }
}
function peerInfoStyle() {
  if (!data.activePeerRect) return { display: 'none' }
  const r = data.activePeerRect
  return {
    position: 'fixed',
    left: r.left + 'px',
    top: r.top + 'px',
    transform: r.above ? 'translateY(-100%)' : 'none',
  }
}
// 右键菜单：音量 + 静音。数据驱动 items，便于以后扩展（私聊等）。
function openPeerMenu(p, e) {
  data.activePeerVIP = null
  data.peerMenu = { key: peerKey(p), x: e.clientX, y: e.clientY }
}
function peerMenuStyle() {
  if (!data.peerMenu) return { display: 'none' }
  return { position: 'fixed', left: data.peerMenu.x + 'px', top: data.peerMenu.y + 'px' }
}
function closePeerFloats() {
  data.activePeerVIP = null
  data.peerMenu = null
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
    // 闭麦立即清空本地音量条与说话指示，避免残留电平/亮灯悬在半空
    if (!data.micOn) { data.micLevel = 0; data.selfSpeaking = false }
    addLog(data.micOn ? '麦克风已开' : '麦克风已静音')
    reportVoiceStatus()
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
  // ESC：有成员浮层（信息卡 / 右键菜单）时先关浮层；否则最小化到托盘。
  // 聚焦在输入框/文本域时不触发，避免误关正在输入的内容。
  if (e.key === 'Escape') {
    const tag = (e.target && e.target.tagName) || ''
    if (tag !== 'INPUT' && tag !== 'TEXTAREA') {
      e.preventDefault()
      // 优先级：退出窗口内全屏 > 关聊天浮层 > 关成员浮层 > 最小化到托盘
      if (data.theater) data.theater = false
      else if (data.chatOpen) data.chatOpen = false
      else if (data.activePeerVIP || data.peerMenu) closePeerFloats()
      else WindowHide()
    }
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

// syncTray 把当前语言下的菜单文案 + 语音开关状态推给后端托盘。
// 后端只做展示（"语音"项据此打勾），不持有语音业务状态。
// 切换语言 / 切换语音时由下面的 watch 自动同步。
function syncTray() {
  SetTrayMenuState(t('tray.show'), t('tray.voice'), t('tray.quit'), data.voiceEnabled).catch(() => {})
}
// 语言或语音开关变化 -> 重推托盘菜单状态（含打勾与翻译文案）。
watch([locale, () => data.voiceEnabled], () => syncTray())
// 停止投屏时收起画质菜单，避免下次投屏时菜单残留打开。
watch(() => data.screenSharing, (v) => { if (!v) data.showQualityMenu = false })

onMounted(() => {
  // 平台闸门（见 screenPlatformOK）：只有 macOS 需要关掉投屏。
  Environment()
    .then((env) => { if (env && env.platform === 'darwin') screenPlatformOK.value = false })
    .catch(() => {}) // 取不到环境就按支持处理，别让 Windows 失去投屏入口
  EventsOn('status:change', (s) => {
    data.status = s
    if (statusKey(s) === 'connected') {
      data.joined = true; refreshStatus()
      if (!refreshTimer) refreshTimer = setInterval(refreshStatus, 2000)
    }
    // 注意：不在「连接中」时置 connected=true——握手期间后端状态先到「连接中」，
    // 若此时翻页会过早进入房间页。connected 由 doJoin/autoConnect 握手成功后显式置位。
    if (statusKey(s) === 'disconnected') {
      data.connected = false; data.joined = false
      if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null }
    }
  })
  EventsOn('peer:update', (peers) => {
    const list = peers || []
    // 清理已离开 peer 的播放解码器，避免 players Map 累积泄漏 AudioDecoder（直到退房才整体 close）。
    if (data.voice || data.screen) {
      const stay = new Set(list.map(peerVIPNum))
      for (const p of data.allPeers) {
        const v = peerVIPNum(p)
        if (!stay.has(v)) {
          if (data.voice) data.voice.removePeer(v)
          if (data.screen) data.screen.removePeer(v)
        }
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
    if (self.vip && data.joined && !data.screen) autoStartScreen()
    // 此时服务端已注册本 peer，上报一次初始语音状态供其他成员信息卡展示。
    reportVoiceStatus()
  })
  EventsOn('chat:message', (c) => { addChat(c.nickName, c.message, c.timestamp) })
  EventsOn('log:message', (msg) => { addLog(msg) })
  // 单个 peer 延迟增量更新：只改对应成员卡的徽标，不重渲整列。
  EventsOn('latency:update', (peerID, ms) => {
    const p = data.allPeers.find(x => x.id === peerID)
    if (p) p.latency = ms
  })
  // 托盘右键"切换语音" -> 复用全局语音开关（状态权威仍在前端，后端只转发事件）
  EventsOn('tray:toggle-voice', () => toggleVoiceEnabled())
  // 托盘恢复窗口 -> 触发剪贴板邀请自检（v2）
  EventsOn('window:shown', () => checkClipboardInvite())
  // 初次推送托盘菜单状态（watch 只在变化时触发，挂载时需手动推一次）
  syncTray()
  // F2 快捷开关麦克风
  window.addEventListener('keydown', onKeydown)
  // 窗口重获焦点（Alt-Tab / 点击切回，非托盘恢复）也触发剪贴板自检：
  // window:shown 只在托盘恢复时推，覆盖不到"窗口一直可见、仅失焦再聚焦"的场景。
  window.addEventListener('focus', checkClipboardInvite)
  // 尝试自动连接
  setTimeout(autoConnect, 500)
  // 自动连接结算后，若仍未进房，检查剪贴板是否含邀请（v2）
  setTimeout(checkClipboardInvite, 800)
})
onUnmounted(() => {
  if (data.voice) { data.voice.stop(); data.voice = null }
  if (data.screen) { data.screen.stop(); data.screen = null }
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('focus', checkClipboardInvite)
  EventsOff('status:change')
  EventsOff('peer:update')
  EventsOff('self:update')
  EventsOff('chat:message')
  EventsOff('log:message')
  EventsOff('latency:update')
  EventsOff('tray:toggle-voice')
  EventsOff('window:shown')
  if (refreshTimer) clearInterval(refreshTimer)
  if (vipCopiedTimer) clearTimeout(vipCopiedTimer)
  if (roomCopiedTimer) clearTimeout(roomCopiedTimer)
})
</script>

<template>
  <main class="app">
    <!-- 顶栏：左侧产品名+状态信息；右侧动作按钮 -->
    <header class="topbar">
      <div class="topbar-left">
        <!-- 房间内显示房间号（更有上下文价值），其他场景显示产品名 -->
        <span class="brand" :class="{ 'brand-clickable': data.joined, 'is-copied': roomCopied }"
              :title="data.joined ? t('topbar.roomCopyTip') : ''"
              :role="data.joined ? 'button' : undefined" :tabindex="data.joined ? 0 : undefined"
              @click="copyRoom" @keydown.enter="copyRoom">
          <template v-if="roomCopied">✓ {{ t('topbar.copied') }}</template>
          <template v-else>{{ data.joined ? data.room : 'NetBridge' }}</template>
        </span>
        <span class="status" :class="'status-' + statusColor(data.status)" :title="statusTitle">
          <span class="status-dot"></span>
          <span class="status-text">{{ t('status.' + statusKey(data.status)) }}</span>
        </span>
        <!-- VIP：点击复制，成功后短暂显示「✓ 已复制」。协议栈信息折进上方状态的 hover title -->
        <span v-if="data.self.vip" class="badge badge-mono badge-clickable"
              :class="{ 'is-copied': vipCopied }"
              :title="t('topbar.vipTip')" role="button" tabindex="0"
              @click="copyVIP" @keydown.enter="copyVIP">
          <template v-if="vipCopied">✓ {{ t('topbar.copied') }}</template>
          <template v-else>{{ data.self.vip }}</template>
        </span>
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
        <!-- 投屏：低频动作，置顶栏 icon-btn；仅在有 P2P 直连成员（或本端正在投屏）时浮现，无观众时不占视觉权重 -->
        <!-- screenSupported / screenPlatformOK：不支持的平台（macOS、老 WebView2）不出现入口 -->
        <button v-if="data.joined && screenSupported && screenPlatformOK && (hasP2PPeer || data.screenSharing)"
                @click="toggleScreenShare"
                class="btn btn-ghost btn-sm icon-btn screen-icon-btn"
                :class="{ 'is-on': data.screenSharing }"
                :title="data.screenSharing ? t('screen.sharing') : t('screen.shareTip')"
                :aria-label="t('screen.share')">
          <svg class="ico" viewBox="0 0 24 24" fill="currentColor">
            <path d="M3 4h18c1.1 0 2 .9 2 2v10c0 1.1-.9 2-2 2h-7v2h3v2H7v-2h3v-2H3c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/>
          </svg>
        </button>
        <button v-if="data.connected" @click="doDisconnect" class="btn btn-ghost btn-sm btn-danger-ghost">{{ t('topbar.disconnect') }}</button>
      </div>
    </header>

    <!-- 日志面板：可折叠的辅助信息区（顶栏日志按钮已隐藏，Ctrl+Shift+L 切换） -->
    <!-- v2：剪贴板检测到邀请时的一键加入浮条 -->
    <div v-if="data.clipInvite" class="invite-toast">
      <span class="invite-toast-txt">{{ t('invite.detected', {room: data.clipInvite.room, server: data.clipInvite.server}) }}</span>
      <button @click="joinFromClipboard" class="btn btn-primary btn-sm">{{ t('invite.join') }}</button>
      <button @click="dismissClipInvite" class="btn btn-ghost btn-sm">{{ t('invite.ignore') }}</button>
    </div>

    <div v-if="showLog" class="log-panel">
      <button class="log-close" @click="showLog = false" :title="t('log.close')" aria-label="×">×</button>
      <div class="log-panel-body" ref="logEl">
        <div v-for="(m, i) in data.log" :key="i" class="log-line">{{ m }}</div>
        <div v-if="data.log.length === 0" class="log-empty">{{ t('log.empty') }}</div>
      </div>
    </div>

    <!-- 合并加入表单：服务器 + 房间/邀请 + 昵称（未进房时显示，连接/加入一步完成） -->
    <div v-if="!data.joined" class="centered">
      <section class="card auth-card">
        <!-- 服务器：输入 + 一次性测试连通性 -->
        <div class="field-label">{{ t('join.serverLabel') }}</div>
        <div class="row">
          <input v-model="data.serverAddr"
                 @keyup.enter="doJoin"
                 :disabled="data.connecting"
                 :placeholder="t('connect.placeholder')"
                 class="input"/>
          <button @click="doTestServer" :disabled="data.testing || data.connecting" class="btn btn-ghost test-btn">
            {{ t('join.test') }}
          </button>
        </div>
        <p class="test-result" :class="{ 'is-ok': data.testOk }">{{ data.testResult }}</p>

        <!-- 房间：房间号 / 粘贴邀请（满宽，与昵称框等宽） -->
        <div class="field-label">{{ t('join.roomLabel') }}</div>
        <input v-model="data.room"
               @input="onRoomInput"
               @keyup.enter="doJoin"
               :disabled="data.connecting"
               :placeholder="t('join.roomOrInvitePlaceholder')"
               class="input"/>
        <p v-if="data.inviteParsed" class="invite-hint">✓ {{ t('invite.parsed', {room: data.inviteParsed.room, server: data.inviteParsed.server}) }}</p>

        <div class="field-label">{{ t('join.nameLabel') }}</div>
        <input v-model="data.nickName"
               ref="nameEl"
               @keyup.enter="doJoin"
               :disabled="data.connecting"
               :placeholder="t('join.namePlaceholder')"
               class="input"/>

        <button @click="doJoin"
                :disabled="data.connecting"
                class="btn btn-primary btn-block">
          {{ data.connecting ? t('connect.connecting') : t('join.button') }}
        </button>
        <label class="voice-check">
          <input type="checkbox" :checked="data.voiceEnabled" @change="toggleVoiceEnabled"/>
          <span>{{ t('voice.enableLabel') }}</span>
        </label>
        <p v-if="data.connectError" class="auth-error">{{ t('connect.failed') }}{{ data.connectError }}</p>
      </section>
    </div>

    <!-- 主聊天页：左成员 + 右聊天 -->
    <section v-else class="room" :class="{ 'has-screen': screenLayout }">
      <!-- 左侧成员列表 -->
      <aside class="members">
        <div class="members-head">
          <span class="members-title">{{ t('members.title') }}</span>
          <span class="members-count">{{ others.length + 1 }}</span>
        </div>
        <ul class="member-list">
          <!-- 自己 -->
          <li class="member member-self" :class="{ 'member-speaking': isSelfSpeaking() }">
            <span class="member-dot dot-self"></span>
            <span class="member-name">{{ data.self.nickName || '...' }}</span>
            <span v-if="isSelfSpeaking()" class="member-speaking-dot" :title="t('voice.speaking')"></span>
            <span class="member-tag">{{ t('members.self') }}</span>
          </li>
          <!-- 其他人 -->
          <li v-for="p in others"
              :key="p.id"
              class="member"
              :class="{ 'member-speaking': isSpeaking(p), 'member-active': isPeerPopover(p) }"
              :title="peerTitle(p)"
              @click="openPeerInfo(p, $event)"
              @contextmenu.prevent="openPeerMenu(p, $event)">
            <span class="member-dot"
                  :class="p.channel === 'p2p' ? 'dot-p2p' : p.channel === 'relay' ? 'dot-relay' : 'dot-pending'"></span>
            <span class="member-name">{{ p.nickName }}</span>
            <span v-if="isSpeaking(p)" class="member-speaking-dot" :title="t('voice.speaking')"></span>
            <MicIcon v-if="isPeerMuted(p)" muted class="member-mute-ico" :title="t('voice.mutedForYou')"/>
            <span v-if="p.channel === 'p2p'" class="member-channel ch-p2p">P2P</span>
            <span v-else-if="p.channel === 'relay'" class="member-channel ch-relay">{{ t('peer.badgeRelay') }}</span>
            <span v-else class="member-channel ch-pending">…</span>
            <span v-if="p.latency >= 0" class="member-latency" :class="latencyClass(p.latency)" :title="t('peer.infoLatency')">{{ p.latency }}ms</span>
          </li>
        </ul>
        <!-- 成员浮层：信息卡(左键) + 右键菜单，同时只显示一个；点遮罩 / ESC 关闭 -->
        <div class="peer-backdrop" v-if="activePeer || menuPeer" @click="closePeerFloats" @contextmenu.prevent="closePeerFloats"></div>
        <div class="peer-info" v-if="activePeer" :style="peerInfoStyle()" @click.stop>
          <div class="info-head">
            <span class="info-name">{{ activePeer.nickName }}</span>
            <button class="info-close" @click="closePeerFloats" :title="t('log.close')" aria-label="×">×</button>
          </div>
          <div class="info-row">
            <span class="info-k">{{ t('peer.infoVIP') }}</span>
            <span class="info-v">{{ activePeer.vip }} <button class="copy-btn" @click="copyText(activePeer.vip)" :title="t('peer.copy')">{{ t('peer.copy') }}</button></span>
          </div>
          <div class="info-row">
            <span class="info-k">{{ t('peer.infoChannel') }}</span>
            <span class="info-v">{{ channelLabel(activePeer) }}</span>
          </div>
          <div class="info-row" v-if="activePeer.v4">
            <span class="info-k">IPv4</span>
            <span class="info-v info-mono">{{ activePeer.v4 }} <button class="copy-btn" @click="copyText(activePeer.v4)" :title="t('peer.copy')">{{ t('peer.copy') }}</button></span>
          </div>
          <div class="info-row" v-if="activePeer.v6">
            <span class="info-k">IPv6</span>
            <span class="info-v info-mono">{{ activePeer.v6 }} <button class="copy-btn" @click="copyText(activePeer.v6)" :title="t('peer.copy')">{{ t('peer.copy') }}</button></span>
          </div>
          <div class="info-row">
            <span class="info-k">{{ t('peer.infoVoice') }}</span>
            <span class="info-v" :class="activePeer.voiceOn ? 'st-on' : 'st-off'">{{ activePeer.voiceOn ? t('peer.infoOn') : t('peer.infoOff') }}</span>
          </div>
          <div class="info-row">
            <span class="info-k">{{ t('peer.infoMic') }}</span>
            <span class="info-v" :class="activePeer.micOn ? 'st-on' : 'st-off'">{{ activePeer.micOn ? t('peer.infoOn') : t('peer.infoOff') }}</span>
          </div>
          <div class="info-row">
            <span class="info-k">{{ t('peer.infoLatency') }}</span>
            <span class="info-v">{{ activePeer.latency >= 0 ? activePeer.latency + ' ms' : '-' }}</span>
          </div>
        </div>
        <!-- 右键菜单：音量 + 静音（数据驱动，便于以后扩展） -->
        <div class="peer-menu" v-if="menuPeer" :style="peerMenuStyle()" @click.stop>
          <div class="menu-item menu-volume">
            <span class="menu-label">{{ t('peer.menuVolume') }}</span>
            <input type="range" min="0" max="100" :value="peerVolPct(menuPeer)" @input="onPeerVolInput(menuPeer, $event)" class="peer-slider"/>
          </div>
          <button class="menu-item menu-mute-btn" :class="{ 'is-on': isPeerMuted(menuPeer) }" @click="togglePeerMute(menuPeer)">
            <MicIcon :muted="isPeerMuted(menuPeer)" class="peer-mute-ico"/>
            <span>{{ isPeerMuted(menuPeer) ? t('peer.menuUnmute') : t('peer.menuMute') }}</span>
          </button>
        </div>
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

      <!-- 中间投屏画面区：投屏(发或看)时顶到中间，聊天退到右侧浮层 -->
      <section class="screen-stage" v-if="data.joined" v-show="screenLayout" :class="{ theater: data.theater }">
        <canvas ref="screenCanvasEl" class="screen-canvas"></canvas>
        <!-- 投屏方无远端画面时的占位提示 -->
        <div v-if="data.screenSharing && !data.screenActive" class="screen-placeholder">{{ t('screen.sharing') }}</div>
        <!-- 画面控件（右下角）：码率(投屏方) / 聊天(未读) / 窗口内全屏(观看方) / 系统全屏(观看方) -->
        <div class="screen-controls">
          <div v-if="data.screenSharing" class="sc-group">
            <button class="sc-btn" @click="data.showQualityMenu = !data.showQualityMenu" :class="{ 'is-on': data.showQualityMenu }" :title="t('screen.quality')" :aria-label="t('screen.quality')">
              <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
            </button>
            <div v-if="data.showQualityMenu" class="sc-menu sc-quality" @click.stop>
              <div class="sc-menu-title">{{ t('screen.resolution') }}</div>
              <button v-for="r in RESOLUTION_PRESETS" :key="r.value" class="sc-menu-item" :class="{ 'is-active': data.screenResolution === r.value }" @click="setScreenResolution(r.value)">{{ r.label }}</button>
              <div class="sc-menu-divider"></div>
              <div class="sc-menu-title">{{ t('screen.bitrate') }}</div>
              <button v-for="b in BITRATE_PRESETS" :key="b.value" class="sc-menu-item" :class="{ 'is-active': data.screenBitrate === b.value }" @click="setScreenBitrate(b.value)">{{ b.label }}</button>
            </div>
          </div>
          <button class="sc-btn" @click="toggleChat" :class="{ 'is-on': data.chatOpen }" :title="t('chat.title')" :aria-label="t('chat.title')">
            <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
            <span v-if="data.chatUnread" class="sc-badge">{{ data.chatUnread > 99 ? '99+' : data.chatUnread }}</span>
          </button>
          <button v-if="data.screenActive" class="sc-btn" @click="toggleTheater" :class="{ 'is-on': data.theater }" :title="t('screen.theater')" :aria-label="t('screen.theater')">
            <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="5" width="18" height="14" rx="1"/><rect x="7" y="9" width="10" height="6" fill="currentColor" stroke="none"/></svg>
          </button>
          <button v-if="data.screenActive" class="sc-btn" @click="toggleSysFullscreen" :class="{ 'is-on': data.sysFs }" :title="t('screen.fullscreen')" :aria-label="t('screen.fullscreen')">
            <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3H5a2 2 0 0 0-2 2v3M16 3h3a2 2 0 0 1 2 2v3M21 16v3a2 2 0 0 1-2 2h-3M3 16v3a2 2 0 0 0 2 2h3"/></svg>
          </button>
        </div>
        <!-- 聊天浮层遮罩：点画面区关闭 -->
        <div class="chat-overlay" v-if="data.chatOpen" @click="data.chatOpen = false"></div>
        <!-- 画质菜单遮罩：点画面区关闭 -->
        <div class="sc-overlay" v-if="data.showQualityMenu" @click="data.showQualityMenu = false"></div>
      </section>

      <!-- 聊天区：无投屏时常驻 flex:1；有投屏时变右侧滑出浮层（.open 滑入） -->
      <section class="chat" :class="{ open: data.chatOpen }">
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
/* VIP 徽标可点击复制：hover 提示可交互 */
.badge-clickable {
  cursor: pointer;
  transition: color .15s, border-color .15s, background .15s;
}
.badge-clickable:hover {
  color: var(--color-text);
  border-color: var(--color-text-muted);
}
/* 复制成功态：实心绿底白字，明确反馈 */
.badge-clickable.is-copied {
  background: var(--color-success);
  color: #fff;
  border-color: var(--color-success);
}

/* 房间名可点击复制邀请链接（joined 时），与 VIP 徽标复制态同款绿色反馈 */
.brand-clickable {
  cursor: pointer;
  transition: color .15s;
}
.brand-clickable:hover {
  color: var(--color-accent);
}
.brand.is-copied {
  color: var(--color-success);
}

/* 邀请解析提示条：房间框粘贴 netbridge:// 邀请后展示 */
.invite-hint {
  margin: -2px 0 2px;
  font-size: 12px;
  color: var(--color-success);
}

/* v2 剪贴板邀请 toast：顶部横条 */
.invite-toast {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 8px auto 0;
  padding: 8px 12px;
  max-width: 560px;
  background: var(--color-success-soft);
  border: 1px solid var(--color-success);
  border-radius: var(--radius-md);
  font-size: 13px;
  color: var(--color-text);
}
.invite-toast-txt {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ===== 按钮 ===== */
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 32px;
  padding: 0 14px;
  box-sizing: border-box;
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
/* 禁用态统一置灰：测试中/连接中按钮文案不变，靠此传达"忙"状态 */
.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
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
  margin: 0 0 4px;
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
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.auth-error {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--color-danger);
  word-break: break-word;
}

/* 合并表单：区块标签 + 服务器行内测试 + 语音/加入邀请各半 + 加入满宽 */
.field-label {
  font-size: 11px;
  font-weight: 500;
  color: var(--color-text-muted);
  letter-spacing: 0.02em;
}
.row {
  display: flex;
  gap: 8px;
}
.row .input {
  flex: 1;
  min-width: 0;
}
/* 测试按钮：覆盖 .btn 默认 32px，与输入框等高 36px */
.row .test-btn {
  flex-shrink: 0;
  height: 36px;
  min-width: 80px;
}
.test-result {
  min-height: 18px;
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--color-danger);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.test-result.is-ok {
  color: var(--color-success);
}
/* 语音复选框（加入按钮下方） */
.voice-check {
  display: flex;
  align-self: flex-start;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--color-text-muted);
  cursor: pointer;
  user-select: none;
}
.voice-check input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-accent);
  cursor: pointer;
}

/* ===== 房间布局 ===== */
.room {
  flex: 1;
  display: flex;
  overflow: hidden;
  min-height: 0;
  position: relative;
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
  transition: transform 80ms ease-out;
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
/* 成员浮层：左键信息卡 + 右键菜单；遮罩层负责点外面 / 右键关闭 */
.peer-backdrop {
  position: fixed;
  inset: 0;
  z-index: 40;
}
.peer-info {
  position: fixed;
  width: 260px;
  padding: 8px 10px;
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.35);
  z-index: 50;
  font-size: 13px;
}
.info-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--color-border);
}
.info-name { font-weight: 600; }
.info-close {
  width: 20px; height: 20px; padding: 0;
  border: none; background: transparent;
  color: var(--color-text-muted); cursor: pointer;
  font-size: 16px; line-height: 1;
}
.info-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 3px 0;
}
.info-k {
  color: var(--color-text-muted);
  flex: 0 0 64px;
}
.info-v {
  flex: 1;
  word-break: break-all;
  display: flex;
  align-items: center;
  gap: 6px;
}
.info-mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
.copy-btn {
  margin-left: auto;
  padding: 1px 6px;
  border: 1px solid var(--color-border-strong);
  border-radius: 3px;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 11px;
  cursor: pointer;
}
.copy-btn:hover { color: var(--color-text); border-color: var(--color-text-muted); }
.st-on { color: var(--color-success); }
.st-off { color: var(--color-text-disabled); }
.peer-menu {
  position: fixed;
  min-width: 180px;
  padding: 4px;
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.35);
  z-index: 50;
}
.menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-radius: 3px;
  font-size: 13px;
}
.menu-volume .peer-slider { flex: 1; accent-color: var(--color-success); cursor: pointer; }
.menu-label { color: var(--color-text-muted); flex: 0 0 36px; }
.menu-mute-btn {
  width: 100%;
  border: none;
  background: transparent;
  color: var(--color-text);
  cursor: pointer;
}
.menu-mute-btn:hover { background: var(--color-hover-overlay); }
.menu-mute-btn.is-on { color: var(--color-danger); }
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
.member-latency {
  font-size: 10px;
  font-weight: 500;
  padding: 2px 5px;
  border-radius: var(--radius-pill);
  letter-spacing: 0.02em;
  line-height: 1;
  font-variant-numeric: tabular-nums;
}
.lat-good { background: var(--color-success-soft); color: var(--color-success); }
.lat-ok   { background: var(--color-warning-soft); color: var(--color-warning); }
.lat-bad  { background: var(--color-danger-soft); color: var(--color-danger); }

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
/* 投屏：顶栏 icon-btn；正在投屏时高亮 accent */
.screen-icon-btn.is-on {
  color: var(--color-accent);
  border-color: var(--color-accent);
}
/* 中间投屏画面区：有投屏(screenActive)时显示并 flex:1 撑满中间，无投屏时 v-show 隐藏。
   has-screen 同时把聊天退为右侧 340px 侧栏（见 .room.has-screen .chat）。 */
.screen-stage {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: 1;
  min-width: 0;
  min-height: 0;
  background: #000;
  overflow: hidden;
  position: relative;
}
/* 窗口内全屏：画面覆盖整个 .room（含 members），ESC 或再点按钮退出。 */
.screen-stage.theater {
  position: absolute;
  inset: 0;
  z-index: 50;
}
.screen-canvas {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
}
/* 画面控件（右下角浮层）*/
.screen-controls {
  position: absolute;
  bottom: 12px;
  right: 12px;
  display: flex;
  gap: 6px;
  z-index: 30;
}
/* 投屏方无远端画面时的占位提示 */
.screen-placeholder {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: rgba(255, 255, 255, 0.45);
  font-size: 14px;
  pointer-events: none;
}
/* 码率按钮 + 下拉菜单容器 */
.sc-group { position: relative; }
.sc-menu {
  position: absolute;
  bottom: 40px;
  right: 0;
  min-width: 96px;
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 4px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  z-index: 40;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
}
.sc-menu-item {
  white-space: nowrap;
  padding: 6px 12px;
  background: transparent;
  color: var(--color-text);
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
  text-align: left;
}
.sc-menu-item:hover { background: rgba(255, 255, 255, 0.06); }
.sc-menu-item.is-active { color: var(--color-accent); font-weight: 600; }
/* 画质面板：比纯码率下拉更宽，含分辨率 + 码率两组 + 分隔。 */
.sc-quality { min-width: 124px; }
.sc-menu-title {
  padding: 6px 12px 2px;
  font-size: 11px;
  color: var(--color-text);
  opacity: 0.5;
}
.sc-menu-divider {
  height: 1px;
  margin: 4px 8px;
  background: var(--color-border);
}
/* 画质菜单遮罩：点画面区关闭菜单。z-index 须低于 screen-controls(30)，否则会盖住
   菜单（菜单在 controls 的层叠上下文内，实际层级被 30 封顶，遮罩高于 30 就点不到菜单项）。 */
.sc-overlay {
  position: absolute;
  inset: 0;
  z-index: 25;
  cursor: pointer;
}
.sc-btn {
  position: relative;
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.5);
  color: #fff;
  border: 1px solid rgba(255, 255, 255, 0.2);
  border-radius: 6px;
  cursor: pointer;
}
.sc-btn:hover { background: rgba(0, 0, 0, 0.75); }
.sc-btn.is-on { background: var(--color-accent); border-color: var(--color-accent); }
.sc-btn .ico { width: 16px; height: 16px; }
.sc-badge {
  position: absolute;
  top: -6px;
  right: -6px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  background: #e53935;
  color: #fff;
  font-size: 10px;
  line-height: 16px;
  border-radius: 8px;
  text-align: center;
}
/* 聊天浮层遮罩：投屏+打开时覆盖画面区，点击关闭。 */
.chat-overlay {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.3);
  z-index: 20;
  cursor: pointer;
}
/* 聊天：无投屏时是常驻 flex:1 列；有投屏时变右侧滑出浮层（默认隐藏，.open 滑入）。
   z-index 60 高于 theater(50)，窗口内全屏时也能拉出聊天。 */
.room.has-screen .chat {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 340px;
  flex: none;
  transform: translateX(100%);
  transition: transform 0.2s ease;
  z-index: 60;
  background: var(--color-bg-secondary);
  border-left: 1px solid var(--color-border);
  box-shadow: -4px 0 16px rgba(0, 0, 0, 0.3);
}
.room.has-screen .chat.open { transform: translateX(0); }
</style>
