//go:build windows

package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	nimAdd    = 0
	nimDelete = 2

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	trayIconID = 1
	hotkeyID   = 1

	wmCreate      = 0x0001
	wmSetText     = 0x000C
	wmCommand     = 0x0111
	wmSetFont     = 0x0030
	wmHotkey      = 0x0312
	wmAppTray     = 0x8002
	wmAppFollow   = 0x8003
	wmAppSettings = 0x8005
	wmSysCommand  = 0x0112
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmChar        = 0x0102
	wmSysKeyDown  = 0x0104
	wmSysKeyUp    = 0x0105
	wmSysChar     = 0x0106

	trayCmdSettings = 2001
	trayCmdToggle   = 2002
	trayCmdConsole  = 2003
	trayCmdExit     = 2004

	ctrlAutostart    = 1001
	ctrlFollowExe    = 1002
	ctrlHotkeyLabel  = 1003
	ctrlHotkeyButton = 1004
	ctrlStatus       = 1005
	ctrlOpacity      = 1006
	ctrlAutoFollow   = 1007

	idOK     = 1
	idCancel = 2

	swHide       = 0
	swShowNormal = 1

	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1
	bstChecked = 1

	wsChild       = 0x40000000
	wsVisible     = 0x10000000
	wsBorder      = 0x00800000
	wsTabStop     = 0x00010000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	ssCenterImage = 0x00000200

	bsPushButton    = 0x00000000
	bsDefPushButton = 0x00000001
	bsAutoCheckbox  = 0x00000003
	esAutoHScroll   = 0x0080
	esNumber        = 0x2000

	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	tpmRightButton = 0x0002

	smCYScreen = 1

	vkShift   = 0x10
	vkControl = 0x11
	vkAlt     = 0x12
	vkEscape  = 0x1B
	vkLWin    = 0x5B
	vkRWin    = 0x5C
)

// notifyIconData 对应 Win32 的 NOTIFYICONDATAW
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
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

var (
	shell32 = syscall.NewLazyDLL("shell32.dll")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	pLoadIconW              = user32.NewProc("LoadIconW")
	pCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	pAppendMenuW            = user32.NewProc("AppendMenuW")
	pTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	pDestroyMenu            = user32.NewProc("DestroyMenu")
	pGetCursorPos           = user32.NewProc("GetCursorPos")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pIsWindowVisible        = user32.NewProc("IsWindowVisible")
	pIsDialogMessageW       = user32.NewProc("IsDialogMessageW")
	pGetDlgItem             = user32.NewProc("GetDlgItem")
	pGetWindowTextW         = user32.NewProc("GetWindowTextW")
	pRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	pRegisterHotKey         = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey       = user32.NewProc("UnregisterHotKey")
	pSetFocus               = user32.NewProc("SetFocus")
	pGetKeyState            = user32.NewProc("GetKeyState")
	pMapVirtualKeyW         = user32.NewProc("MapVirtualKeyW")

	trayHwnd          uintptr
	trayIconAdded     bool
	taskbarCreatedMsg uint32

	settingsHwnd            uintptr
	settingsClassRegistered bool

	hotkeyRegistered bool
	hotkeySuspended  bool
	hotkeyMod        uint32
	hotkeyVK         uint32

	// 设置窗口里的快捷键编辑状态（仅界面线程访问）
	pendingHotkeyMod uint32
	pendingHotkeyVK  uint32
	captureHotkey    bool
	hotkeyLabelHwnd  uintptr
	statusLabelHwnd  uintptr
)

func addTrayIcon(hwnd uintptr) {
	trayHwnd = hwnd

	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = trayIconID
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmAppTray

	icon, _, _ := pLoadIconW.Call(0, 32512) // IDI_APPLICATION
	nid.HIcon = icon

	tip, _ := syscall.UTF16FromString("三角洲行动 · 每日密码")
	copy(nid.SzTip[:], tip)

	pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	trayIconAdded = true
}

