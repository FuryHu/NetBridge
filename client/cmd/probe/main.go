//go:build darwin

// Command probe 是 macOS 移植的 Phase 0 探针：在真机上验证四件靠读代码无法确定的事。
//
//	1. 创建 utun 是否真的需要 root
//	2. ifconfig + route 的这套配置能否把网卡配起来（并打印实际的路由表）
//	3. **出站广播/多播能否从 utun 发出去**（本机开的房间，别人能不能看见）
//	4. **另一端写进 utun 的广播能否送达本机 socket**（别人开的房间，本机能不能看见）
//
// 3 与 4 是决定性问题，缺一个 LAN 大厅发现就不成立：Civ 6 这类游戏靠子网广播
// 找房间，只出得来、进不去（或反过来）都等于找不到房间。若两个方向都不通，
// 客户端在 Mac 上就没有产品价值，后面的工作不该继续。
//
// 刻意只依赖 tun.NetAdapter 接口（而不是 TunAdapter 的具体类型），
// 这样探针验证的就是真正要上线的那份实现，而不是另写一遍的验证代码。
//
// 用法：
//
//	cd client
//	go run ./cmd/probe          # 不带头权限，只会跑第 1 步（记录不用 root 的报错）
//	sudo go run ./cmd/probe     # 完整探针
//
// 验证完即可删除本目录。退出时 utun 随 fd 关闭而销毁，路由由内核连带清理。
package main

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/FuryHu/netbridge/client/tun"
	"golang.org/x/sys/unix"
)

// testVIP 探针用的虚拟 IP，落在项目的 10.66.0.0/16 网段里。
const testVIP = "10.66.0.5"

// probePort 测试包的目标端口，随便挑一个高位端口以免撞上真实服务。
const probePort = 47031

// broadcastVIP 虚拟网段的子网广播地址（10.66.0.0/16）。
const broadcastVIP = "10.66.255.255"

// peerProbeVIP 入站测试里扮演"对端"的地址。必须是本机没有的地址，
// 否则内核认出源是本机自己，测试就失去意义了。
const peerProbeVIP = "10.66.0.99"

