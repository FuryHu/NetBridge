package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"

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

	a.log.Info("NetBridge 客户端已启动")
}

// shutdown 在 Wails 应用退出时调用，清理资源。
func (a *App) shutdown(ctx context.Context) {
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
	IsIPv6     bool   `json:"isIPv6"`  // P2P 通道是否走 IPv6（self 则看 PublicAddress 是否 v6）
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
			IsIPv6:     isV6,
		})
	}
	// 按 VIP 排序，保证顺序稳定
	sort.Slice(views, func(i, j int) bool {
		return views[i].VIP < views[j].VIP
	})
	return views
}
