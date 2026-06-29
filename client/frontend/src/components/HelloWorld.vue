<script setup>
import {reactive, onMounted, onUnmounted, nextTick, ref, computed} from 'vue'
import {Connect, Disconnect, JoinRoom, LeaveRoom, GetPeers, GetSelf, GetStatus, SendChat} from '../../wailsjs/go/main/App'
import {EventsOn, EventsOff} from '../../wailsjs/runtime/runtime'

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
  joined: false,
  tunActive: false,
  chat: [],
  log: [],
})
const showLog = ref(false)
const chatMsg = ref('')
const chatEl = ref(null)
const logEl = ref(null)
let autoTried = false
let refreshTimer = null

// 自定义确认弹窗
const confirmMsg = ref('')
const confirmAction = ref(null)
function showConfirm(msg, action) {
  confirmMsg.value = msg
  confirmAction.value = action
}
function doConfirm() {
  if (confirmAction.value) confirmAction.value()
  confirmMsg.value = ''
  confirmAction.value = null
}
function cancelConfirm() {
  confirmMsg.value = ''
  confirmAction.value = null
}

const others = computed(() => data.allPeers)

// 兼容服务端返回的两种 IPv6 标识：通道是否走 v6 / peer 是否暴露 v6 端点。
const isPeerV6 = (p) => !!(p.isIPv6 || p.IsIPv6)
const peerHasV6 = (p) => !!(p.v6 || p.V6)
// channel 文案（hover title 用）；不再依赖 emoji 在主视图里表达。
function peerTitle(p) {
  const ch = p.channel === 'p2p' ? 'P2P 直连' : p.channel === 'relay' ? '服务器中转' : '连接中'
  const proto = isPeerV6(p) ? ' · IPv6' : (p.channel === 'p2p' ? ' · IPv4' : '')
  const v6 = peerHasV6(p) && !isPeerV6(p) ? '\n候选 IPv6: ' + (p.v6 || p.V6) : ''
  return `${p.nickName} · VIP: ${p.vip} · ${ch}${proto}${v6}`
}
// 把后端的 status 字符串映射成视觉颜色 token——使用规范里的 4 个状态色之一。
function statusColor(s) {
  if (s === '已连接' || s === 'P2P直连') return 'success'
  if (s === '连接中') return 'info'
  if (s === '打洞中' || s === '服务器中转') return 'warning'
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
  if (!addr) return
  addLog(`连接 ${addr} ...`)
  try {
    await Connect(addr)
    data.connected = true
    saveHistory({ serverAddr: addr, room: data.room, nickName: data.nickName })
    addLog('✓ 已连接')
    refreshStatus()
  } catch (e) {
    addLog('✗ 连接失败: ' + e)
  }
}

