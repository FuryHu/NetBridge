//go:build windows

package main

// 系统托盘（原生 Win32 实现）。
//
// 为什么不用第三方托盘库：client 体积敏感（见记忆 client-binary-size），
// 第三方库会给 exe 增加数百 KB。Wails v2.12.0 又没有应用层托盘 API（见
// wails-no-tray-api），所以这里直接走 Shell_NotifyIconW + 隐藏消息窗口 +
// TrackPopupMenuEx，全部用 syscall（golang.org/x/sys 已是依赖），零新增依赖。
//
// 托盘图标 + 右键菜单跑在一个独立 goroutine 的消息循环里，与 Wails 主线程的
// webview2 消息循环互不干扰。菜单项回调里调 wailsRuntime（WindowShow /
// EventsEmit / Quit）--这些函数本身做了 LockOSThread 或 Invoke 到主线程，跨
// goroutine 调用安全。

import (
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---- Win32 消息 / 标志常量 ----
const (
	wmTray          = 0x8000 // WM_APP：托盘鼠标事件的回调消息
	wmCommand       = 0x0111
	wmClose         = 0x0010
	wmDestroy       = 0x0002
	wmNull          = 0x0000
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	messageOnly = ^uintptr(2) // HWND_MESSAGE = (HWND)-3

	// NOTIFYICONDATA
	nimAdd     = 0x00000000
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	// 菜单
	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	mfChecked      = 0x00000008
	tpmRightAlign  = 0x0008
	tpmBottomAlign = 0x0020

	idiApplication = 32512

	// 托盘右键菜单命令 ID（与 WM_COMMAND wParam 低字匹配）
	cmdShow = iota + 0x9000
	cmdToggleVoice
	cmdQuit
)

// ---- 懒加载的 Win32 proc ----
var (
	pKernel32         = syscall.NewLazyDLL("kernel32.dll")
	pGetModuleHandleW = pKernel32.NewProc("GetModuleHandleW")

	pUser32              = syscall.NewLazyDLL("user32.dll")
	pRegisterClassExW    = pUser32.NewProc("RegisterClassExW")
	pCreateWindowExW     = pUser32.NewProc("CreateWindowExW")
	pDefWindowProcW      = pUser32.NewProc("DefWindowProcW")
	pGetMessageW         = pUser32.NewProc("GetMessageW")
	pTranslateMessage    = pUser32.NewProc("TranslateMessage")
	pDispatchMessageW    = pUser32.NewProc("DispatchMessageW")
	pPostMessageW        = pUser32.NewProc("PostMessageW")
	pPostQuitMessage     = pUser32.NewProc("PostQuitMessage")
	pDestroyWindow       = pUser32.NewProc("DestroyWindow")
	pUnregisterClassW    = pUser32.NewProc("UnregisterClassW")
	pLoadIconW           = pUser32.NewProc("LoadIconW")
	pCreatePopupMenu     = pUser32.NewProc("CreatePopupMenu")
	pAppendMenuW         = pUser32.NewProc("AppendMenuW")
	pTrackPopupMenuEx    = pUser32.NewProc("TrackPopupMenuEx")
	pDestroyMenu         = pUser32.NewProc("DestroyMenu")
	pGetCursorPos        = pUser32.NewProc("GetCursorPos")
	pSetForegroundWindow = pUser32.NewProc("SetForegroundWindow")

	pShell32          = syscall.NewLazyDLL("shell32.dll")
	pShellNotifyIconW = pShell32.NewProc("Shell_NotifyIconW")
)

// ---- Win32 结构体（字段顺序与 SDK 对齐，靠 Go 默认对齐得到一致内存布局）----
type tPoint struct{ X, Y int32 }

type tMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      tPoint
}

type tWndClassEx struct {
	Size, Style uint32
	WndProc     uintptr
	ClsExtra    int32
	WndExtra    int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	BgBrush     uintptr
	MenuName    *uint16
	ClassName   *uint16
	IconSm      uintptr
}

// notifyIconData 对应 NOTIFYICONDATAW。CbSize 用 unsafe.Sizeof 取，避免手算。
// GUIDItem 用 [16]byte（对齐 1）替代 windows.GUID（对齐 4）--所处偏移已是
// 4/8 对齐，二者字节布局完全一致。
type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeout         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GUIDItem         [16]byte
}

