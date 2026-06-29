package protocol

import "fmt"

// 服务端默认信令 / 中转 UDP 端口。
const DefaultServerPort = 10555

// VIPrefix 虚拟 IP 段前缀，完整网段为 10.66.0.0/16。
// 房间内按加入顺序分配 10.66.0.2、10.66.0.3 …（.0 为网络号，.1 预留作网关）。
const VIPrefix = "10.66.0"

// HeartbeatInterval 客户端心跳发送间隔（秒）。
const HeartbeatInterval = 5

// PeerTimeout 服务端判定 peer 离线的超时阈值（秒）。
const PeerTimeout = 15

// DefaultMTU 虚拟网卡 MTU，留出 UDP/外层头部余量，避免物理网卡分片丢包。
const DefaultMTU = 1400

// ReadBufferSize UDP 读缓冲区大小（字节）。
const ReadBufferSize = 65535

// PunchTimeout UDP 打洞超时时间（秒），超时后回落 Relay。
const PunchTimeout = 3

// VIPStart 虚拟 IP 起始主机号（从 2 开始，避开网络号与网关）。
const VIPStart = 2

// VIPToIP 将虚拟 IP 主机号转为点分十进制字符串，如 2 → "10.66.0.2"。
func VIPToIP(vip uint32) string {
	return fmt.Sprintf("%s.%d", VIPrefix, vip)
}