func removeTrayIcon() {
	if !trayIconAdded {
		return
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = trayHwnd
	nid.UID = trayIconID
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	trayIconAdded = false
}

func initTaskbarMessage() {
	r, _, _ := pRegisterWindowMessageW.Call(utf16Ptr("TaskbarCreated"))
	taskbarCreatedMsg = uint32(r)
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer pDestroyMenu.Call(menu)

	appendTrayMenu(menu, mfString, trayCmdSettings, "打开设置")
	if isOverlayVisible() {
		appendTrayMenu(menu, mfString, trayCmdToggle, "隐藏悬浮窗")
	} else {
		appendTrayMenu(menu, mfString, trayCmdToggle, "显示悬浮窗")
	}
	appendTrayMenu(menu, mfString, trayCmdConsole, "打开控制台")
	pAppendMenuW.Call(menu, mfSeparator, 0, 0)
	appendTrayMenu(menu, mfString, trayCmdExit, "退出")

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	// 先置前台，菜单才能正常响应点击和消失
	pSetForegroundWindow.Call(hwnd)
	pTrackPopupMenu.Call(menu, tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	pPostMessageW.Call(hwnd, 0, 0, 0)
}

func appendTrayMenu(menu uintptr, flags uintptr, id uintptr, text string) {
	pAppendMenuW.Call(menu, flags, id, utf16Ptr(text))
}

func isOverlayVisible() bool {
	r, _, _ := pIsWindowVisible.Call(mainHwnd)
	return r != 0
}

func toggleOverlay() {
	if isOverlayVisible() {
		pShowWindow.Call(mainHwnd, swHide)
	} else {
		pShowWindow.Call(mainHwnd, swShowNormal)
	}
}

// exitApp 关闭设置窗口与悬浮窗，消息循环随后退出
func exitApp() {
	if settingsHwnd != 0 {
		pDestroyWindow.Call(settingsHwnd)
	}
	pDestroyWindow.Call(mainHwnd)
}

// registerOverlayHotkey 重新注册全局快捷键，失败时恢复原快捷键
func registerOverlayHotkey(mods, vk uint32) bool {
	oldMod, oldVK := hotkeyMod, hotkeyVK
	oldRegistered := hotkeyRegistered

	hotkeySuspended = false
	if hotkeyRegistered {
		pUnregisterHotKey.Call(mainHwnd, hotkeyID)
		hotkeyRegistered = false
	}
	if vk == 0 {
		hotkeyMod, hotkeyVK = 0, 0
		return true
	}

	if r, _, _ := pRegisterHotKey.Call(mainHwnd, hotkeyID, uintptr(mods), uintptr(vk)); r == 0 {
		if oldRegistered {
			if r2, _, _ := pRegisterHotKey.Call(mainHwnd, hotkeyID, uintptr(oldMod), uintptr(oldVK)); r2 != 0 {
				hotkeyRegistered = true
			}
		}
		return false
	}
	hotkeyRegistered = true
	hotkeyMod, hotkeyVK = mods, vk
	return true
}

// suspendOverlayHotkey 暂时注销全局快捷键，避免录制新快捷键时冲突
func suspendOverlayHotkey() {
	if !hotkeyRegistered {
		return
	}
	pUnregisterHotKey.Call(mainHwnd, hotkeyID)
	hotkeyRegistered = false
	hotkeySuspended = true
}

// resumeOverlayHotkey 录制结束后恢复原来的全局快捷键
func resumeOverlayHotkey() {
	if !hotkeySuspended {
		return
	}
	hotkeySuspended = false
	if hotkeyVK == 0 {
		return
	}
	if r, _, _ := pRegisterHotKey.Call(mainHwnd, hotkeyID, uintptr(hotkeyMod), uintptr(hotkeyVK)); r != 0 {
		hotkeyRegistered = true
	}
}

func registerSettingsClass() {
	if settingsClassRegistered {
		return
	}
	hInst, _, _ := pGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("DeltaDailySecretSettings")
	cursor, _, _ := pLoadCursorW.Call(0, 32512)

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   syscall.NewCallback(settingsWndProc),
		HInstance:     hInst,
		HCursor:       cursor,
		HbrBackground: 6, // COLOR_WINDOW + 1
		LpszClassName: className,
	}
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		log.Printf("注册设置窗口类失败: %v", err)
		return
	}
	settingsClassRegistered = true
}