// tray 持有托盘的全部状态。回调（onShow/onToggleVoice/onQuit）由 App 提供，
// 菜单项触发时在托盘线程上执行。
type tray struct {
	log   *slog.Logger
	cb    uintptr // syscall.NewCallback 句柄，必须保活
	hwnd  uintptr
	hIcon uintptr

	onShow        func()
	onToggleVoice func()
	onQuit        func()

	// menu 是右键菜单的显示状态：翻译后的文案由前端推送（i18n 单一来源），
	// voiceOn 仅用于决定"语音"项是否打勾，是展示态而非业务态（业务权威仍在前端）。
	// menuMu 保护 menu：前端在 IPC 线程写、托盘线程在 showMenu 里读。
	menuMu sync.Mutex
	menu   trayMenuState

	wg     sync.WaitGroup
	closed atomic.Bool
}

// trayMenuState 是前端推送给托盘的菜单显示状态。
type trayMenuState struct {
	showLabel  string
	voiceLabel string
	quitLabel  string
	voiceOn    bool
}

func newTray(log *slog.Logger, onShow, onToggleVoice, onQuit func()) *tray {
	return &tray{
		log:           log,
		onShow:        onShow,
		onToggleVoice: onToggleVoice,
		onQuit:        onQuit,
		// 前端 webview 就绪前若弹出菜单，先用兜底文案，避免空白项。
		menu: trayMenuState{
			showLabel:  "显示主窗口",
			voiceLabel: "语音",
			quitLabel:  "退出 NetBridge",
		},
	}
}

// setMenuState 由 App.SetTrayMenuState 转发，更新菜单文案与语音打勾状态。
func (t *tray) setMenuState(showLabel, voiceLabel, quitLabel string, voiceOn bool) {
	t.menuMu.Lock()
	t.menu.showLabel = showLabel
	t.menu.voiceLabel = voiceLabel
	t.menu.quitLabel = quitLabel
	t.menu.voiceOn = voiceOn
	t.menuMu.Unlock()
}

// start 在后台 goroutine 里跑托盘消息循环，立即返回。
func (t *tray) start() {
	t.cb = syscall.NewCallback(t.wndProc)
	t.wg.Add(1)
	go t.run()
}

// stop 向托盘窗口投递 WM_CLOSE，等待 goroutine 退出并清理图标。
// 可在任意 goroutine 调用（如 App.shutdown）。
func (t *tray) stop() {
	if t.hwnd == 0 || t.closed.Swap(true) {
		return
	}
	pPostMessageW.Call(t.hwnd, uintptr(wmClose), 0, 0)
	t.wg.Wait()
}

