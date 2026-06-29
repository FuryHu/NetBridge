package tun

import (
	"context"
	"log/slog"
	"sync"

	"github.com/FuryHu/netbridge/client/core"
)

type Bridge struct {
	tun    NetAdapter
	client *core.Client
	log    *slog.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewBridge(tun NetAdapter, client *core.Client) *Bridge {
	return &Bridge{tun: tun, client: client, log: slog.Default()}
}

func (b *Bridge) SetLogger(log *slog.Logger) { b.log = log }

func (b *Bridge) Start(ctx context.Context) {
	ctx, b.cancel = context.WithCancel(ctx)
	b.client.SetDataHandler(b.handleInbound)
	b.wg.Add(1)
	go b.outboundLoop(ctx)
	b.log.Info("TUN Bridge 已启动")
}

func (b *Bridge) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
	b.wg.Wait()
	b.log.Info("TUN Bridge 已停止")
}

func (b *Bridge) outboundLoop(ctx context.Context) {
	defer b.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		raw, err := b.tun.ReadPacket()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if len(raw) < 20 {
			continue
		}
		if raw[0]>>4 != 4 {
			continue // 只处理 IPv4，IPv6/ARP/其他直接忽略
		}
		// 直接读裸字节，绕开 net.IPv4 的 16 字节 IPv4-in-IPv6 表示坑。
		d0, d1, d2, d3 := raw[16], raw[17], raw[18], raw[19]

		// 受限广播 255.255.255.255 与子网广播 10.66.255.255 都按广播处理。
		// Civ 6 的 LAN 大厅扫描就是发到 .255 这种广播地址。
		if d0 == 255 && d1 == 255 && d2 == 255 && d3 == 255 {
			b.broadcast(raw)
			continue
		}
		if d0 == 10 && d1 == 66 && d2 == 255 && d3 == 255 {
			b.broadcast(raw)
			continue
		}
		// 多播（224.0.0.0/4）也按广播分发，覆盖 SSDP / mDNS 类发现。
		if d0 >= 224 && d0 <= 239 {
			b.broadcast(raw)
			continue
		}

		if d0 != 10 || d1 != 66 {
			continue // 目的地不在虚拟网段，丢弃
		}
		dstVIP := uint32(d3) | uint32(d2)<<8
		if dstVIP == 0 {
			continue
		}
		b.client.SendToPeer(dstVIP, raw)
	}
}

func (b *Bridge) handleInbound(srcVIP uint32, data []byte) {
	b.tun.WritePacket(data)
}

func (b *Bridge) broadcast(data []byte) {
	selfVIP := b.client.GetSelf().VirtualIP
	for _, p := range b.client.GetPeers() {
		if p.VirtualIP == selfVIP {
			continue
		}
		b.client.SendToPeer(p.VirtualIP, data)
	}
}
