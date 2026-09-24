//go:build darwin

package tun

import "os/exec"

// runHidden 在 macOS 上就是普通的 exec.Command。
//
// Windows 版本要设 CREATE_NO_WINDOW，是因为 GUI 父进程调用 netsh 这类控制台程序时
// 系统会给子进程分配一个新控制台窗口，在屏幕上一闪而过；macOS 没有这个行为，
// 也不需要等价的标志。保留同名函数是为了让 tun_darwin.go 与 tun_real.go 的
// 调用点读起来完全一致。
func runHidden(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
