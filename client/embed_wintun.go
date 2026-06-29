//go:build windows && cgo

package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// embedWintunDLL 内嵌 wintun.dll（需先从 https://www.wintun.net/ 下载放入 assets/）。
//
//go:embed assets/wintun.dll
var wintunDLL []byte

// extractWintunDLL 将内嵌的 wintun.dll 释放到可执行文件同目录。
// 程序退出时自动清理（可选，非必须。残留不影响下次运行，会被覆盖）。
func extractWintunDLL() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取 exe 路径失败: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	dllPath := filepath.Join(exeDir, "wintun.dll")

	// 如果已存在且大小一致，跳过（避免每次启动都写磁盘）。
	if existing, err := os.ReadFile(dllPath); err == nil && len(existing) == len(wintunDLL) {
		return nil
	}

	if err := os.WriteFile(dllPath, wintunDLL, 0644); err != nil {
		return fmt.Errorf("释放 wintun.dll 失败: %w", err)
	}
	return nil
}
