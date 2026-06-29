// Package netconn 提供 NetBridge 客户端的 UDP 通信能力。
// 单个 UDP socket 同时用于：与 server 信令、与对端打洞、与对端 P2P 通信。
package netconn

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/FuryHu/netbridge/protocol"
)

// UDPConn 封装一个 UDP socket，提供编码收发能力。
type UDPConn struct {
	conn *net.UDPConn
	mu   sync.Mutex
	log  func(string, ...any) // 日志函数，外部注入
}

// NewUDPConn 绑定本地随机端口并创建连接。
// logFn 可为 nil，不输出日志。
//
// 监听 [::]:0 以同时支持 IPv4 与 IPv6——Go 在双栈系统上会自动开 IPV6_V6ONLY=0，
// 一个 socket 即可收发两族。若运行环境只有 IPv4（Windows IPv6 被禁用等），
// 自动回退到 0.0.0.0:0。
func NewUDPConn(logFn func(string, ...any)) (*UDPConn, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6unspecified, Port: 0})
	if err != nil {
		// 双栈失败时回退到纯 v4。极少数 Windows 配置或容器里会走到这里。
		var fbErr error
		conn, fbErr = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if fbErr != nil {
			return nil, fmt.Errorf("创建 UDP socket 失败: %w（v6 错误: %v）", fbErr, err)
		}
	}
	// 调大读写缓冲，避免 Civ 6 回合切换瞬间的大批量同步包打爆默认 8KB 内核缓冲。
	// 失败不致命（许多平台默认就够），仅 Debug 记录。
	const sockBufSize = 4 * 1024 * 1024
	if err := conn.SetReadBuffer(sockBufSize); err != nil && logFn != nil {
		logFn("SetReadBuffer 失败（可忽略）: %v", err)
	}
	if err := conn.SetWriteBuffer(sockBufSize); err != nil && logFn != nil {
		logFn("SetWriteBuffer 失败（可忽略）: %v", err)
	}
	if logFn == nil {
		logFn = func(string, ...any) {}
	}
	return &UDPConn{conn: conn, log: logFn}, nil
}

// Close 关闭连接。
func (u *UDPConn) Close() error {
	return u.conn.Close()
}

// LocalAddr 返回本地绑定的地址（含 NAT 映射前的端口）。
func (u *UDPConn) LocalAddr() *net.UDPAddr {
	return u.conn.LocalAddr().(*net.UDPAddr)
}

// RemoteAddr 返回连接的远端地址（如已设）。
func (u *UDPConn) RemoteAddr() *net.UDPAddr {
	return u.conn.RemoteAddr().(*net.UDPAddr)
}

// SendPacket 编码并发送报文到指定地址。
func (u *UDPConn) SendPacket(addr *net.UDPAddr, p any) error {
	data, err := protocol.Encode(p)
	if err != nil {
		return fmt.Errorf("编码失败: %w", err)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	_, err = u.conn.WriteToUDP(data, addr)
	return err
}

// SendRaw 发送原始字节（用于 P2P 转发 IP 包等场景）。
func (u *UDPConn) SendRaw(addr *net.UDPAddr, data []byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	_, err := u.conn.WriteToUDP(data, addr)
	return err
}

// ReadPacket 读取一个报文，返回原始字节和发送方地址。
// 设置超时，超时返回 error。
func (u *UDPConn) ReadPacket(timeout time.Duration) ([]byte, *net.UDPAddr, error) {
	_ = u.conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, protocol.ReadBufferSize)
	n, remote, err := u.conn.ReadFromUDP(buf)
	if err != nil {
		return nil, nil, err
	}
	data := make([]byte, n)
	copy(data, buf[:n])
	return data, remote, nil
}

// Start 启动持续读循环，每收到一个报文调用 handler。
// handler 在独立 goroutine 中执行，不阻塞读循环。
// ctx 取消时退出循环。
func (u *UDPConn) Start(ctx context.Context, handler func(remote *net.UDPAddr, data []byte)) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			data, remote, err := u.ReadPacket(1 * time.Second)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// 读超时是正常的（没有包时等待），仅网络错误才记录。
				continue
			}
			go handler(remote, data)
		}
	}()
}