func main() {
	fmt.Println("=== NetBridge macOS 探针 (Phase 0) ===")
	fmt.Printf("测试 VIP: %s   网段: 10.66.0.0/16\n\n", testVIP)

	// --- 1. 创建 utun -------------------------------------------------------
	header("1. 创建 utun")
	if os.Geteuid() != 0 {
		info("当前不是 root（euid=%d）——先记录一下非特权创建失败的报错，再用 sudo 重跑。", os.Geteuid())
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	adapter, err := tun.Create("NetBridge", testVIP, logger)
	if err != nil {
		fail("创建失败: %v", err)
		if os.Geteuid() != 0 {
			info("→ 符合预期（需要 root）。请用 sudo go run ./cmd/probe 重跑。")
		}
		os.Exit(1)
	}
	defer adapter.Close()
	ok("utun 创建成功，且 SetVIP 未报错")

	// --- 2. 找出接口名并打印真实状态 ---------------------------------------
	header("2. 接口与路由的实际情况")
	ifName := findInterfaceByAddr(testVIP)
	if ifName == "" {
		fail("没找到持有 %s 的接口——SetVIP 可能没真正生效", testVIP)
	} else {
		ok("接口名: %s", ifName)
		dumpCmd("ifconfig", ifName)
	}
	dumpRoute()

	// --- 3. 出站路径 -------------------------------------------------------
	header("3. 出站路径（各目标逐个测试，失败的会自动尝试补救路由后重测）")
	if ifName == "" {
		fail("接口名未知，跳过出站测试")
		summary(false, false)
		return
	}
	broadcastOK := testOutbound(adapter, ifName)

	// --- 4. 入站路径 -------------------------------------------------------
	header("4. 入站路径（对端发来的广播写进 utun，本机能否收到）")
	inboundOK := testInbound(adapter)

	header("结论")
	summary(broadcastOK, inboundOK)
}

// testOutbound 依次测试四个目的地址能否从 utun 发出。
// 判定方式：起一个读网卡的 goroutine，发包后看该包是否真的出现在 utun 上。
// 返回「子网广播」是否成功——那是决定性的那一项。
func testOutbound(adapter tun.NetAdapter, ifName string) bool {
	conn, err := listenOnVIP()
	if err != nil {
		fail("绑定 %s 的 UDP socket 失败: %v", testVIP, err)
		return false
	}
	defer conn.Close()

	seen := make(chan net.IP, 32)
	go func() {
		for {
			pkt, err := adapter.ReadPacket()
			if err != nil {
				return // 网卡已关，正常退出
			}
			if len(pkt) >= 20 && pkt[0]>>4 == 4 {
				seen <- net.IPv4(pkt[16], pkt[17], pkt[18], pkt[19])
			}
		}
	}()

	type target struct {
		label string
		addr  string
		// 没发出去时的补救路由（参数不含 route 命令本身）；nil 表示无补救手段
		fix []string
	}
	targets := []target{
		{
			label: "子网广播 10.66.255.255",
			addr:  "10.66.255.255",
			fix:   []string{"-net", "10.66.255.255", "-netmask", "255.255.255.255", "-interface", ifName},
		},
		{
			label: "受限广播 255.255.255.255",
			addr:  "255.255.255.255",
			fix:   []string{"-host", "255.255.255.255", "-interface", ifName},
		},
		{
			label: "多播 224.0.0.1",
			addr:  "224.0.0.1",
			fix:   []string{"-net", "224.0.0.0/4", "-interface", ifName},
		},
		{
			label: "单播 10.66.0.9（不存在的对端）",
			addr:  "10.66.0.9",
			fix:   nil, // 网段路由已覆盖，不需要补救
		},
	}

	subnetBroadcastOK := false
	for _, t := range targets {
		// 丢掉上一轮可能残留的包
		drainSeen(seen)

		label := t.label
		if sent := sendAndWatch(conn, t.addr, seen); sent {
			ok("%s → 已从 utun 发出", label)
			if t.addr == "10.66.255.255" {
				subnetBroadcastOK = true
			}
			continue
		}

		// 第一次没出去，试补救路由
		if t.fix == nil {
			fail("%s → 2 秒内未在 utun 上读到（且无补救手段）", label)
			continue
		}
		info("%s → 未从 utun 发出，尝试加路由: route add %s", label, strings.Join(t.fix, " "))
		out, err := exec.Command("route", append([]string{"-n", "add"}, t.fix...)...).CombinedOutput()
		if err != nil {
			fail("  加路由也失败: %v  输出: %s", err, strings.TrimSpace(string(out)))
			continue
		}
		drainSeen(seen)
		if sendAndWatch(conn, t.addr, seen) {
			warn("  加路由后成功 → **上线前必须把这步补进 SetVIP**")
			if t.addr == "10.66.255.255" {
				subnetBroadcastOK = true
			}
		} else {
			fail("  加路由后仍未发出——该目标在 macOS 上可能无法通过 utun 广播")
		}
	}
	return subnetBroadcastOK
}

// testInbound 验证入站的一半：对端写进 utun 的广播包，本机 socket 能否收到。
//
// 出站能发只说明"本机开的房间别人看得见"，还要能收到别人发来的广播才能
// "进得去别人的房间"——两个方向都成立，LAN 大厅发现才算完整。
//
// 做法是自己造一个 IPv4+UDP 广播包写进 utun，看本机 socket 能否收到。
// 之所以要自己造包而不是让内核生成：出站测试已经证明"内核生成的包出得去"，
// 这里要验的是**入站方向**，所以必须绕开发送路径，直接往网卡里灌。
//
// 源地址故意用一个本机没有的 VIP（10.66.0.99），排掉"本机自己发的包被内核
// 回环给自己"这种假阳性——仿的是真从对端来的包。
// 两种接收端都测：绑 VIP 的（最严格）与绑 0.0.0.0 的（多数游戏的真实绑法）。
func testInbound(adapter tun.NetAdapter) bool {
	receivers := []struct {
		label string
		ip    string
	}{
		{"绑定 VIP " + testVIP + " 的 socket", testVIP},
		{"绑定 0.0.0.0 的 socket", "0.0.0.0"},
	}

	allOK := true
	for _, r := range receivers {
		conn, err := listenAt(r.ip)
		if err != nil {
			fail("%s 创建失败: %v", r.label, err)
			allOK = false
			continue
		}
		localPort := conn.LocalAddr().(*net.UDPAddr).Port
		// 目的端口必须是这个 socket 实际拿到的端口，绑 0 时是内核分配的
		pkt := buildUDPPacket(net.ParseIP(peerProbeVIP), net.ParseIP(broadcastVIP),
			probePort, uint16(localPort), []byte("netbridge-inbound"))

		if err := adapter.WritePacket(pkt); err != nil {
			fail("%s：往 utun 写包失败: %v", r.label, err)
			conn.Close()
			allOK = false
			continue
		}

		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 2048)
		n, _, err := conn.ReadFromUDP(buf)
		conn.Close()
		switch {
		case err != nil:
			fail("%s：2 秒内没收到写进 utun 的广播包（%v）", r.label, err)
			allOK = false
		case string(buf[:n]) != "netbridge-inbound":
			warn("%s：收到包但内容不符: %q", r.label, string(buf[:n]))
			allOK = false
		default:
			ok("%s：收到（对端发来的广播能送达本机游戏）", r.label)
		}
	}
	return allOK
}

