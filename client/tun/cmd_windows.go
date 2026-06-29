//go:build windows

package tun

import (
	"os/exec"
	"syscall"
)

// runHidden 创建一个不会弹出控制台窗口的 *exec.Cmd。
//
// Go 在 Windows 上默认通过 CreateProcess 启动子进程并继承控制台句柄；
// 即使父进程是 GUI（Wails 打包后是 windows 子系统），调用 netsh / powershell
// 这类控制台程序时，Windows 仍会为其分配一个新控制台窗口——在屏幕上一闪而过。
//
// CREATE_NO_WINDOW (0x08000000) 告诉 CreateProcess 不要为子进程分配控制台，
// stdin/stdout/stderr 仍可通过管道捕获，命令照常执行。
//
// 仅在 Windows 构建中使用，所以这个文件加了 //go:build windows。
func runHidden(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return cmd
}
