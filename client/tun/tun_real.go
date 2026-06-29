//go:build windows && cgo

package tun

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

	"golang.zx2c4.com/wintun"
)

type WinTunAdapter struct {
	name      string
	adapter   *wintun.Adapter
	session   wintun.Session
	log       *slog.Logger
	closeOnce sync.Once
}

// CreateWinTun 创建一张 NetBridge 虚拟网卡并启动会话。
// 不再尝试通过 `netsh delete interface` 清理同名残留——
//   1. wintun.CreateAdapter 自身会处理同名情况；
//   2. netsh 的 delete 子命令对 wintun.dll 创建的卡基本无效（它是给 WMI/旧 TAP 用的）；
//   3. 多调用一次外部命令会多闪一次 cmd 窗口（虽然现在已用 CREATE_NO_WINDOW 抑制，
//      但能少一次系统调用就少一次）。
func CreateWinTun(name, vip string, log *slog.Logger) (*WinTunAdapter, error) {
	if log == nil {
		log = slog.Default()
	}

	adapter, err := wintun.CreateAdapter(name, "NetBridge", nil)
	if err != nil {
		return nil, fmt.Errorf("创建 WinTun 网卡失败: %w", err)
	}

	session, err := adapter.StartSession(0x400000)
	if err != nil {
		adapter.Close()
		return nil, fmt.Errorf("启动 WinTun 会话失败: %w", err)
	}

	a := &WinTunAdapter{name: name, adapter: adapter, session: session, log: log}

	if err := a.SetVIP(vip); err != nil {
		a.Close()
		return nil, fmt.Errorf("设置虚拟 IP 失败: %w", err)
	}

	log.Info("WinTun 网卡就绪", "name", name, "vip", vip)
	return a, nil
}

func (a *WinTunAdapter) ReadPacket() ([]byte, error) {
	pkt, err := a.session.ReceivePacket()
	if err != nil {
		return nil, fmt.Errorf("WinTun 读包失败: %w", err)
	}
	data := make([]byte, len(pkt))
	copy(data, pkt)
	a.session.ReleaseReceivePacket(pkt)
	return data, nil
}

func (a *WinTunAdapter) WritePacket(data []byte) error {
	pkt, err := a.session.AllocateSendPacket(len(data))
	if err != nil {
		return fmt.Errorf("WinTun 发送缓冲区不足: %w", err)
	}
	copy(pkt, data)
	a.session.SendPacket(pkt)
	return nil
}

func (a *WinTunAdapter) Close() error {
	var err error
	a.closeOnce.Do(func() {
		a.log.Info("关闭 WinTun 网卡", "name", a.name)
		// 撤销 SetVIP 里加的防火墙规则，避免规则越积越多（即使没积累，也保持干净）。
		runHidden("netsh", "advfirewall", "firewall", "delete", "rule",
			"name=NetBridge Trust 10.66/16").Run()
		a.session.End()
		if a.adapter != nil {
			err = a.adapter.Close()
		}
	})
	return err
}

// SetVIP 配置/重新配置网卡的虚拟 IP、metric、MTU、网络类别与防火墙规则。
//
// 设计为幂等：同一个 adapter 实例可在不同房间间切换 VIP 时反复调用。
// 进入新房间时只需调一次 SetVIP，无需销毁重建 adapter——避免每次切房间都
// 经历 ~2s 的 wintun.CreateAdapter 驱动初始化。
func (a *WinTunAdapter) SetVIP(vip string) error {
	if net.ParseIP(vip) == nil {
		return fmt.Errorf("无效 IP: %s", vip)
	}
	// /16 子网（10.66.0.0/16）让所有 10.66.x.x 都走虚拟网卡。
	cmd := runHidden("netsh", "interface", "ip", "set", "address",
		fmt.Sprintf("name=%s", a.name), "source=static",
		fmt.Sprintf("addr=%s", vip), "mask=255.255.0.0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("netsh 设置 IP 失败: %w\n输出: %s", err, string(out))
	}

	// 接口 metric=1：保证 Civ 6 等程序在多网卡机器上优先经虚拟网卡寻找 LAN 房间。
	runHidden("netsh", "interface", "ipv4", "set", "interface", a.name, "metric=1").Run()

	// MTU=1400：UDP/IP 头 28B + 紧凑帧头 12B + 一点余量，避开物理网卡 1500 分片。
	// store=active 而非 persistent，避免 reboot 后残留。
	runHidden("netsh", "interface", "ipv4", "set", "subinterface",
		a.name, "mtu=1400", "store=active").Run()

	// Windows 默认把私有 IP 段当公网网络处理时会限制广播——把这块卡显式标为"专用网络"。
	// PowerShell Set-NetConnectionProfile 比 netsh 改 firewall 更准；失败忽略（旧系统可能没有）。
	runHidden("powershell", "-Command",
		fmt.Sprintf(`Set-NetConnectionProfile -InterfaceAlias '%s' -NetworkCategory Private -ErrorAction SilentlyContinue`, a.name),
	).Run()

	// 显式把 10.66.0.0/16 加入入站允许规则（所有协议、所有端口、所有 profile）。
	//
	// 之所以必要：Windows 防火墙的"NetworkCategory=Private"只是放宽规则，但很多 ICMP /
	// 文件共享 / LAN 发现的默认入站规则仍然按 profile 单独控制。再叠加 360/火绒/企业 EDR
	// 时尤其混乱——往往表现为"Relay 通道偶尔能 ping 通，P2P 直连完全 ping 不通"。
	// 直接按"源 IP 段"开白名单，能绕过所有这些不可见的 per-profile / per-program 规则。
	//
	// remoteip 是源 IP 过滤——只有来自虚拟网段的入站会被这条规则放行，对公网流量无影响。
	runHidden("netsh", "advfirewall", "firewall", "delete", "rule",
		"name=NetBridge Trust 10.66/16").Run() // 删旧规则避免重复
	runHidden("netsh", "advfirewall", "firewall", "add", "rule",
		"name=NetBridge Trust 10.66/16",
		"dir=in", "action=allow", "protocol=any",
		"remoteip=10.66.0.0/16",
		"profile=any",
	).Run()

	return nil
}