func openSettings() {
	if settingsHwnd != 0 {
		pShowWindow.Call(settingsHwnd, swShowNormal)
		pSetForegroundWindow.Call(settingsHwnd)
		return
	}

	w := px(432)
	h := px(410)
	screenW, _, _ := pGetSystemMetrics.Call(smCXScreen)
	screenH, _, _ := pGetSystemMetrics.Call(smCYScreen)
	x := (int32(screenW) - w) / 2
	y := (int32(screenH) - h) / 2

	hInst, _, _ := pGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("DeltaDailySecretSettings")
	title, _ := syscall.UTF16PtrFromString("设置 - 三角洲行动每日密码")

	hwnd, _, err := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(wsCaption|wsSysMenu),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		log.Printf("创建设置窗口失败: %v", err)
		return
	}
	settingsHwnd = hwnd

	pShowWindow.Call(hwnd, swShowNormal)
	pUpdateWindow.Call(hwnd)
}

func settingsWndProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case wmCreate:
		createSettingsControls(hwnd)
		return 0
	case wmCommand:
		switch wparam & 0xFFFF {
		case idOK:
			saveSettingsFromControls(hwnd)
			return 0
		case idCancel:
			pDestroyWindow.Call(hwnd)
			return 0
		case ctrlHotkeyButton:
			startHotkeyCapture()
			return 0
		}
		return 0
	case wmClose:
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		settingsHwnd = 0
		if captureHotkey {
			captureHotkey = false
			resumeOverlayHotkey()
		}
		hotkeyLabelHwnd = 0
		statusLabelHwnd = 0
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(message), wparam, lparam)
	return r
}

func createSettingsControls(hwnd uintptr) {
	pad := px(18)
	width := px(380)
	y := px(16)

	autostart := createControl(hwnd, "BUTTON", "开机自启（登录 Windows 后自动运行）", bsAutoCheckbox|wsTabStop, pad, y, width, px(24), ctrlAutostart)
	if autostartEnabled() {
		pSendMessageW.Call(autostart, bmSetCheck, bstChecked, 0)
	}
	y += px(36)

	createControl(hwnd, "STATIC", "显示/隐藏悬浮窗快捷键：", 0, pad, y, width, px(20), 0)
	y += px(24)

	cfg := currentSettings()
	pendingHotkeyMod = cfg.HotkeyMod
	pendingHotkeyVK = cfg.HotkeyVK

	hotkeyLabelHwnd = createControl(hwnd, "STATIC", "", wsBorder|ssCenterImage, pad, y, px(210), px(26), ctrlHotkeyLabel)
	updateHotkeyLabel()
	createControl(hwnd, "BUTTON", "更改", bsPushButton|wsTabStop, pad+px(220), y, px(80), px(26), ctrlHotkeyButton)
	y += px(40)

	createControl(hwnd, "STATIC", "随以下程序启动时显示悬浮窗：", 0, pad, y, width, px(20), 0)
	y += px(24)

	autoFollow := createControl(hwnd, "BUTTON", "自动识别三角洲行动主程序（推荐）", bsAutoCheckbox|wsTabStop, pad, y, width, px(24), ctrlAutoFollow)
	if cfg.AutoFollow {
		pSendMessageW.Call(autoFollow, bmSetCheck, bstChecked, 0)
	}
	y += px(32)

	edit := createControl(hwnd, "EDIT", "", wsBorder|wsTabStop|esAutoHScroll, pad, y, width, px(26), ctrlFollowExe)
	setControlText(edit, cfg.FollowExe)
	y += px(32)

	hint := createControl(hwnd, "STATIC", "自动识别失败时使用此进程名，默认：\r\nDeltaForceClient-Win64-Shipping.exe", 0, pad, y, width, px(34), 0)
	pSendMessageW.Call(hint, wmSetFont, fontSmall, 1)
	y += px(44)

	createControl(hwnd, "STATIC", "悬浮窗背景不透明度（20-100）：", 0, pad, y, width, px(20), 0)
	y += px(24)

	opacityEdit := createControl(hwnd, "EDIT", "", wsBorder|wsTabStop|esAutoHScroll|esNumber, pad, y, px(80), px(26), ctrlOpacity)
	setControlText(opacityEdit, strconv.Itoa(cfg.Opacity))
	y += px(38)

	createControl(hwnd, "BUTTON", "保存", bsDefPushButton|wsTabStop, pad+width-px(170), y, px(80), px(30), idOK)
	createControl(hwnd, "BUTTON", "取消", bsPushButton|wsTabStop, pad+width-px(80), y, px(80), px(30), idCancel)

	statusLabelHwnd = createControl(hwnd, "STATIC", "", 0, pad, y+px(42), width, px(20), ctrlStatus)
}

