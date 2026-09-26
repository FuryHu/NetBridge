//go:build darwin

package core

import (
	"encoding/binary"
	"net"
	"sync"
	"time"
)

// Civ 6 的房间发现广播端口范围（62900-62999，抓包与联机教程均验证）。
// 加入方的游戏进程不监听这些端口（只有主机监听），代理绑定不会冲突；
// 本机当主机时绑定会失败，逐端口跳过即可（主机方向不需要代理）。
const (
	lanProxyPortFirst = 62900
	lanProxyPortLast  = 62999
	lanProxyMaxPacket = 1500
)

// LanProxy 把本机真实网卡上的 UDP 广播中继进虚拟网络，解决局域网游戏
// （Civ 6 等）在 macOS 上搜不到房间的问题。
//
// 背景：这类游戏在 macOS 上把发现 socket 绑定到真实网卡（如 en0），发往
// 255.255.255.255:62900-62999 的探针根本不会进 utun——改路由表也劫持不了
// （socket 绑定了接口）。bridge 只转发"进了虚拟网卡"的包，对它们爱莫能助。
//
// 原理（参考文明6联机教程的 UDP 代理方案，收进客户端内）：
//  1. 绑定 0.0.0.0:62900-62999。游戏发广播时内核会向本机同端口 socket
//     投递一份副本，代理由此"旁听"到探针。
//  2. 把探针重建为 src=VIP 的完整 IP 包，经 SendToPeer 广播给所有 peer——
//     对端看到来自 10.66.0.x 的广播，会像真实局域网一样响应。
//  3. 对端的响应单播回 VIP:6290X，同样落在代理 socket 上；代理再投递回
//     游戏的真实网卡地址:源端口，游戏就能看到房间。
//
// 防环：只中继 src 是本机真实网卡地址的广播；src 是虚拟网段（10.66/16）
// 的包视为对端响应，只回投本地、绝不再次广播。
type LanProxy struct {
	client *Client

	// localIPs 缓存本机真实网卡 IPv4（排除回环/链路本地/虚拟网段），10s 刷新。
	// 用于区分"本机游戏的广播"和"对端发来的响应"。
	localMu  sync.Mutex
	localIPs map[string]bool
	lastEnum time.Time

	// gameAddr 记录最近一次游戏探针的源地址（真实网卡 IP:端口），对端响应
	// 按它回投——游戏每轮扫描会换源端口，必须跟随更新。
	gameMu   sync.Mutex
	gameAddr *net.UDPAddr

	// 两个 Once 只在首次中继/回投时各记一条日志，确认整条链路已通；
	// 探针每轮扫描近百个，逐包打日志会刷爆日志面板。
	relayLogOnce sync.Once
	replyLogOnce sync.Once

	conns     []*net.UDPConn
	stopCh    chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// NewLanProxy 创建广播代理。仅 darwin 需要——Windows 的 wintun 能正常
// 收发广播，游戏发现协议原生可用。
func NewLanProxy(c *Client) *LanProxy {
	return &LanProxy{client: c, stopCh: make(chan struct{})}
}

// Start 绑定发现端口并启动读循环。端口逐个绑定、失败跳过，全部失败也能启动
// （只是没有中继能力），不向上抛错——代理是锦上添花，不能反过来挡住房间加入。
func (p *LanProxy) Start() {
	bound := 0
	for port := lanProxyPortFirst; port <= lanProxyPortLast; port++ {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err != nil {
			continue
		}
		p.conns = append(p.conns, conn)
		p.wg.Add(1)
		go p.readLoop(conn)
		bound++
	}
	p.client.clientLog("LAN", "广播代理已启动（端口 %d-%d 绑定成功 %d 个）",
		lanProxyPortFirst, lanProxyPortLast, bound)
}

// Stop 关闭全部 socket 并等待读循环退出。与 bridge 同生命周期调用。
func (p *LanProxy) Stop() {
	p.closeOnce.Do(func() {
		close(p.stopCh)
		for _, conn := range p.conns {
			conn.Close()
		}
		p.wg.Wait()
	})
}

// readLoop 读取单个代理 socket。buf 复用是安全的：中继与回投都在本轮
// 循环内同步完成（SendToPeer 同步编码写入，WriteToUDP 同步拷贝）。
func (p *LanProxy) readLoop(conn *net.UDPConn) {
	defer p.wg.Done()
	buf := make([]byte, lanProxyMaxPacket)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-p.stopCh:
				return
			default:
			}
			continue
		}
		if p.isLocalAddr(src.IP) {
			p.relayBroadcast(conn, src, buf[:n])
		} else {
			p.replyToGame(conn, buf[:n])
		}
	}
}

