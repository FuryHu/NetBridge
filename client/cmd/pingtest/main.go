// Command pingtest 是阶段 1 的命令行验证工具：向 server 发 Ping 并打印 RTT。
// 用法：go run ./cmd/pingtest 1.2.3.4 10555
// 跑通后即可删除，正式联调走 Wails 客户端的 PingServer 方法。
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/FuryHu/netbridge/client/netconn"
	"github.com/FuryHu/netbridge/protocol"
)

func main() {
	host := "127.0.0.1"
	port := 10555
	if len(os.Args) >= 2 {
		host = os.Args[1]
	}
	if len(os.Args) >= 3 {
		var p int
		_, _ = fmt.Sscanf(os.Args[2], "%d", &p)
		if p > 0 {
			port = p
		}
	}

	ctx := context.Background()
	_ = ctx

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		fmt.Println("解析地址失败:", err)
		os.Exit(1)
	}

	conn, err := netconn.NewUDPConn(nil)
	if err != nil {
		fmt.Println("建立 UDP 连接失败:", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Printf("本地端口: %s → 目标 %s:%d\n", conn.LocalAddr(), host, port)

	// 连发 5 个 Ping 统计 RTT
	count := 5
	var total int64
	ok := 0
	for i := 1; i <= count; i++ {
		ts := time.Now().UnixMilli()
		if err := conn.SendPacket(addr, protocol.NewPing(uuid.NewString(), ts)); err != nil {
			fmt.Printf("[%d] 发送失败: %v\n", i, err)
			continue
		}

		// 等待 Pong，超时 2 秒
		data, _, err := conn.ReadPacket(2 * time.Second)
		if err != nil {
			fmt.Printf("[%d] 超时\n", i)
			continue
		}

		pt, err := protocol.PeekType(data)
		if err != nil || pt != protocol.TypePong {
			fmt.Printf("[%d] 收到非 Pong 报文: %s\n", i, pt)
			continue
		}
		var pong protocol.PongPacket
		if err := protocol.Decode(data, &pong); err != nil {
			fmt.Printf("[%d] 解码失败: %v\n", i, err)
			continue
		}
		rtt := time.Now().UnixMilli() - pong.Timestamp
		total += rtt
		ok++
		fmt.Printf("[%d] pong  RTT=%dms\n", i, rtt)

		time.Sleep(500 * time.Millisecond)
	}

	if ok > 0 {
		fmt.Printf("\n成功 %d/%d, 平均 RTT=%dms\n", ok, count, total/int64(ok))
	} else {
		fmt.Println("\n全部超时，请检查 server 是否启动 / 端口 / 防火墙")
		os.Exit(1)
	}
}
