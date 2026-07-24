package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FuryHu/netbridge/client/core"
	"github.com/FuryHu/netbridge/client/tun"
	"github.com/FuryHu/netbridge/protocol"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是 Wails 应用结构，持有前后端共享的状态与方法。
//
// 网卡复用：adapter 一旦创建就缓存在 App 上，直到进程退出才 Close。
// 切换房间时仅调 adapter.SetVIP 重新配置 IP，避免每次都经历 ~2s 的
// wintun 驱动初始化。bridge 与 adapter 解耦——LeaveRoom 只停 bridge，
// 不动 adapter。
type App struct {
	ctx     context.Context
	client  *core.Client
	bridge  *tun.Bridge
	adapter tun.NetAdapter
	log     *slog.Logger

	// tunMu 串行化网卡相关操作（首次创建、SetVIP、Stop）——
	// onSelfUpdate 来自网络收包 goroutine，可能并发触发，必须加锁。
	tunMu sync.Mutex
	// lastVIP 记录上次配置到 adapter 的 VIP，避免每次 self:update 都重设。
	lastVIP string

	// quitting 区分"真正退出"与"关闭即最小化到托盘"。
	// OnBeforeClose 非退出时阻止关闭并隐藏窗口；tray 的"退出"菜单置 true 后才放行。
	quitting atomic.Bool
	tray     *tray
}

// NewApp 创建 App 实例。
func NewApp() *App {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return &App{
		log: log,
	}
}

// startup 在 Wails 应用启动时调用，初始化客户端核心。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.client = core.New(a.log)

	// 注册事件回调 — 后端状态变更时推送到前端。
	a.client.SetOnStateChange(func(s core.State) {
		wailsRuntime.EventsEmit(a.ctx, "status:change", s.String())
	})
	a.client.SetOnPeerUpdate(func(peers []protocol.PeerInfo) {
		wailsRuntime.EventsEmit(a.ctx, "peer:update", a.peersToViews(peers))
	})
	a.client.SetOnSelfUpdate(func(self protocol.PeerInfo) {
		// 服务端发来的"自己"信息——VIP / 公网端点全。前端用它替换占位的本地名字。
		wailsRuntime.EventsEmit(a.ctx, "self:update", a.selfToView(self))

		// "拿到 VIP" 是触发网卡自动开启/切换的唯一可靠信号源——
		// 无论是首次 JoinRoom 还是切换房间都会走这里。空 PeerInfo 是 LeaveRoom 推的占位，跳过。
		if self.VirtualIP != 0 {
			go a.autoEnsureTun(self.VirtualIP)
		}
	})
	a.client.SetChatHandler(func(nickName, msg string, ts int64) {
		wailsRuntime.EventsEmit(a.ctx, "chat:message", map[string]interface{}{
			"nickName":  nickName,
			"message":   msg,
			"timestamp": ts,
		})
	})
	a.client.SetLogHandler(func(msg string) {
		wailsRuntime.EventsEmit(a.ctx, "log:message", msg)
	})
	// 单个 peer 延迟变化（增量）：前端只更新对应成员卡的延迟徽标，避免重渲整列。
	a.client.SetOnLatencyUpdate(func(peerID string, ms int64) {
		wailsRuntime.EventsEmit(a.ctx, "latency:update", peerID, ms)
	})
	a.client.SetVoiceHandler(func(srcVIP uint32, payload []byte) {
		if a.ctx == nil {
			return
		}
		// srcVIP 是虚拟 IP 主机号（uint32），前端按它分发给对应 peer 的播放队列。
		// payload 为 voice 子格式字节，[]byte 经 Wails 序列化为 base64，前端自行解码。
		wailsRuntime.EventsEmit(a.ctx, "voice:data", map[string]interface{}{
			"srcVIP": srcVIP,
			"data":   payload,
		})
	})

	// 系统托盘：关闭/ESC 最小化到这里，右键菜单提供显示窗口 / 切换语音 / 退出。
	// 菜单项回调在托盘线程触发，转调下面的方法（都用 wailsRuntime，跨线程安全）。
	a.tray = newTray(a.log, a.showFromTray, a.toggleVoiceFromTray, a.quitFromTray)
	a.tray.start()

	a.log.Info("NetBridge 客户端已启动")
}

