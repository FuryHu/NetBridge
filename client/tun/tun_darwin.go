//go:build darwin

package tun

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/tun"
)

// utunHeaderSize 是 darwin 的 utun 在每包前面保留的 4 字节地址族头（AF_INET/AF_INET6）。
//
// wireguard 的 tun 包按 wireguard-go 的约定实现：调用方传 offset=utunHeaderSize，
// 数据落在 buf[offset:]，头字节由库自己填（写）或剥（读）。因此读写缓冲都必须
// 多留这 4 字节，offset 传小于 4 的值会直接返回 io.ErrShortBuffer。
const utunHeaderSize = 4

// maxPacketSize 是单包读取缓冲的上界。虚拟网段 MTU 是 1400，留一倍余量，
// 避免内核交出超长包时被静默截断。
const maxPacketSize = 2048

// DarwinTunAdapter 用 utun 实现 NetAdapter。
//
// 与 Windows 的 WinTunAdapter 的三处差异（都源于 utun 是内核自带的点对点接口）：
//   - 网卡名由内核分配（utunN），不能自定义——创建时传 "utun" 让内核挑一个空闲号。
//   - 没有链路层，也没有链路层广播：广播/多播包能否发出完全取决于路由表，
//     而不是网卡自身的语义（Windows 上则依赖网卡的 /16 掩码与网络类别）。
//   - 不需要释放任何驱动 DLL，但创建 utun 与改路由都要求 root。
type DarwinTunAdapter struct {
	name   string
	device tun.Device
	log    *slog.Logger

	// writeMu 串行化 WritePacket，并保护复用的 wrBuf。
	// 收包路径（netconn 的 dispatch）每包一个 goroutine，会并发调 handleInbound。
	writeMu sync.Mutex
	wrBuf   []byte

	closeOnce sync.Once
	closeErr  error
}

// Create 创建一张 NetBridge 虚拟网卡并配置好虚拟 IP。
//
// name 参数在 darwin 上被忽略（utun 名字由内核分配），保留是为了与 Windows
// 侧 CreateWinTun 的签名一致。MTU 由 wireguard 的 tun 包经 ioctl 设置。
func Create(name, vip string, log *slog.Logger) (NetAdapter, error) {
	if log == nil {
		log = slog.Default()
	}

	// 传 "utun" 而非具体名字：库只在名字形如 utun[0-9]+ 时才接受，
	// 传 "NetBridge" 会直接报错；传 "utun" 则由内核分配下一个空闲号。
	device, err := tun.CreateTUN("utun", defaultMTU)
	if err != nil {
		return nil, fmt.Errorf("创建 utun 失败（需要 root 权限）: %w", err)
	}

	realName, err := device.Name()
	if err != nil {
		device.Close()
		return nil, fmt.Errorf("读取 utun 名字失败: %w", err)
	}

	a := &DarwinTunAdapter{name: realName, device: device, log: log}

	// 必须有人消费 Events()：库里的路由监听 goroutine 是**阻塞发送**（缓冲 10），
	// 没人读就会卡死并泄漏 goroutine。顺带把接口状态变化记进日志——
	// 排查"网卡建好了但包不通"时，up/down/MTU 是最有用的线索。
	go a.drainEvents()

	if err := a.SetVIP(vip); err != nil {
		a.Close()
		return nil, fmt.Errorf("设置虚拟 IP 失败: %w", err)
	}

	log.Info("utun 网卡就绪", "name", realName, "vip", vip)
	return a, nil
}

// defaultMTU 与 protocol.DefaultMTU 保持一致，避免 tun 包反向依赖 protocol。
const defaultMTU = 1400

func (a *DarwinTunAdapter) drainEvents() {
	for ev := range a.device.Events() {
		switch ev {
		case tun.EventUp:
			a.log.Info("utun 接口 up", "name", a.name)
		case tun.EventDown:
			a.log.Info("utun 接口 down", "name", a.name)
		case tun.EventMTUUpdate:
			a.log.Info("utun MTU 变化", "name", a.name)
		}
	}
}