func createControl(parent uintptr, class, text string, style uintptr, x, y, w, h int32, id uintptr) uintptr {
	classPtr, _ := syscall.UTF16PtrFromString(class)
	textPtr, _ := syscall.UTF16PtrFromString(text)
	hInst, _, _ := pGetModuleHandleW.Call(0)

	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(classPtr)),
		uintptr(unsafe.Pointer(textPtr)),
		style|wsChild|wsVisible,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, id, hInst, 0,
	)
	pSendMessageW.Call(hwnd, wmSetFont, fontBody, 1)
	return hwnd
}

func setControlText(hwnd uintptr, text string) {
	pSendMessageW.Call(hwnd, wmSetText, 0, utf16Ptr(text))
}

func getControlText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func setStatus(text string) {
	if statusLabelHwnd != 0 {
		setControlText(statusLabelHwnd, text)
	}
}

func updateHotkeyLabel() {
	if hotkeyLabelHwnd == 0 {
		return
	}
	if pendingHotkeyVK == 0 {
		setControlText(hotkeyLabelHwnd, "未设置")
		return
	}
	setControlText(hotkeyLabelHwnd, formatHotkey(pendingHotkeyMod, pendingHotkeyVK))
}

func startHotkeyCapture() {
	captureHotkey = true
	suspendOverlayHotkey()
	setStatus("")
	setControlText(hotkeyLabelHwnd, "请按下快捷键…（Esc 取消）")
	pSetFocus.Call(settingsHwnd)
}

// handleHotkeyCapture 在消息循环里接管键盘消息，录制新的快捷键
func handleHotkeyCapture(message uint32, wparam uintptr) bool {
	switch message {
	case wmKeyUp, wmSysKeyUp, wmChar, wmSysChar:
		return true
	case wmKeyDown, wmSysKeyDown:
	default:
		return false
	}

	vk := uint32(wparam)
	if vk == vkEscape {
		captureHotkey = false
		resumeOverlayHotkey()
		setStatus("已取消快捷键设置")
		updateHotkeyLabel()
		return true
	}

	// 单独按下修饰键时继续等待
	switch vk {
	case vkShift, vkControl, vkAlt, vkLWin, vkRWin:
		return true
	}

	mods := uint32(0)
	if keyDown(vkControl) {
		mods |= modControl
	}
	if keyDown(vkAlt) {
		mods |= modAlt
	}
	if keyDown(vkShift) {
		mods |= modShift
	}

	if mods == 0 {
		setControlText(hotkeyLabelHwnd, "请按住 Ctrl / Alt / Shift 再按其他键")
		return true
	}

	pendingHotkeyMod, pendingHotkeyVK = mods, vk
	captureHotkey = false
	resumeOverlayHotkey()
	setStatus("")
	updateHotkeyLabel()
	return true
}