// shutdown 在 Wails 应用退出时调用，清理资源。
func (a *App) shutdown(ctx context.Context) {
	if a.tray != nil {
		// 先摘掉托盘图标，避免退出后任务栏残留空壳图标。
		a.tray.stop()
	}
	if a.bridge != nil {
		a.bridge.Stop()
	}
	if a.adapter != nil {
		a.adapter.Close()
		a.adapter = nil
	}
	if a.client != nil {
		a.client.Close()
	}
	a.log.Info("NetBridge 客户端已停止")
}

// ---- 托盘相关 ----

// onBeforeClose 拦截窗口关闭：非真正退出时改为隐藏到托盘，退出时放行。
// 注意：wailsRuntime.Quit 内部也会回调 OnBeforeClose，靠 quitting 标志放行。
func (a *App) onBeforeClose(ctx context.Context) (prevent bool) {
	if a.quitting.Load() {
		return false
	}
	// 异步隐藏，避免在 WM_CLOSE 同步处理链里直接操作窗口的重入风险。
	go wailsRuntime.WindowHide(ctx)
	return true
}

// showFromTray 托盘"显示主窗口" / 双击图标：从托盘恢复窗口。
// 恢复时推 window:shown 事件，供前端做"窗口重获焦点"相关逻辑（如剪贴板邀请自检）。
func (a *App) showFromTray() {
	if a.ctx != nil {
		wailsRuntime.WindowShow(a.ctx)
		wailsRuntime.EventsEmit(a.ctx, "window:shown")
	}
}

// toggleVoiceFromTray 托盘"切换语音"：发事件给前端，由前端调 toggleVoiceEnabled()。
// 语音状态权威仍在前端，后端不持有--与"暂不需后端管理语音状态"的要求一致。
func (a *App) toggleVoiceFromTray() {
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "tray:toggle-voice")
	}
}

// quitFromTray 托盘"退出"：置退出标志后真正退出应用。
func (a *App) quitFromTray() {
	a.quitting.Store(true)
	if a.ctx != nil {
		wailsRuntime.Quit(a.ctx)
	}
}

// SetTrayMenuState 由前端推送托盘右键菜单的显示状态。
// 文案由前端按当前语言翻译后传入（i18n 单一来源，后端不做翻译）；
// voiceOn 仅用于"语音"项是否打勾，是展示态，语音业务权威仍在前端。
func (a *App) SetTrayMenuState(showLabel, voiceLabel, quitLabel string, voiceOn bool) {
	if a.tray != nil {
		a.tray.setMenuState(showLabel, voiceLabel, quitLabel, voiceOn)
	}
}

// ---- 前端可调用的方法 ----

// Connect 连接服务器。
func (a *App) Connect(serverAddr string) error {
	if a.client == nil {
		return fmt.Errorf("客户端未初始化")
	}
	return a.client.Connect(serverAddr)
}

// JoinRoom 加入房间（需先 Connect）。
// 网卡的开启不在这里同步触发——服务端要先回 RoomStatus 分配 VIP，
// 拿到 VIP 后由 onSelfUpdate 回调里的 autoEnsureTun 异步开启。
func (a *App) JoinRoom(room, nickName string) error {
	if a.client == nil {
		return fmt.Errorf("客户端未初始化")
	}
	return a.client.JoinRoom(room, nickName)
}

