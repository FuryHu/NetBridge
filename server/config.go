package main

import (
	"flag"
	"os"
	"strconv"

	"github.com/FuryHu/netbridge/protocol"
)

// Config 服务端运行配置。
// 所有项均可通过命令行 flag 覆盖，环境变量作为默认值来源。
type Config struct {
	Addr        string // 监听地址，如 :10555 或 0.0.0.0:10555
	Port        int    // 监听端口
	RoomTimeout int    // peer 超时秒数（阶段 2 心跳剔除用）
}

// LoadConfig 从命令行与环境变量解析配置。
func LoadConfig() Config {
	var c Config
	flag.StringVar(&c.Addr, "addr", "", "监听地址，如 0.0.0.0:10555（默认 :<port>）")
	flag.IntVar(&c.Port, "port", envInt("NETBRIDGE_PORT", protocol.DefaultServerPort), "UDP 监听端口")
	flag.IntVar(&c.RoomTimeout, "room-timeout", envInt("NETBRIDGE_ROOM_TIMEOUT", protocol.PeerTimeout), "peer 超时秒数")
	flag.Parse()

	if c.Addr == "" {
		// 默认监听 [::]:<port>——Go 的双栈语义会同时接受 IPv4 与 IPv6 入连。
		// 用户若想强制只听 v4，可以显式传 -addr 0.0.0.0:10555。
		c.Addr = "[::]:" + strconv.Itoa(c.Port)
	}
	return c
}

// envInt 读取环境变量整数，失败或不存在返回默认值。
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
