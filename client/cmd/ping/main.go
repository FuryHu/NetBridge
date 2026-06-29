// cmd/ping 是一个独立命令行工具，用于验证 client -> server 的 UDP Ping/Pong 通信。
// 阶段 1 联调专用，不依赖 Wails GUI。直接 `go run ./cmd/ping` 即可。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/FuryHu/netbridge/protocol"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:10555", "服务器地址")
	count := flag.Int("count", 5, "发送 Ping 次数")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// 用同一个 socket 与服务器通信（后续 P2P 打洞也需要复用此 socket）。
	// 绑定 :0 让系统随机分配本地端口。
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		log.Error("创建 UDP socket 失败", "err", err)
		os.Exit(1)
	}
	defer conn.Close()
	log.Info("客户端 socket 就绪", "local", conn.LocalAddr())

	// 服务器地址
	serverAddr, err := net.ResolveUDPAddr("udp", *addr)
	if err != nil {
		log.Error("解析服务器地址失败", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// 启动接收 goroutine，接收 Pong（及后续其他报文）。
	go receiveLoop(ctx, conn, log)

	// 发送 count 次 Ping
	for i := 0; i < *count; i++ {
		ts := time.Now().UnixMilli()
		ping := protocol.NewPing("test-peer", ts)
		data, err := json.Marshal(ping)
		if err != nil {
			log.Error("编码 Ping 失败", "err", err)
			continue
		}
		_, err = conn.WriteToUDP(data, serverAddr)
		if err != nil {
			log.Error("发送 Ping 失败", "err", err)
			continue
		}
		log.Info("已发送 Ping", "seq", i+1, "ts", ts)

		// 间隔 2 秒
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
	}

	log.Info("Ping 完成，等待 3 秒接收残余 Pong...")
	select {
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
	}
}

// receiveLoop 循环接收 UDP 报文并打印。
func receiveLoop(ctx context.Context, conn *net.UDPConn, log *slog.Logger) {
	buf := make([]byte, protocol.ReadBufferSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(time.Second))
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			log.Error("ReadFromUDP 失败", "err", err)
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])

		ptype, err := protocol.PeekType(data)
		if err != nil {
			log.Debug("无法解析报文", "raw", data)
			continue
		}

		switch ptype {
		case protocol.TypePong:
			var pong protocol.PongPacket
			_ = protocol.Decode(data, &pong)
			rtt := time.Now().UnixMilli() - pong.Timestamp
			log.Info("← Pong", "from", remote, "rtt_ms", rtt, "ts", pong.Timestamp)
		default:
			log.Info("← 报文", "type", ptype, "from", remote, "len", n)
		}
	}
}