// run 创建消息窗口、注册托盘图标，然后跑消息循环直到收到 WM_QUIT。
func (t *tray) run() {
	defer t.wg.Done()

	// Win32 窗口有线程亲和性：创建它的线程必须负责 GetMessage/DispatchMessage/
	// DestroyWindow。锁住当前 OS 线程，防止 Go 把 goroutine 迁到别的线程导致
	// 消息泵失效。
	runtime.LockOSThread()

	hInst, _, _ := pGetModuleHandleW.Call(0)

	className := windows.StringToUTF16Ptr("NetBridgeTrayWnd")
	wc := tWndClassEx{}
	wc.Size = uint32(unsafe.Sizeof(wc))
	wc.WndProc = t.cb
	wc.Instance = hInst
	wc.ClassName = className
	if r, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		t.log.Error("托盘：注册窗口类失败")
		return
	}

	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, // WS_OVERLAPPED
		0, 0, 0, 0,
		uintptr(messageOnly),
		0, hInst, 0,
	)
	if hwnd == 0 {
		t.log.Error("托盘：创建消息窗口失败")
		return
	}
	t.hwnd = hwnd

	t.hIcon = loadAppIcon(hInst)
	t.addIcon("NetBridge")

	for {
		var m tMsg
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || int32(r) == -1 { // 0=WM_QUIT，-1=错误
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	// 清理顺序：先删图标再销毁窗口，避免托盘残留空壳图标。
	t.delIcon()
	pDestroyWindow.Call(hwnd)
	pUnregisterClassW.Call(uintptr(unsafe.Pointer(className)), hInst)
}

// wndProc 是托盘消息窗口的窗口过程，在托盘线程上被调用。
func (t *tray) wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmTray:
		// lParam 低字 = 鼠标消息。
		switch uint32(lparam) & 0xFFFF {
		case wmLButtonDblClk:
			if t.onShow != nil {
				t.onShow()
			}
		case wmRButtonUp:
			t.showMenu(hwnd)
		}
		return 0
	case wmCommand:
		// wParam 低字 = 菜单命令 ID。
		switch uint32(wparam) & 0xFFFF {
		case cmdShow:
			if t.onShow != nil {
				t.onShow()
			}
		case cmdToggleVoice:
			if t.onToggleVoice != nil {
				t.onToggleVoice()
			}
		case cmdQuit:
			if t.onQuit != nil {
				t.onQuit()
			}
		}
		return 0
	case wmDestroy:
		// DefWindowProc 收到 WM_CLOSE 会调 DestroyWindow -> WM_DESTROY。
		// 这里发 WM_QUIT 让 GetMessage 返回 0，退出 run() 的循环。
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// showMenu 在鼠标位置弹出右键菜单。
func (t *tray) showMenu(hwnd uintptr) {
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer pDestroyMenu.Call(menu)

	// 取当前菜单显示状态（前端推送的翻译文案 + 语音打勾）。
	m := func() trayMenuState {
		t.menuMu.Lock()
		defer t.menuMu.Unlock()
		return t.menu
	}()

	// 注意：每个 UTF-16 指针都要用局部变量持有，并在 syscall.Call 参数里
	// 直接 uintptr(unsafe.Pointer(p)) 内联转换--这样 Go 运行时才会保证该缓冲区
	// 在 syscall 执行期间不被 GC 回收（unsafe.Pointer 规则 4）。放进辅助函数
	// 里返回 uintptr 会破坏这个保证。
	lShow := windows.StringToUTF16Ptr(m.showLabel)
	pAppendMenuW.Call(menu, uintptr(mfString), uintptr(cmdShow), uintptr(unsafe.Pointer(lShow)))
	pAppendMenuW.Call(menu, uintptr(mfSeparator), 0, 0)
	// "语音"项：voiceOn 为真时加 mfChecked，在菜单项前打勾，直观反映当前开关状态。
	voiceFlags := uintptr(mfString)
	if m.voiceOn {
		voiceFlags |= uintptr(mfChecked)
	}
	lVoice := windows.StringToUTF16Ptr(m.voiceLabel)
	pAppendMenuW.Call(menu, voiceFlags, uintptr(cmdToggleVoice), uintptr(unsafe.Pointer(lVoice)))
	pAppendMenuW.Call(menu, uintptr(mfSeparator), 0, 0)
	lQuit := windows.StringToUTF16Ptr(m.quitLabel)
	pAppendMenuW.Call(menu, uintptr(mfString), uintptr(cmdQuit), uintptr(unsafe.Pointer(lQuit)))

	var pt tPoint
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	// 托盘菜单两个必备技巧：弹菜单前 SetForegroundWindow + 弹完后投 WM_NULL，
	// 否则菜单点外部不会自动消失（Windows 的已知行为）。
	pSetForegroundWindow.Call(hwnd)
	pTrackPopupMenuEx.Call(menu, uintptr(tpmRightAlign|tpmBottomAlign),
		uintptr(pt.X), uintptr(pt.Y), hwnd, 0)
	pPostMessageW.Call(hwnd, uintptr(wmNull), 0, 0)
}

// addIcon / delIcon 通过 Shell_NotifyIconW 增删托盘图标。
func (t *tray) addIcon(tip string) {
	nid := notifyIconData{}
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = t.hwnd
	nid.UID = 1
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmTray
	nid.HIcon = t.hIcon
	copy(nid.SzTip[:], windows.StringToUTF16(tip))
	pShellNotifyIconW.Call(uintptr(nimAdd), uintptr(unsafe.Pointer(&nid)))
}

func (t *tray) delIcon() {
	nid := notifyIconData{}
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = t.hwnd
	nid.UID = 1
	pShellNotifyIconW.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&nid)))
}

// loadAppIcon 加载 exe 自身图标资源。Wails 的 Windows 资源编译把 app.ico 放在
// 资源 ID 3（见 winc.AppIconID = 3），所以这里取 ID 3，拿不到回退系统默认图标。
// 两种都是共享资源，无需 DestroyIcon。这样不内嵌第二份图标，零额外体积。
func loadAppIcon(hInst uintptr) uintptr {
	if h, _, _ := pLoadIconW.Call(hInst, 3); h != 0 {
		return h
	}
	if h, _, _ := pLoadIconW.Call(0, idiApplication); h != 0 {
		return h
	}
	return 0
}