// LeaveRoom 退出当前房间。
// 注意：bridge 停止但 adapter 保留，下次 JoinRoom 拿到新 VIP 后只需 SetVIP，无需重建。
func (a *App) LeaveRoom() {
	if a.client != nil {
		a.client.LeaveRoom()
	}
	a.tunMu.Lock()
	stopped := false
	if a.bridge != nil {
		a.bridge.Stop()
		a.bridge = nil
		stopped = true
	}
	a.tunMu.Unlock()
	if stopped {
		a.emitTunActive(false)
	}
}

// RestartAsAdmin 以管理员权限重启应用。
// manifest 已声明 requireAdministrator，生产环境理论上不会被调用——
// 保留作为开发期（wails dev 父进程非管理员）的兜底手段。
func (a *App) RestartAsAdmin() {
	RestartAsAdmin()
}

// OpenURL 用系统默认浏览器打开外部链接（如 GitHub 仓库）。
// 前端图标按钮调用；webview 内 <a href> 打开外部站点行为不可靠，统一走这里。
func (a *App) OpenURL(url string) {
	if a.ctx != nil {
		wailsRuntime.BrowserOpenURL(a.ctx, url)
	}
}

// Disconnect 断开服务器连接。
// adapter 不在这里关——保留到 shutdown，避免下次 Connect+JoinRoom 又要 2s 重建。
func (a *App) Disconnect() {
	if a.client != nil {
		a.client.Disconnect()
	}
	a.tunMu.Lock()
	stopped := false
	if a.bridge != nil {
		a.bridge.Stop()
		a.bridge = nil
		stopped = true
	}
	a.tunMu.Unlock()
	if stopped {
		a.emitTunActive(false)
	}
}

// SendChat 发送聊天消息到房间。
func (a *App) SendChat(msg string) error {
	if a.client == nil {
		return fmt.Errorf("客户端未初始化")
	}
	return a.client.SendChat(msg)
}

// SetVoiceStatus 上报本地语音状态（开语音 / 开麦），由后端转发给房间其他成员。
// 前端在 voiceEnabled / micOn 变化及进房后各调用一次。
func (a *App) SetVoiceStatus(voiceOn, micOn bool) error {
	if a.client == nil {
		return fmt.Errorf("客户端未初始化")
	}
	return a.client.SendPeerStatus(voiceOn, micOn)
}

// SendVoiceToAll 向房间内所有其他 peer 广播一帧语音。
// payload 为 voice 子格式字节（codec/seq/ts/audio，见 protocol/voice.go）。
// 前端每帧调用一次，由后端遍历 peer 分发，避免高频 IPC。
func (a *App) SendVoiceToAll(payload []byte) error {
	if a.client == nil {
		return fmt.Errorf("客户端未初始化")
	}
	return a.client.SendVoiceToAll(payload)
}

// autoEnsureTun 是 onSelfUpdate 回调里的网卡自动开启逻辑。
//
// 三种情况：
//  1. 首次拿到 VIP（adapter == nil）→ 创建 adapter + 启动 bridge
//  2. 同房间内 self:update 重复推送（lastVIP == vip32 && bridge != nil）→ 不做事
//  3. 切换房间（lastVIP != vip32）→ adapter.SetVIP 重新配置，bridge 不需要重启
//
// 失败通过 client.Log 通道推到前端日志面板。
func (a *App) autoEnsureTun(vip32 uint32) {
	vip := protocol.VIPToIP(vip32)
	if vip == "" {
		return
	}

	a.tunMu.Lock()
	defer a.tunMu.Unlock()

	// 情况 2：同 VIP 重复触发。
	if a.adapter != nil && a.lastVIP == vip && a.bridge != nil {
		return
	}

	// 情况 1：首次创建。
	if a.adapter == nil {
		adapter, err := a.createTunLocked(vip)
		if err != nil {
			a.emitLog(fmt.Sprintf("✗ 自动开启虚拟网卡失败: %s", err.Error()))
			return
		}
		a.adapter = adapter
		a.lastVIP = vip
		a.startBridgeLocked()
		a.emitLog(fmt.Sprintf("✓ 虚拟网卡已自动开启 (%s)", vip))
		a.emitTunActive(true)
		return
	}

	// 情况 3：切换 VIP（同 adapter 复用）。
	if a.lastVIP != vip {
		if err := a.adapter.SetVIP(vip); err != nil {
			a.emitLog(fmt.Sprintf("✗ 切换 VIP 失败: %s", err.Error()))
			return
		}
		a.lastVIP = vip
		a.emitLog(fmt.Sprintf("✓ 虚拟网卡 VIP 已切换 → %s", vip))
	}

	// bridge 可能因 LeaveRoom 已停，重新拉起。
	if a.bridge == nil {
		a.startBridgeLocked()
	}
	a.emitTunActive(true)
}

