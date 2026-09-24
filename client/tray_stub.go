//go:build !windows

package main

import "log/slog"

// 非 Windows 平台的托盘占位实现。
//
// 这里只提供与 tray_windows.go 一致的接口，让 app.go 不必写平台分支就能编译，
// 所有方法都是 no-op。macOS 上配合 hideOnCloseEnabled() 返回 false，关窗 / Cmd+Q
// 都是真正退出，不存在"隐藏到托盘"这条语义（原因见 platform_darwin.go）。
//
// 为什么 macOS 第一期不做真托盘：Cocoa 要求 NSStatusItem 在主线程创建，而 Wails
// 的 OnStartup 跑在工作 goroutine 上（见记忆 wails-no-tray-api），直接调第三方
// 托盘库会崩。要做就得 dispatch_async 回主队列，属于 cgo/ObjC 桥的活，留 Phase 3。
//
// 保留回调字段而不是删掉：Phase 3 接上真托盘时，App 侧三个回调
// （showFromTray / toggleVoiceFromTray / quitFromTray）已经写好，不用再动 app.go。
type tray struct {
	log *slog.Logger

	onShow        func()
	onToggleVoice func()
	onQuit        func()
}

func newTray(log *slog.Logger, onShow, onToggleVoice, onQuit func()) *tray {
	return &tray{
		log:           log,
		onShow:        onShow,
		onToggleVoice: onToggleVoice,
		onQuit:        onQuit,
	}
}

// setMenuState 无事可做：没有菜单要更新文案。
// 保持签名一致，让前端的 SetTrayMenuState 不需要平台判断。
func (t *tray) setMenuState(showLabel, voiceLabel, quitLabel string, voiceOn bool) {}

// start 是 no-op：不创建任何 UI。
func (t *tray) start() {}

// stop 是 no-op：没有资源要清理；幂等，可重复调用。
func (t *tray) stop() {}
