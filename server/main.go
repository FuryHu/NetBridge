package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/FuryHu/netbridge/server/internal/room"
	"github.com/FuryHu/netbridge/server/internal/server"
	"github.com/FuryHu/netbridge/server/internal/signaling"
)

func main() {
	cfg := LoadConfig()

	// 结构化日志输出到 stdout，systemd 下由 journald 统一接管。
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 绑定 UDP socket。
	//
	// 监听 [::] 同时接受 IPv4 + IPv6——Go 在 dual-stack 系统上会自动开 IPV6_V6ONLY=0，
	// IPv4 客户端会以 v4-mapped IPv6 形式（::ffff:x.x.x.x）出现在 ReadFromUDP 的 remote 里。
	// 后续在房间里给客户端回的 PublicAddress 会调用 normalizeUDPAddr 规整为纯 v4 或纯 v6 表示。
	//
	// 若用户在 -addr 显式写了 IPv4（如 0.0.0.0:10555），保持原行为只接受 IPv4。
	addr, err := net.ResolveUDPAddr("udp", cfg.Addr)
	if err != nil {
		log.Error("解析监听地址失败", "addr", cfg.Addr, "err", err)
		os.Exit(1)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Error("UDP 监听失败", "addr", cfg.Addr, "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	// 读写缓冲都调大——Civ 6 等大流量场景下，回合切换瞬间多人同步包会瞬时打爆默认 8KB 内核缓冲。
	_ = conn.SetReadBuffer(4 * 1024 * 1024)
	_ = conn.SetWriteBuffer(4 * 1024 * 1024)

	// 优雅退出：捕获 Ctrl+C / systemd stop（SIGTERM）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(conn, nil, log) // handler 在下面注入

	// 创建房间管理器并注入 RoomHandler（阶段 2 核心）。
	mgr := room.NewManager()
	srv.SetHandler(signaling.NewRoomHandler(srv, mgr, log, ctx))

	log.Info("NetBridge Server 启动", "addr", cfg.Addr, "port", cfg.Port, "timeout", cfg.RoomTimeout)

	if err := srv.Serve(ctx); err != nil {
		log.Error("服务退出异常", "err", err)
		os.Exit(1)
	}
	log.Info("NetBridge Server 已停止")
}