// buildUDPPacket 手工拼一个 IPv4 + UDP 包（含两个校验和）。
// 只填 IPv4 头的前 20 字节，没有选项。
func buildUDPPacket(src, dst net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	const ipHdrLen, udpHdrLen = 20, 8

	total := ipHdrLen + udpHdrLen + len(payload)
	pkt := make([]byte, total)

	// --- IPv4 头 ---
	pkt[0] = 0x45 // version 4, IHL 5（20 字节）
	pkt[1] = 0    // DSCP/ECN
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	binary.BigEndian.PutUint16(pkt[4:6], 1) // ID（无所谓，不拆片）
	binary.BigEndian.PutUint16(pkt[6:8], 0) // flags/fragment offset：不拆片
	pkt[8] = 64                             // TTL
	pkt[9] = 17                             // protocol = UDP
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:ipHdrLen]))

	// --- UDP 头 ---
	binary.BigEndian.PutUint16(pkt[20:22], srcPort)
	binary.BigEndian.PutUint16(pkt[22:24], dstPort)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(udpHdrLen+len(payload)))
	copy(pkt[28:], payload)

	// UDP 校验和要带伪首部（源/目的 IP、协议号、UDP 长度）
	pseudo := make([]byte, 0, 12+udpHdrLen+len(payload))
	pseudo = append(pseudo, src.To4()...)
	pseudo = append(pseudo, dst.To4()...)
	pseudo = append(pseudo, 0, 17)
	pseudo = append(pseudo, byte((udpHdrLen+len(payload))>>8), byte(udpHdrLen+len(payload)))
	pseudo = append(pseudo, pkt[20:total]...)
	sum := ipChecksum(pseudo)
	if sum == 0 {
		sum = 0xffff // UDP 里校验和 0 表示"未计算"，算出 0 要写成全 1
	}
	binary.BigEndian.PutUint16(pkt[26:28], sum)

	return pkt
}

