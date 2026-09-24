//go:build darwin

package main

// build/darwin/Info.plist 里几个 macOS 专有 key 的由来（plist 本身写不下注释，
// 而 plist 的 XML 注释在 macOS 上是否被接受没有权威说法，就不冒险了）：
//
//   LSMinimumSystemVersion = 12.0
//     WKWebView 里 getUserMedia 要在 WKUIDelegate 的
//     requestMediaCapturePermissionForOrigin 里放行，那个回调 macOS 12 才有
//     （更早的版本只有私有 SPI）。所以最低版本定 12.0。
//
//   NSMicrophoneUsageDescription
//     缺这个 key，TCC 会直接杀掉进程，而不是弹授权框。
//
//   NSLocalNetworkUsageDescription（macOS 15+ 的"本地网络"权限）
//     客户端要把局域网广播/多播转发给同一物理局域网内的对端（游戏大厅发现
//     靠的就是这个）。缺这个 key 时系统要么用默认文案、要么静默拒绝。
//     目前只是加了声明，真机上要用"两台同一 WiFi 的机器"验证是否弹框、是否放行。

// prepareTunRuntime 在 macOS 上无事可做：utun 是内核自带的接口，
// 不像 wintun 那样需要释放驱动 DLL。
func prepareTunRuntime() error { return nil }

// hideOnCloseEnabled 始终为 false：macOS 首期不做菜单栏图标，关窗即退出。
//
// 不能用"关窗=隐藏"的 Windows 语义，根本原因是 Wails v2 在 darwin 上把
// **关窗按钮、Cmd+Q（菜单 Quit）、系统退出**三条路径合并成了同一个回调，无法区分：
//
//	WindowDelegate.windowShouldClose / WailsContext.Quit / AppDelegate.applicationShouldTerminate
//	  → processMessage("Q") → Frontend.Quit() → OnBeforeClose(ctx)
//
// 于是 OnBeforeClose 一旦返回 true（阻止关闭），关窗按钮就变成**完全没反应**——
// windowShouldClose 已经固定返回 false，窗口不会关；Go 侧又不调 [NSApp stop]，
// 应用也不会退。Cmd+Q 同样失灵，用户只能强制退出。
//
// 就算改成"隐藏窗口"也不行：Wails 的 AppDelegate 没实现
// applicationShouldHandleReopen，被隐藏的窗口点 Dock 图标回不来。
//
// 所以没有托盘时就老实退出（Wails 默认 HideWindowOnClose=false，ObjC 层的
// windowShouldClose 本就会直接走 "Q"→退出，与这里返回 false 是一致的）。
// Phase 3 补上真正的 NSStatusItem 托盘、且能处理 Dock 重开之后，再改回 true。
func hideOnCloseEnabled() bool { return false }
