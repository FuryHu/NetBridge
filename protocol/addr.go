package protocol

import (
	"net"
	"strconv"
)

// NormalizeUDPAddr 把一个 *net.UDPAddr 规整成"语义清晰"的两个表示：
//   - v4Addr：若该地址等价于 IPv4（包括 4-in-6 的 ::ffff:x.x.x.x），返回 "1.2.3.4:port"，否则空串
//   - v6Addr：若该地址是真正的 IPv6 单播（非 4-in-6 且非链路本地的回环 mapping），返回 "[2001:..]:port"
//
// 之所以要这个工具：Go 在双栈 socket 上接收的 IPv4 客户端，remote.IP 是 ::ffff:1.2.3.4，
// 直接 String() 会写成 "[::ffff:1.2.3.4]:port"——客户端用 net.ResolveUDPAddr 解析回来后，
// 写出去的 UDP 包会被内核当 IPv6 发，而对端 NAT 看到的是 IPv4。地址语义错位会让打洞失败。
//
// 调用方应优先使用此函数的结果（任一非空都可作为 PublicAddress），并避免直接拼接 remote.String()。
func NormalizeUDPAddr(addr *net.UDPAddr) (v4Addr, v6Addr string) {
	if addr == nil {
		return "", ""
	}
	// To4() 对 IPv4 与 4-in-6 都返回 4 字节切片；对真正的 v6 返回 nil。
	if v4 := addr.IP.To4(); v4 != nil {
		v4Addr = net.JoinHostPort(v4.String(), strconv.Itoa(addr.Port))
		return
	}
	// 真正的 IPv6：包括 GUA、ULA、链路本地等。
	// 我们不试图过滤——服务端无法判断打洞可达性，交给客户端尝试。
	v6Addr = net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port))
	return
}

// PreferredAddr 在 v4 / v6 之间挑一个填到 PublicAddress 主字段。
// 策略：优先 IPv6（如果有），因为没有 NAT 直连可能性最大；否则用 IPv4。
//
// 客户端打洞时是并行尝试两个地址的，主字段仅是默认展示。
func PreferredAddr(v4Addr, v6Addr string) string {
	if v6Addr != "" {
		return v6Addr
	}
	return v4Addr
}

// IsIPv6Addr 判断一个 "host:port" 形式的地址是否为 IPv6。
// 用于 UI 展示是否打"v6"徽标。
func IsIPv6Addr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil
}