func (a *DarwinTunAdapter) ReadPacket() ([]byte, error) {
	buf := make([]byte, utunHeaderSize+maxPacketSize)
	bufs := [][]byte{buf}
	sizes := []int{0}

	for {
		n, err := a.device.Read(bufs, sizes, utunHeaderSize)
		if err != nil {
			return nil, fmt.Errorf("utun 读包失败: %w", err)
		}
		if n == 0 || sizes[0] == 0 {
			// 不该发生（内核不会交空包），但真发生时要重试而不是返回错误——
			// bridge 的 outboundLoop 遇错只 continue，会把读错误变成 CPU 热循环。
			continue
		}
		// 拷出负载段：调用方持有返回值，而 buf 下一轮就复用。
		out := make([]byte, sizes[0])
		copy(out, buf[utunHeaderSize:utunHeaderSize+sizes[0]])
		return out, nil
	}
}

func (a *DarwinTunAdapter) WritePacket(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	need := utunHeaderSize + len(data)
	if cap(a.wrBuf) < need {
		a.wrBuf = make([]byte, need)
	}
	buf := a.wrBuf[:need]
	copy(buf[utunHeaderSize:], data) // 前 4 字节留给库填地址族

	bufs := [][]byte{buf}
	n, err := a.device.Write(bufs, utunHeaderSize)
	if err != nil {
		return fmt.Errorf("utun 写包失败: %w", err)
	}
	if n != len(bufs) {
		// 库的返回是"成功写入的缓冲个数"，不是字节数。
		return fmt.Errorf("utun 写包不完整: %d/%d", n, len(bufs))
	}
	return nil
}

// SetVIP 配置/重新配置网卡的虚拟 IP、MTU 与路由。
//
// 设计为幂等：同一个 adapter 实例可在不同房间间切换 VIP 时反复调用
// （与 WinTunAdapter.SetVIP 的约定一致）。
//
// 与 netsh 版本的对应关系：
//   - `netsh ... source=static addr=X mask=255.255.0.0` → `ifconfig <if> inet X X netmask 255.255.0.0`
//     utun 是点对点接口，第二段地址（对端）也填自身——macOS 上这是标准写法。
//   - `netsh ... metric=1`（保证多网卡机器优先走虚拟网卡）在 macOS 不需要：
//     路由按前缀长度择优，10.66.0.0/16 天然比默认路由更具体。
//   - MTU 不在这一步设置：wireguard 的 tun 包在 CreateTUN 时已经用 ioctl 设好了。
//   - Windows 侧的防火墙入站白名单（`NetBridge Trust 10.66/16`）在 macOS 无对应物，
//     macOS 默认不拦本地进程间的入站流量，只有应用防火墙（ALF）开着才需要干预。
func (a *DarwinTunAdapter) SetVIP(vip string) error {
	if net.ParseIP(vip) == nil {
		return fmt.Errorf("无效 IP: %s", vip)
	}

	// 先把接口地址配好——route 依赖它已存在。
	if out, err := runHidden("ifconfig", a.name, "inet", vip, vip,
		"netmask", "255.255.0.0", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig 设置 IP 失败: %w\n输出: %s", err, string(out))
	}

	// 显式加网段路由。掩码 255.255.0.0 通常已经生成了 10.66.0.0/16 的直连路由，
	// 此时这条命令会报 "File exists"——那是幂等成功的信号，不算错误。
	if out, err := runHidden("route", "-n", "add", "-net", "10.66.0.0/16",
		"-interface", a.name).CombinedOutput(); err != nil {
		if !isRouteExists(out) {
			return fmt.Errorf("route 添加网段路由失败: %w\n输出: %s", err, string(out))
		}
	}

	return nil
}

// isRouteExists 判断 route 命令的失败是否只是"路由已存在"。
// route(8) 用 exit status 1 表示各种错误，只能靠 stderr 文本区分。
func isRouteExists(out []byte) bool {
	lower := strings.ToLower(string(out))
	return strings.Contains(lower, "file exists") || strings.Contains(lower, "already in table")
}

func (a *DarwinTunAdapter) Close() error {
	a.closeOnce.Do(func() {
		a.log.Info("关闭 utun 网卡", "name", a.name)
		// 关闭 fd 即销毁接口，内核会连带清掉经它走的路由——不需要像 Windows
		// 那样显式删防火墙规则。
		if a.device != nil {
			a.closeErr = a.device.Close()
		}
	})
	return a.closeErr
}
