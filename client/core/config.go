package core

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Config 客户端运行配置。
type Config struct {
	// ServerAddr 服务端地址，如 "vps-ip:10555"
	ServerAddr string
	// Room 房间号/暗号
	Room string
	// NickName 玩家昵称
	NickName string
	// PeerID 客户端唯一标识，与"这台机器"绑定，跨重启稳定。
	PeerID string
	// ServerPort 服务端端口，默认 10555
	ServerPort int
}

// DefaultConfig 返回带默认值的配置。
//
// PeerID 通过 persistentPeerID() 持久化到用户配置目录：
//   - Windows: %APPDATA%\NetBridge\peer_id
//   - 其他系统：~/.config/netbridge/peer_id
//
// 这样同一台机器无论重启多少次、退出再进入房间，服务端都识别为同一个 peer，
// 复用同一个虚拟 IP，避免"每次进房间 VIP 就 +1"。
func DefaultConfig() Config {
	return Config{
		ServerPort: 10555,
		PeerID:     persistentPeerID(),
	}
}

// persistentPeerID 从配置文件读取 PeerID；若不存在则新生成一个并写回。
// 读写失败时退化为内存 uuid——功能不丢，只是失去跨重启稳定性。
func persistentPeerID() string {
	path, err := peerIDFilePath()
	if err != nil {
		return uuid.NewString()
	}
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if _, err := uuid.Parse(id); err == nil {
			return id
		}
		// 文件存在但内容损坏 → 重新生成。
	}
	id := uuid.NewString()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(id), 0o644)
	return id
}

// peerIDFilePath 返回 peer_id 文件的绝对路径。
func peerIDFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "NetBridge", "peer_id"), nil
}

