//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RestartAsAdmin 通过 osascript 弹出系统管理员授权框，以 root 重新启动本应用，
// 当前进程随后退出。
//
// macOS 上 utun 只能由 root 创建，所以第一期让整个应用以 root 跑（整 app 提权），
// 而不是拆一个只做网卡操作的 root helper。已知代价，Phase 0 要在真机上确认第二点：
//
//   - 每次启动都要输一次管理员密码（系统授权会缓存一小段时间）；
//   - root 下的麦克风授权走的是另一套 TCC 判定，可能被直接拒掉。
//     若真被拒，第一期就得改成拆分 root helper（特权只留在 helper 里）。
func RestartAsAdmin() error {
	if os.Geteuid() == 0 {
		// 已经是 root 就不用再提权。这同时是防死循环的兜底：提权实例再走到
		// 这里会直接返回错误，不会无限弹密码框。
		return fmt.Errorf("当前已是 root，无需提权")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// do shell script 只吃一段 shell 命令字符串，两层引号都要自己处理：
	//   shellQuote - 让 sh 把整条路径当成一个词（.app 路径常带空格）
	//   appleQuote - 让 AppleScript 把它当成字面量
	// 末尾的 & 是必须的：do shell script 默认一直等到命令结束，而本应用是常驻
	// GUI，不后台化就会把 osascript 连同本进程一起卡住。重定向到 /dev/null 同样
	// 必要——否则后台进程会一直握着 osascript 收集输出用的管道写端，
	// osascript 仍要等到管道关闭才返回。
	script := fmt.Sprintf("do shell script %s with administrator privileges",
		appleQuote(shellQuote(exe)+" >/dev/null 2>&1 &"))

	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		// 用户点"取消"也走这里（osascript 退出码 1），所以别当成崩溃。
		return fmt.Errorf("提权失败（用户取消或 osascript 报错）: %v: %s",
			err, strings.TrimSpace(string(out)))
	}

	os.Exit(0)
	return nil
}

// shellQuote 把 s 包成 POSIX sh 的单引号字符串。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appleQuote 把 s 包成 AppleScript 的双引号字面量。
func appleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