func keyDown(vk uint32) bool {
	r, _, _ := pGetKeyState.Call(uintptr(vk))
	return int16(r) < 0
}

func formatHotkey(mods, vk uint32) string {
	if vk == 0 {
		return "未设置"
	}
	var parts []string
	if mods&modControl != 0 {
		parts = append(parts, "Ctrl")
	}
	if mods&modAlt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&modShift != 0 {
		parts = append(parts, "Shift")
	}
	parts = append(parts, keyName(vk))
	return strings.Join(parts, "+")
}

func keyName(vk uint32) string {
	if vk >= 0x70 && vk <= 0x87 { // F1 ~ F24
		return fmt.Sprintf("F%d", vk-0x6F)
	}
	switch vk {
	case 0x20:
		return "Space"
	case 0x0D:
		return "Enter"
	case 0x09:
		return "Tab"
	case 0x25:
		return "Left"
	case 0x26:
		return "Up"
	case 0x27:
		return "Right"
	case 0x28:
		return "Down"
	case 0x24:
		return "Home"
	case 0x23:
		return "End"
	case 0x21:
		return "PgUp"
	case 0x22:
		return "PgDn"
	case 0x2D:
		return "Insert"
	case 0x2E:
		return "Delete"
	}

	if r, _, _ := pMapVirtualKeyW.Call(uintptr(vk), 2); r != 0 { // MAPVK_VK_TO_CHAR
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return string(rune(r))
		}
		if r >= 'a' && r <= 'z' {
			return strings.ToUpper(string(rune(r)))
		}
	}
	return fmt.Sprintf("VK%02X", vk)
}

func saveSettingsFromControls(hwnd uintptr) {
	cfg := currentSettings()

	// 若仍处于录制状态，先恢复原快捷键，再按新值重新注册
	if captureHotkey {
		captureHotkey = false
		resumeOverlayHotkey()
	}

	checked := false
	if chk, _, _ := pGetDlgItem.Call(hwnd, ctrlAutostart); chk != 0 {
		r, _, _ := pSendMessageW.Call(chk, bmGetCheck, 0, 0)
		checked = r == bstChecked
	}

	if !registerOverlayHotkey(pendingHotkeyMod, pendingHotkeyVK) {
		setStatus("快捷键注册失败，可能已被其他程序占用，请换一个组合")
		return
	}

	if err := setAutostart(checked); err != nil {
		setStatus("设置开机自启失败：" + err.Error())
		return
	}

	follow := ""
	if edit, _, _ := pGetDlgItem.Call(hwnd, ctrlFollowExe); edit != 0 {
		follow = getControlText(edit)
	}

	autoFollow := false
	if chk, _, _ := pGetDlgItem.Call(hwnd, ctrlAutoFollow); chk != 0 {
		r, _, _ := pSendMessageW.Call(chk, bmGetCheck, 0, 0)
		autoFollow = r == bstChecked
	}

	opacity := cfg.Opacity
	if edit, _, _ := pGetDlgItem.Call(hwnd, ctrlOpacity); edit != 0 {
		if txt := strings.TrimSpace(getControlText(edit)); txt != "" {
			n, err := strconv.Atoi(txt)
			if err != nil {
				setStatus("不透明度需要填写 20-100 的数字")
				return
			}
			opacity = clampOpacity(n)
		}
	}

	cfg.HotkeyMod = pendingHotkeyMod
	cfg.HotkeyVK = pendingHotkeyVK
	cfg.FollowExe = strings.TrimSpace(follow)
	cfg.AutoFollow = autoFollow
	cfg.Opacity = opacity
	if err := saveAppSettings(cfg); err != nil {
		setStatus("保存设置失败：" + err.Error())
		return
	}

	applyOverlayOpacity(cfg.Opacity)
	applyFollowNow(mainHwnd)
	pDestroyWindow.Call(hwnd)
}
