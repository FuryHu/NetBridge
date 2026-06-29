//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// RestartAsAdmin 以管理员权限重新启动当前程序，当前进程随后退出。
func RestartAsAdmin() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	_ = windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"),
		windows.StringToUTF16Ptr(exe), nil, nil, windows.SW_SHOWNORMAL)
	os.Exit(0)
	return nil
}