// createTunLocked 释放 wintun.dll 并创建 adapter。调用方必须持有 tunMu。
func (a *App) createTunLocked(vip string) (tun.NetAdapter, error) {
	if err := extractWintunDLL(); err != nil {
		return nil, fmt.Errorf("释放 wintun.dll 失败: %w", err)
	}
	return tun.CreateWinTun("NetBridge", vip, a.log)
}

// startBridgeLocked 启动一个新 bridge 绑定到 a.adapter。调用方必须持有 tunMu。
func (a *App) startBridgeLocked() {
	bridge := tun.NewBridge(a.adapter, a.client)
	bridge.SetLogger(a.log)
	bridge.Start(a.ctx)
	a.bridge = bridge
}

// emitLog 把信息推到前端日志面板（log:message 事件）。
// 不依赖 SetLogHandler，因为后者绑定的是 core.Client 内部日志通道，
// App 层自身事件应直接 emit。
func (a *App) emitLog(msg string) {
	a.log.Info(msg)
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "log:message", msg)
	}
}

// emitTunActive 通知前端虚拟网卡的活动状态变化——
// 前端顶栏的 ⚡TUN 徽标依赖这个事件，而不再由"开/关网卡"按钮自己维护。
func (a *App) emitTunActive(active bool) {
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "tun:active", active)
	}
}

// PingServer 向指定地址发送 Ping 并返回 RTT（毫秒）。
// 用于测试与服务器的连通性。
func (a *App) PingServer(addr string) (int64, error) {
	if a.client == nil {
		// 兜底：未初始化时创建临时连接。
		if err := a.Connect(addr); err != nil {
			return 0, err
		}
	}
	return a.client.PingServer()
}

// TestServer 对指定服务器做一次性 UDP Ping/Pong 探测，返回 RTT（毫秒）。
// 不建立持久连接、不影响 client 状态--仅供"加入前测试服务器连通性"。
// 编码走 protocol.Encode（JSON），与 cmd/ping 同款，服务端 ping_handler 可识别。
func (a *App) TestServer(addr string) (int64, error) {
	resolved, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return 0, fmt.Errorf("解析地址失败: %w", err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		return 0, fmt.Errorf("创建 socket 失败: %w", err)
	}
	defer conn.Close()

	ts := time.Now().UnixMilli()
	data, err := protocol.Encode(protocol.NewPing("test", ts))
	if err != nil {
		return 0, fmt.Errorf("编码 Ping 失败: %w", err)
	}
	if _, err := conn.WriteToUDP(data, resolved); err != nil {
		return 0, fmt.Errorf("发送 Ping 失败: %w", err)
	}

	// 收到 Pong 即算 RTT；非 Pong 报文忽略，5s 内无 Pong 视为超时。
	buf := make([]byte, protocol.ReadBufferSize)
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return 0, fmt.Errorf("等待 Pong 超时")
		}
		ptype, err := protocol.PeekType(buf[:n])
		if err != nil || ptype != protocol.TypePong {
			continue
		}
		var pong protocol.PongPacket
		if err := protocol.Decode(buf[:n], &pong); err != nil {
			continue
		}
		return time.Now().UnixMilli() - pong.Timestamp, nil
	}
}