// isLocalAddr 判断 src 是否本机真实网卡地址（即本机游戏发出的广播）。
// 网卡列表缓存 10s：IP 集在一次游戏会话里几乎不变，逐包枚举是浪费。
func (p *LanProxy) isLocalAddr(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	p.localMu.Lock()
	defer p.localMu.Unlock()
	if time.Since(p.lastEnum) > 10*time.Second {
		p.localIPs = map[string]bool{}
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				ipn, ok := a.(*net.IPNet)
				if !ok {
					continue
				}
				if ip4 := ipn.IP.To4(); ip4 != nil && !isExcludedLocal(ip4) {
					p.localIPs[ip4.String()] = true
				}
			}
		}
		p.lastEnum = time.Now()
	}
	return p.localIPs[v4.String()]
}

// isExcludedLocal 排除回环、链路本地（awdl 等）与虚拟网段——
// 链路本地会引入 awdl 接口上的重复广播，虚拟网段则必须留给"对端响应"分支。
func isExcludedLocal(ip4 net.IP) bool {
	return ip4.IsLoopback() ||
		(ip4[0] == 169 && ip4[1] == 254) ||
		(ip4[0] == 10 && ip4[1] == 66)
}

// relayBroadcast 把本机游戏的探针中继给所有 peer。
func (p *LanProxy) relayBroadcast(conn *net.UDPConn, src *net.UDPAddr, payload []byte) {
	p.gameMu.Lock()
	p.gameAddr = src
	p.gameMu.Unlock()

	selfVIP := p.client.GetSelf().VirtualIP
	if selfVIP == 0 {
		return // 未入房，无从中继
	}
	dstPort := conn.LocalAddr().(*net.UDPAddr).Port
	pkt := buildBroadcastPacket(selfVIP, src.Port, dstPort, payload)

	for _, peer := range p.client.GetPeers() {
		if peer.VirtualIP == selfVIP {
			continue
		}
		_ = p.client.SendToPeer(peer.VirtualIP, pkt)
	}
	p.relayLogOnce.Do(func() {
		p.client.clientLog("LAN", "已中继首个游戏广播 %s -> 全部成员", src.String())
	})
}

// replyToGame 把对端主机的响应投递回本机游戏。
func (p *LanProxy) replyToGame(conn *net.UDPConn, payload []byte) {
	p.gameMu.Lock()
	gaddr := p.gameAddr
	p.gameMu.Unlock()
	if gaddr == nil {
		return // 还没见过本机游戏的探针，无从回投
	}
	// 从代理 socket 直接写回：源端口保持 6290X，游戏看到的响应与真实局域网一致。
	if _, err := conn.WriteToUDP(payload, gaddr); err != nil {
		return
	}
	p.replyLogOnce.Do(func() {
		p.client.clientLog("LAN", "已回投首个主机响应 -> %s", gaddr.String())
	})
}

// buildBroadcastPacket 用给定源构造一个 IPv4+UDP 广播包（dst=255.255.255.255）。
// 代理 socket 只能拿到 UDP 负载，必须重建完整 IP 包才能送进虚拟网——
// peer 侧的 bridge 原样写包进 wintun，对端内核要的是完整 IP 包。
// UDP 校验和置 0：IPv4 允许（RFC 768），包在 NetBridge 帧内传输、外层已有
// 保护，省一次计算。IP 头校验和必须算——对端协议栈收到会校验。
func buildBroadcastPacket(srcVIP uint32, srcPort, dstPort int, payload []byte) []byte {
	total := 20 + 8 + len(payload)
	pkt := make([]byte, total)
	pkt[0] = 0x45 // IPv4，IHL=5
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	pkt[8] = 64 // TTL
	pkt[9] = 17 // UDP
	// srcVIP 与 bridge 的编码约定一致：高两位固定 10.66，低两位是 VIP 主机号。
	pkt[12], pkt[13] = 10, 66
	pkt[14] = byte(srcVIP >> 8)
	pkt[15] = byte(srcVIP)
	pkt[16], pkt[17], pkt[18], pkt[19] = 255, 255, 255, 255
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:20]))
	binary.BigEndian.PutUint16(pkt[20:22], uint16(srcPort))
	binary.BigEndian.PutUint16(pkt[22:24], uint16(dstPort))
	binary.BigEndian.PutUint16(pkt[24:26], uint16(8+len(payload)))
	copy(pkt[28:], payload)
	return pkt
}

// ipChecksum 计算 IPv4 头校验和（hdr 的第 10-11 字节此时必须为 0）。
func ipChecksum(hdr []byte) uint16 {
	var sum uint32
	for i := 0; i < len(hdr); i += 2 {
		sum += uint32(hdr[i])<<8 | uint32(hdr[i+1])
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