// ipChecksum 就是标准的 16 位反码求和（IP 头与 UDP 校验和共用同一算法）。
func ipChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 { // 奇数长度补一个 0 字节
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// sendAndWatch 发一个包，并在 2 秒内等待它出现在 utun 上。
func sendAndWatch(conn *net.UDPConn, dst string, seen <-chan net.IP) bool {
	raddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", dst, probePort))
	if err != nil {
		fail("  解析 %s 失败: %v", dst, err)
		return false
	}
	if _, err := conn.WriteToUDP([]byte("netbridge-probe"), raddr); err != nil {
		// 这一层就失败说明 socket/路由就没让人发出去，值得单独报出来
		fail("  sendto 直接失败: %v", err)
		return false
	}
	select {
	case got := <-seen:
		_ = got
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

// listenOnVIP 绑定到测试 VIP，并打开 SO_BROADCAST
// （BSD 上往任何广播地址发包都要求这个选项，否则 sendto 直接 EACCES）。
func listenOnVIP() (*net.UDPConn, error) {
	return listenAt(testVIP)
}

// listenAt 绑定到指定 IPv4 地址（"0.0.0.0" 表示不限），端口由内核分配，
// 并打开 SO_BROADCAST。入站测试也用它，保证两边 socket 选项一致。
func listenAt(ip string) (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip), Port: 0})
	if err != nil {
		return nil, err
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		conn.Close()
		return nil, err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1)
	}); err != nil {
		conn.Close()
		return nil, err
	}
	if sockErr != nil {
		conn.Close()
		return nil, fmt.Errorf("设置 SO_BROADCAST 失败: %w", sockErr)
	}
	return conn, nil
}

// findInterfaceByAddr 找出持有该 IPv4 地址的接口名。
// 通过 net.Interfaces 而不是解析 ifconfig 输出，避免依赖文本格式。
func findInterfaceByAddr(addr string) string {
	target := net.ParseIP(addr)
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ipnet.IP.Equal(target) {
				return iface.Name
			}
		}
	}
	return ""
}

func drainSeen(seen <-chan net.IP) {
	for {
		select {
		case <-seen:
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func dumpCmd(name string, args ...string) {
	info("$ %s %s", name, strings.Join(args, " "))
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		fail("  执行失败: %v", err)
		return
	}
	fmt.Println(indent(strings.TrimRight(string(out), "\n")))
}

func dumpRoute() {
	info("$ netstat -rn -f inet（只看 10.66 与 224 相关）")
	out, err := exec.Command("netstat", "-rn", "-f", "inet").CombinedOutput()
	if err != nil {
		fail("  执行失败: %v", err)
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "10.66") || strings.Contains(line, "224") ||
			strings.HasPrefix(strings.TrimSpace(line), "Destination") {
			fmt.Println(indent(line))
		}
	}
}

func summary(outboundOK, inboundOK bool) {
	if outboundOK && inboundOK {
		ok("子网广播双向都通 → macOS 客户端具备产品价值，可以进入 Phase 1。")
		return
	}
	if outboundOK != inboundOK {
		if inboundOK {
			fail("出站不通、入站通：本机能收到别人的广播，但自己发的出不去 → 别人看不见本机开的房间。")
		} else {
			fail("出站通、入站不通：本机发得出广播，但收不到别人的 → 进不了别人开的房间。")
		}
		info("单向不通已足以让 LAN 大厅发现不成立，先查清卡在哪一层再决定是否继续投入。")
		return
	}
	fail("子网广播两个方向都不通 → Civ 6 这类靠广播找人房间的游戏在 Mac 上不成立。")
	info("在继续投入前，先确认：目标游戏是否只靠子网广播发现房间？")
	info("若走的是「直接连接 IP」（本项目 README 里提到的 VIP 直连）则不受影响。")
}

// ---- 输出格式 ----

func header(s string)         { fmt.Printf("\n=== %s ===\n", s) }
func ok(s string, a ...any)   { fmt.Printf("  ✓ "+s+"\n", a...) }
func warn(s string, a ...any) { fmt.Printf("  ! "+s+"\n", a...) }
func info(s string, a ...any) { fmt.Printf("  · "+s+"\n", a...) }
func fail(s string, a ...any) { fmt.Printf("  ✗ "+s+"\n", a...) }

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