// GetPeers 返回当前房间内的 peer 列表。
func (a *App) GetPeers() []PeerView {
	if a.client == nil {
		return nil
	}
	return a.peersToViews(a.client.GetPeers())
}

// GetSelf 返回自己的 peer 信息。
func (a *App) GetSelf() PeerView {
	if a.client == nil {
		return PeerView{}
	}
	return a.selfToView(a.client.GetSelf())
}

// GetStatus 返回客户端当前状态字符串。
func (a *App) GetStatus() string {
	if a.client == nil {
		return "disconnected"
	}
	return a.client.GetStatus()
}

// IsTunActive 返回虚拟网卡当前是否处于工作状态。
// 前端不再有"开/关网卡"按钮，但顶栏的 ⚡TUN 徽标仍需要这个状态。
func (a *App) IsTunActive() bool {
	a.tunMu.Lock()
	defer a.tunMu.Unlock()
	return a.bridge != nil && a.adapter != nil
}

// ---- 数据结构 ----

// PeerView 前端展示用的 peer 信息。
type PeerView struct {
	ID         string `json:"id"`
	NickName   string `json:"nickName"`
	VIP        string `json:"vip"`
	PublicAddr string `json:"publicAddr"`
	V4         string `json:"v4,omitempty"`
	V6         string `json:"v6,omitempty"`
	Channel    string `json:"channel"` // p2p / relay / none
	VoiceOn    bool   `json:"voiceOn,omitempty"` // 是否开启语音（对方上报）
	MicOn      bool   `json:"micOn,omitempty"`   // 是否开麦（对方上报）
	IsIPv6     bool   `json:"isIPv6"`  // P2P 通道是否走 IPv6（self 则看 PublicAddress 是否 v6）
	Latency    int64 `json:"latency"`  // 到该 peer 的展示延迟（ms）；-1 表示未知。P2P=实测 RTT，中转=双方 SRTT 之和
}

// selfToView 把自己的 PeerInfo 转成前端视图。
// 自身的 IsIPv6 判定：服务端给我们看到的端点（PublicAddress）的协议族即可——
// 同时上报"我能被对方走 v6 联系到吗"。
func (a *App) selfToView(self protocol.PeerInfo) PeerView {
	return PeerView{
		ID:         self.ID,
		NickName:   self.NickName,
		VIP:        protocol.VIPToIP(self.VirtualIP),
		PublicAddr: self.PublicAddress,
		V4:         self.PublicV4,
		V6:         self.PublicV6,
		// "我能否被对方通过 v6 找到" = 服务端能解析到我的 v6 端点。
		IsIPv6: self.PublicV6 != "",
	}
}

func (a *App) peersToViews(peers []protocol.PeerInfo) []PeerView {
	views := make([]PeerView, 0, len(peers))
	selfID := ""
	if a.client != nil {
		selfID = a.client.GetSelf().ID
	}
	for _, p := range peers {
		if p.ID == selfID {
			continue
		}
		ch, _, isV6 := "none", "", false
		if a.client != nil {
			ch, _, isV6 = a.client.GetPeerChannelInfo(p.ID)
		}
		views = append(views, PeerView{
			ID:         p.ID,
			NickName:   p.NickName,
			VIP:        protocol.VIPToIP(p.VirtualIP),
			PublicAddr: p.PublicAddress,
			V4:         p.PublicV4,
			V6:         p.PublicV6,
			Channel:    ch,
			VoiceOn:    p.VoiceOn,
			MicOn:      p.MicOn,
			IsIPv6:     isV6,
			Latency:    a.client.GetPeerLatency(p.ID),
		})
	}
	// 按 VIP 排序，保证顺序稳定
	sort.Slice(views, func(i, j int) bool {
		return views[i].VIP < views[j].VIP
	})
	return views
}
