//go:build windows

package main

import "fmt"

// prepareTunRuntime 在创建网卡前准备平台运行时。
// Windows 上需要把内嵌的 wintun.dll 释放到可执行文件同目录。
func prepareTunRuntime() error {
	if err := extractWintunDLL(); err != nil {
		return fmt.Errorf("释放 wintun.dll 失败: %w", err)
	}
	return nil
}

// hideOnCloseEnabled 关闭窗口时是否"隐藏到托盘"而不是退出。
//
// 仅当真的有托盘可回时才有意义——Windows 上托盘菜单提供"显示主窗口"，
// 隐藏后用户能回来；macOS 首期没有托盘，隐藏即失联（见 platform_darwin.go）。
func hideOnCloseEnabled() bool { return true }