async function doDisconnect() {
  showConfirm('确定要断开连接吗？', async () => {
    await Disconnect()
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
  showConfirm('确定要退出房间吗？', () => {
    LeaveRoom()
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
  } catch (e) { data.connected = false }
}

// ---- 事件 ----

onMounted(() => {
  EventsOn('status:change', (s) => {
    data.status = s
    if (s === '已连接') {
      data.joined = true; refreshStatus()
      if (!refreshTimer) refreshTimer = setInterval(refreshStatus, 2000)
    }
    if (s === '连接中') data.connected = true
    if (s === '未连接') {
      data.connected = false; data.joined = false
      if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null }
    }
  })
  EventsOn('peer:update', (peers) => { data.allPeers = peers || [] })
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
  })
  EventsOn('chat:message', (c) => { addChat(c.nickName, c.message, c.timestamp) })
  EventsOn('log:message', (msg) => { addLog(msg) })
  EventsOn('tun:active', (active) => { data.tunActive = !!active })
  // 尝试自动连接
  setTimeout(autoConnect, 500)
})
onUnmounted(() => {
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
          <span class="status-text">{{ data.status }}</span>
        </span>
        <span v-if="data.self.vip" class="badge badge-mono" title="虚拟 IP">{{ data.self.vip }}</span>
        <span v-if="data.tunActive" class="badge badge-info" title="虚拟网卡已激活">TUN</span>
        <span v-if="data.self.isIPv6" class="badge badge-primary" title="服务端可经 IPv6 联系到本机">IPv6</span>
        <span v-else-if="data.self.v4" class="badge badge-ghost" title="仅 IPv4 公网端点">IPv4</span>
      </div>
      <div class="topbar-right">
        <button @click="showLog = !showLog" class="btn btn-ghost btn-sm" :class="{ 'is-active': showLog }">日志</button>
        <button v-if="data.joined" @click="doLeaveRoom" class="btn btn-ghost btn-sm">退出房间</button>
        <button v-if="data.connected" @click="doDisconnect" class="btn btn-ghost btn-sm btn-danger-ghost">断开</button>
      </div>
    </header>

    <!-- 日志面板：可折叠的辅助信息区 -->
    <div v-if="showLog" class="log-panel">
      <div class="log-panel-body" ref="logEl">
        <div v-for="(m, i) in data.log" :key="i" class="log-line">{{ m }}</div>
        <div v-if="data.log.length === 0" class="log-empty">暂无日志</div>
      </div>
    </div>

    <!-- 连接服务器页 -->
    <div v-if="!data.connected" class="centered">
      <section class="card auth-card">
        <h2 class="card-title">连接服务器</h2>
        <input v-model="data.serverAddr"
               @keyup.enter="doConnect"
               placeholder="服务器地址，例如 1.2.3.4:10555"
               class="input"/>
        <button @click="doConnect" class="btn btn-primary btn-block">连接</button>
      </section>
    </div>

    <!-- 加入房间页 -->
    <div v-else-if="!data.joined" class="centered">
      <section class="card auth-card">
        <h2 class="card-title">加入房间</h2>
        <input v-model="data.room" @keyup.enter="doJoinRoom" placeholder="房间号" class="input"/>
        <input v-model="data.nickName" @keyup.enter="doJoinRoom" placeholder="昵称" class="input"/>
        <button @click="doJoinRoom" class="btn btn-primary btn-block">加入</button>
      </section>
    </div>

    <!-- 主聊天页：左成员 + 右聊天 -->
    <section v-else class="room">
      <!-- 左侧成员列表 -->
      <aside class="members">
        <div class="members-head">
          <span class="members-title">成员</span>
          <span class="members-count">{{ others.length + 1 }}</span>
        </div>
        <ul class="member-list">
          <!-- 自己 -->
          <li class="member member-self">
            <span class="member-dot dot-self"></span>
            <span class="member-name">{{ data.self.nickName || '...' }}</span>
            <span class="member-tag">我</span>
          </li>
          <!-- 其他人 -->
          <li v-for="p in others"
              :key="p.id"
              class="member"
              :title="peerTitle(p)">
            <span class="member-dot"
                  :class="p.channel === 'p2p' ? 'dot-p2p' : p.channel === 'relay' ? 'dot-relay' : 'dot-pending'"></span>
            <span class="member-name">{{ p.nickName }}</span>
            <span v-if="p.channel === 'p2p'" class="member-channel ch-p2p">P2P</span>
            <span v-else-if="p.channel === 'relay'" class="member-channel ch-relay">中转</span>
            <span v-else class="member-channel ch-pending">…</span>
          </li>
        </ul>
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
          <div v-if="data.chat.length === 0" class="chat-empty">暂无消息</div>
        </div>
        <div class="chat-input-row">
          <input v-model="chatMsg" @keyup.enter="doSendChat" placeholder="输入消息..." class="input"/>
          <button @click="doSendChat" class="btn btn-primary">发送</button>
        </div>
      </section>
    </section>

    <!-- 确认弹窗 -->
    <div v-if="confirmMsg" class="modal-overlay" @click.self="cancelConfirm">
      <div class="modal">
        <p class="modal-text">{{ confirmMsg }}</p>
        <div class="modal-actions">
          <button @click="cancelConfirm" class="btn btn-ghost">取消</button>
          <button @click="doConfirm" class="btn btn-primary">确定</button>
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
  background: var(--color-bg-secondary);
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
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
</style>
