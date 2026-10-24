//go:build windows

package main

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	overlayTimerID = 1

	wsPopup        = 0x80000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExLayered    = 0x00080000
	wsExNoActivate = 0x08000000

	wmDestroy    = 0x0002
	wmPaint      = 0x000F
	wmClose      = 0x0010
	wmEraseBkgnd = 0x0014
	wmTimer      = 0x0113
	wmNcHitTest  = 0x0084
	wmLButtonUp  = 0x0202
	wmRButtonUp  = 0x0205
	wmAppUpdate  = 0x8001

	htCaption  = 2
	swShow     = 5
	smCXScreen = 0

	fwBold = 700

	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	// UpdateLayeredWindow
	ulwAlpha      = 0x00000002
	acSrcOver     = 0
	acSrcAlpha    = 1
	dibRGBColors  = 0
	biRGB         = 0
	pixelFormat32 = 0x000E200B // PixelFormat32bppPARGB

	textRenderingHintAntiAlias = 4
	stringFormatFlagsNoWrap    = 0x00001000
	flushIntentionSync         = 1
)

// 颜色（COLORREF = R | G<<8 | B<<16）
const (
	clrCard   = 30 | 35<<8 | 46<<16
	clrTitle  = 255 | 197<<8 | 61<<16
	clrText   = 232 | 237<<8 | 245<<16
	clrSub    = 138 | 148<<8 | 166<<16
	clrSecret = 255 | 214<<8 | 102<<16
	clrFooter = 95 | 107<<8 | 127<<16
	clrError  = 255 | 120<<8 | 120<<16
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdiplus  = syscall.NewLazyDLL("gdiplus.dll")

	pRegisterClassExW    = user32.NewProc("RegisterClassExW")
	pCreateWindowExW     = user32.NewProc("CreateWindowExW")
	pDefWindowProcW      = user32.NewProc("DefWindowProcW")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pShowWindow          = user32.NewProc("ShowWindow")
	pUpdateWindow        = user32.NewProc("UpdateWindow")
	pGetMessageW         = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessageW    = user32.NewProc("DispatchMessageW")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pPostMessageW        = user32.NewProc("PostMessageW")
	pBeginPaint          = user32.NewProc("BeginPaint")
	pEndPaint            = user32.NewProc("EndPaint")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pSetTimer            = user32.NewProc("SetTimer")
	pKillTimer           = user32.NewProc("KillTimer")
	pSetWindowPos        = user32.NewProc("SetWindowPos")
	pSendMessageW        = user32.NewProc("SendMessageW")
	pLoadCursorW         = user32.NewProc("LoadCursorW")
	pSetWindowRgn        = user32.NewProc("SetWindowRgn")
	pSetProcessDPIAware  = user32.NewProc("SetProcessDPIAware")
	pGetDpiForSystem     = user32.NewProc("GetDpiForSystem")
	pUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	pGetDC               = user32.NewProc("GetDC")
	pReleaseDC           = user32.NewProc("ReleaseDC")
	pGetWindowRect       = user32.NewProc("GetWindowRect")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pCreateFontW        = gdi32.NewProc("CreateFontW")
	pCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")

	pGdiplusStartup               = gdiplus.NewProc("GdiplusStartup")
	pGdiplusShutdown              = gdiplus.NewProc("GdiplusShutdown")
	pGdipCreateBitmapFromScan0    = gdiplus.NewProc("GdipCreateBitmapFromScan0")
	pGdipGetImageGraphicsContext  = gdiplus.NewProc("GdipGetImageGraphicsContext")
	pGdipGraphicsClear            = gdiplus.NewProc("GdipGraphicsClear")
	pGdipSetTextRenderingHint     = gdiplus.NewProc("GdipSetTextRenderingHint")
	pGdipCreateFontFromLogfontW   = gdiplus.NewProc("GdipCreateFontFromLogfontW")
	pGdipCreateSolidFill          = gdiplus.NewProc("GdipCreateSolidFill")
	pGdipDrawString               = gdiplus.NewProc("GdipDrawString")
	pGdipCreateStringFormat       = gdiplus.NewProc("GdipCreateStringFormat")
	pGdipSetStringFormatAlign     = gdiplus.NewProc("GdipSetStringFormatAlign")
	pGdipSetStringFormatLineAlign = gdiplus.NewProc("GdipSetStringFormatLineAlign")
	pGdipFlush                    = gdiplus.NewProc("GdipFlush")
	pGdipDeleteGraphics           = gdiplus.NewProc("GdipDeleteGraphics")
	pGdipDisposeImage             = gdiplus.NewProc("GdipDisposeImage")
	pGdipDeleteFont               = gdiplus.NewProc("GdipDeleteFont")
	pGdipDeleteBrush              = gdiplus.NewProc("GdipDeleteBrush")
	pGdipDeleteStringFormat       = gdiplus.NewProc("GdipDeleteStringFormat")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

	mainHwnd uintptr

	dpiScale  = 1.0
	winWidth  int32
	winHeight int32

	// 设置窗口控件用的 GDI 字体
	fontBody  uintptr
	fontSmall uintptr

	// 悬浮窗绘制使用 GDI+，逐像素 alpha
	gpReady       bool
	gpToken       uintptr
	gpFontTitle   uintptr
	gpFontBody    uintptr
	gpFontSmall   uintptr
	gpFontSecret  uintptr
	gpBrushTitle  uintptr
	gpBrushText   uintptr
	gpBrushSub    uintptr
	gpBrushSecret uintptr
	gpBrushFooter uintptr
	gpBrushError  uintptr
	gpFmtLeft     uintptr
	gpFmtRight    uintptr

	overlayOpacity  = defaultOpacity
	overlayDIB      uintptr
	overlayMemDC    uintptr
	overlayBits     []byte
	overlaySurfaceW int32
	overlaySurfaceH int32
)

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type rect struct {
	Left, Top, Right, Bottom int32
}

type point struct {
	X, Y int32
}

type size struct {
	CX, CY int32
}

type rectF struct {
	X, Y, Width, Height float32
}

type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type paintStruct struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     rect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type logFont struct {
	Height         int32
	Width          int32
	Escapement     int32
	Orientation    int32
	Weight         int32
	Italic         byte
	Underline      byte
	StrikeOut      byte
	CharSet        byte
	OutPrecision   byte
	ClipPrecision  byte
	Quality        byte
	PitchAndFamily byte
	FaceName       [32]uint16
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type gdiplusStartupInput struct {
	GdiplusVersion           uint32
	DebugEventCallback       uintptr
	SuppressBackgroundThread int32
	SuppressExternalCodecs   int32
}

// overlayState 是悬浮窗展示的数据，由后台刷新协程写入
type overlayState struct {
	loading bool
	secrets []Secret
	errMsg  string
	updated time.Time
}

var (
	stateMu  sync.Mutex
	state    = overlayState{loading: true}
	fetchFn  func() ([]Secret, error)
	fetching atomic.Bool
)

// runOverlay 创建右上角悬浮窗、托盘和设置窗口，并进入消息循环
func runOverlay(fetch func() ([]Secret, error)) error {
	// 窗口消息队列绑定创建它的线程，必须锁定当前 OS 线程，
	// 否则 goroutine 被调度到其他线程后消息无人处理，窗口会被系统判定为无响应。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fetchFn = fetch
	initDPIAwareness()
	loadAppSettings()
	initTaskbarMessage()
	registerSettingsClass()

	hInst, _, _ := pGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("DeltaDailySecretOverlay")
	cursor, _, _ := pLoadCursorW.Call(0, 32512) // IDC_ARROW

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   syscall.NewCallback(overlayWndProc),
		HInstance:     hInst,
		HCursor:       cursor,
		LpszClassName: className,
	}
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("注册窗口类失败: %v", err)
	}

	winWidth = px(246)
	winHeight = overlayHeight(1)
	screenW, _, _ := pGetSystemMetrics.Call(smCXScreen)
	x := int32(screenW) - winWidth - px(20)

	title, _ := syscall.UTF16PtrFromString("三角洲行动每日密码")
	hwnd, _, err := pCreateWindowExW.Call(
		uintptr(wsExTopmost|wsExToolWindow|wsExLayered|wsExNoActivate),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(wsPopup),
		uintptr(x), uintptr(px(20)),
		uintptr(winWidth), uintptr(winHeight),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("创建悬浮窗失败: %v", err)
	}
	mainHwnd = hwnd

	cfg := currentSettings()
	overlayOpacity = clampOpacity(cfg.Opacity)

	applyWindowRegion(hwnd, winWidth, winHeight)
	initFonts()
	initOverlayGraphics()
	renderOverlay()

	addTrayIcon(hwnd)

	if cfg.HotkeyVK != 0 && !registerOverlayHotkey(cfg.HotkeyMod, cfg.HotkeyVK) {
		log.Printf("全局快捷键注册失败，可能已被其他程序占用")
	}

	// 配置了跟随程序时，只有目标程序在运行时才显示悬浮窗
	targets := followTargets()
	visible := true
	if len(targets) > 0 {
		visible = anyProcessRunning(targets)
	}
	if visible {
		pShowWindow.Call(hwnd, swShow)
		pUpdateWindow.Call(hwnd)
	}

	startFollowWatcher(hwnd)
	triggerFetch()

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		// 快捷键录制期间接管键盘消息
		if captureHotkey && settingsHwnd != 0 {
			if m.Message == wmSysCommand {
				continue
			}
			if m.Hwnd == settingsHwnd && handleHotkeyCapture(m.Message, m.WParam) {
				continue
			}
		}
		// 设置窗口需要 Tab 键导航
		if settingsHwnd != 0 {
			if handled, _, _ := pIsDialogMessageW.Call(settingsHwnd, uintptr(unsafe.Pointer(&m))); handled != 0 {
				continue
			}
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func overlayWndProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case wmPaint:
		// 画面由 UpdateLayeredWindow 提供，这里只校验重绘区域
		var ps paintStruct
		pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmEraseBkgnd:
		return 1
	case wmNcHitTest:
		// 整块区域都当作标题栏，由系统处理拖动，避免自定义拖拽与点击冲突
		return htCaption
	case wmTimer:
		triggerFetch()
		return 0
	case wmHotkey:
		toggleOverlay()
		return 0
	case wmAppTray:
		switch lparam & 0xFFFF {
		case wmLButtonUp:
			toggleOverlay()
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
		return 0
	case wmAppFollow:
		if wparam != 0 {
			pShowWindow.Call(hwnd, swShowNormal)
		} else {
			pShowWindow.Call(hwnd, swHide)
		}
		return 0
	case wmCommand:
		switch wparam & 0xFFFF {
		case trayCmdSettings:
			// 投递到消息循环再创建窗口，避免在托盘菜单的模态循环里创建
			pPostMessageW.Call(hwnd, wmAppSettings, 0, 0)
		case trayCmdToggle:
			toggleOverlay()
		case trayCmdConsole:
			openConsole()
		case trayCmdExit:
			exitApp()
		}
		return 0
	case wmAppSettings:
		openSettings()
		return 0
	case wmAppUpdate:
		layoutOverlay(hwnd)
		scheduleNextRefresh(hwnd)
		return 0
	case wmClose:
		// 悬浮窗不响应关闭，退出请使用托盘菜单
		return 0
	case wmDestroy:
		removeTrayIcon()
		pKillTimer.Call(hwnd, overlayTimerID)
		releaseOverlaySurface()
		releaseOverlayGraphics()
		releaseFonts()
		pPostQuitMessage.Call(0)
		return 0
	}

	// 资源管理器重启后重新添加托盘图标
	if taskbarCreatedMsg != 0 && message == taskbarCreatedMsg {
		addTrayIcon(hwnd)
		return 0
	}

	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(message), wparam, lparam)
	return r
}

// triggerFetch 后台拉取数据，完成后通知界面线程重绘
func triggerFetch() {
	if fetchFn == nil || !fetching.CompareAndSwap(false, true) {
		return
	}

	stateMu.Lock()
	if state.updated.IsZero() {
		state.loading = true
	}
	stateMu.Unlock()
	renderOverlay()

	go func() {
		defer fetching.Store(false)

		secrets, err := fetchFn()

		stateMu.Lock()
		state.loading = false
		if err != nil {
			state.errMsg = err.Error()
		} else {
			state.errMsg = ""
			state.secrets = secrets
			state.updated = time.Now()
		}
		stateMu.Unlock()

		if err != nil {
			log.Printf("刷新每日密码失败: %v", err)
		}
		pPostMessageW.Call(mainHwnd, wmAppUpdate, 0, 0)
	}()
}

// scheduleNextRefresh 成功时等到次日 00:05，失败时 30 分钟后重试
func scheduleNextRefresh(hwnd uintptr) {
	stateMu.Lock()
	failed := state.errMsg != ""
	stateMu.Unlock()

	delay := 30 * time.Minute
	if !failed {
		delay = time.Until(nextDailyRefresh())
	}
	ms := uint32(delay / time.Millisecond)
	if ms < 60_000 {
		ms = 60_000
	}
	pKillTimer.Call(hwnd, overlayTimerID)
	pSetTimer.Call(hwnd, overlayTimerID, uintptr(ms), 0)
}

func nextDailyRefresh() time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 5, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func layoutOverlay(hwnd uintptr) {
	stateMu.Lock()
	rows := len(state.secrets)
	stateMu.Unlock()

	h := overlayHeight(rows)
	if h != winHeight {
		winHeight = h
		screenW, _, _ := pGetSystemMetrics.Call(smCXScreen)
		x := int32(screenW) - winWidth - px(20)
		pSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(px(20)), uintptr(winWidth), uintptr(h), swpNoZOrder|swpNoActivate)
		applyWindowRegion(hwnd, winWidth, h)
	}
	renderOverlay()
}

func overlayHeight(rows int) int32 {
	if rows < 1 {
		rows = 1
	}
	return px(12) + px(24) + px(24) + int32(rows)*px(27) + px(32)
}

func applyWindowRegion(hwnd uintptr, w, h int32) {
	rgn, _, _ := pCreateRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(h+1), uintptr(px(14)), uintptr(px(14)))
	if rgn != 0 {
		pSetWindowRgn.Call(hwnd, rgn, 1)
	}
}

// applyOverlayOpacity 设置背景不透明度（字体保持不透明）
func applyOverlayOpacity(opacity int) {
	overlayOpacity = clampOpacity(opacity)
	if mainHwnd != 0 {
		renderOverlay()
	}
}

// ---- 逐像素 alpha 绘制 ----

func initOverlayGraphics() {
	input := gdiplusStartupInput{GdiplusVersion: 1}
	if r, _, _ := pGdiplusStartup.Call(uintptr(unsafe.Pointer(&gpToken)), uintptr(unsafe.Pointer(&input)), 0); r != 0 {
		log.Printf("GDI+ 初始化失败")
		return
	}

	screen, _, _ := pGetDC.Call(0)
	gpFontTitle = createGpFont(screen, -px(16), fwBold)
	gpFontBody = createGpFont(screen, -px(14), 400)
	gpFontSmall = createGpFont(screen, -px(12), 400)
	gpFontSecret = createGpFont(screen, -px(16), fwBold)
	pReleaseDC.Call(0, screen)

	if gpFontTitle == 0 || gpFontBody == 0 || gpFontSmall == 0 || gpFontSecret == 0 {
		log.Printf("GDI+ 字体创建失败")
		return
	}

	gpBrushTitle = createGpBrush(clrTitle)
	gpBrushText = createGpBrush(clrText)
	gpBrushSub = createGpBrush(clrSub)
	gpBrushSecret = createGpBrush(clrSecret)
	gpBrushFooter = createGpBrush(clrFooter)
	gpBrushError = createGpBrush(clrError)
	gpFmtLeft = createGpFormat(0)
	gpFmtRight = createGpFormat(2)
	gpReady = true
}

func createGpFont(hdc uintptr, height int32, weight int32) uintptr {
	var lf logFont
	lf.Height = height
	lf.Weight = weight
	lf.CharSet = 1
	lf.Quality = 5
	face, _ := syscall.UTF16FromString("Microsoft YaHei UI")
	copy(lf.FaceName[:], face)

	var font uintptr
	if r, _, _ := pGdipCreateFontFromLogfontW.Call(hdc, uintptr(unsafe.Pointer(&lf)), uintptr(unsafe.Pointer(&font))); r != 0 {
		return 0
	}
	return font
}

// createGpBrush 用 COLORREF 颜色创建 GDI+ 实心画刷（完全不透明）
func createGpBrush(color uint32) uintptr {
	r := (color >> 0) & 0xFF
	g := (color >> 8) & 0xFF
	b := (color >> 16) & 0xFF
	argb := uint32(0xFF000000) | r<<16 | g<<8 | b
	var brush uintptr
	if r2, _, _ := pGdipCreateSolidFill.Call(uintptr(argb), uintptr(unsafe.Pointer(&brush))); r2 != 0 {
		return 0
	}
	return brush
}

func createGpFormat(align int32) uintptr {
	var format uintptr
	if r, _, _ := pGdipCreateStringFormat.Call(stringFormatFlagsNoWrap, 0, uintptr(unsafe.Pointer(&format))); r != 0 {
		return 0
	}
	pGdipSetStringFormatAlign.Call(format, uintptr(align))
	pGdipSetStringFormatLineAlign.Call(format, 1) // 垂直居中
	return format
}

func renderOverlay() {
	if mainHwnd == 0 || !gpReady {
		return
	}
	w, h := winWidth, winHeight
	if w <= 0 || h <= 0 {
		return
	}
	ensureOverlaySurface(w, h)
	if overlayMemDC == 0 || len(overlayBits) == 0 {
		return
	}

	fillCardBackground(overlayBits, overlayOpacity)

	stateMu.Lock()
	loading := state.loading
	secrets := append([]Secret(nil), state.secrets...)
	errMsg := state.errMsg
	updated := state.updated
	stateMu.Unlock()

	textBits := make([]byte, len(overlayBits))
	var bmp, gfx uintptr
	if r, _, _ := pGdipCreateBitmapFromScan0.Call(
		uintptr(w), uintptr(h), uintptr(w*4), pixelFormat32,
		uintptr(unsafe.Pointer(&textBits[0])), uintptr(unsafe.Pointer(&bmp)),
	); r == 0 {
		if r2, _, _ := pGdipGetImageGraphicsContext.Call(bmp, uintptr(unsafe.Pointer(&gfx))); r2 == 0 && gfx != 0 {
			pGdipSetTextRenderingHint.Call(gfx, textRenderingHintAntiAlias)
			pGdipGraphicsClear.Call(gfx, 0)
			drawOverlayTexts(gfx, w, h, loading, secrets, errMsg, updated)
			pGdipFlush.Call(gfx, flushIntentionSync)
			pGdipDeleteGraphics.Call(gfx)
		}
		pGdipDisposeImage.Call(bmp)
	}

	compositeText(overlayBits, textBits)
	updateLayeredWindow(mainHwnd, w, h)
}

func drawOverlayTexts(gfx uintptr, w, h int32, loading bool, secrets []Secret, errMsg string, updated time.Time) {
	pad := px(16)
	rowH := px(27)
	y := px(12)
	contentW := w - 2*pad

	gpDraw(gfx, "三角洲行动 · 每日密码", pad, y, contentW, px(22), gpFontTitle, gpBrushTitle, gpFmtLeft)
	y += px(24)

	subtitle := "正在获取…"
	if !updated.IsZero() {
		subtitle = updated.Format("2006-01-02") + " · 每日自动更新"
	}
	gpDraw(gfx, subtitle, pad, y, contentW, px(16), gpFontSmall, gpBrushSub, gpFmtLeft)
	y += px(24)

	switch {
	case len(secrets) > 0:
		for _, s := range secrets {
			gpDraw(gfx, s.MapName, pad, y, contentW, rowH, gpFontBody, gpBrushText, gpFmtLeft)
			gpDraw(gfx, s.Secret, pad, y, contentW, rowH, gpFontSecret, gpBrushSecret, gpFmtRight)
			y += rowH
		}
	case loading:
		gpDraw(gfx, "正在获取每日密码…", pad, y, contentW, rowH, gpFontBody, gpBrushSub, gpFmtLeft)
	case errMsg != "":
		gpDraw(gfx, "获取失败，稍后自动重试", pad, y, contentW, rowH, gpFontBody, gpBrushError, gpFmtLeft)
	}

	gpDraw(gfx, "托盘图标设置 · 按住拖动", pad, h-px(24), contentW, px(14), gpFontSmall, gpBrushFooter, gpFmtRight)
}

func gpDraw(gfx uintptr, text string, x, y, w, h int32, font, brush, format uintptr) {
	if gfx == 0 || font == 0 || brush == 0 || format == 0 {
		return
	}
	ptr, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	r := rectF{float32(x), float32(y), float32(w), float32(h)}
	pGdipDrawString.Call(gfx, uintptr(unsafe.Pointer(ptr)), ^uintptr(0), font,
		uintptr(unsafe.Pointer(&r)), format, brush)
}

func ensureOverlaySurface(w, h int32) {
	if overlayMemDC != 0 && overlaySurfaceW == w && overlaySurfaceH == h {
		return
	}
	releaseOverlaySurface()

	screen, _, _ := pGetDC.Call(0)
	if screen == 0 {
		return
	}
	defer pReleaseDC.Call(0, screen)

	bmi := bitmapInfo{}
	bmi.Header.Size = uint32(unsafe.Sizeof(bitmapInfoHeader{}))
	bmi.Header.Width = w
	bmi.Header.Height = -h // 自上而下
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 32
	bmi.Header.Compression = biRGB

	var bits unsafe.Pointer
	dib, _, _ := pCreateDIBSection.Call(screen, uintptr(unsafe.Pointer(&bmi)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 {
		return
	}
	memDC, _, _ := pCreateCompatibleDC.Call(screen)
	if memDC == 0 {
		pDeleteObject.Call(dib)
		return
	}
	pSelectObject.Call(memDC, dib)

	overlayDIB = dib
	overlayMemDC = memDC
	overlaySurfaceW = w
	overlaySurfaceH = h
	overlayBits = unsafe.Slice((*byte)(bits), int(w)*int(h)*4)
}

func releaseOverlaySurface() {
	if overlayMemDC != 0 {
		pDeleteDC.Call(overlayMemDC)
		overlayMemDC = 0
	}
	if overlayDIB != 0 {
		pDeleteObject.Call(overlayDIB)
		overlayDIB = 0
	}
	overlayBits = nil
	overlaySurfaceW, overlaySurfaceH = 0, 0
}

func releaseOverlayGraphics() {
	for _, f := range []uintptr{gpFontTitle, gpFontBody, gpFontSmall, gpFontSecret} {
		if f != 0 {
			pGdipDeleteFont.Call(f)
		}
	}
	for _, b := range []uintptr{gpBrushTitle, gpBrushText, gpBrushSub, gpBrushSecret, gpBrushFooter, gpBrushError} {
		if b != 0 {
			pGdipDeleteBrush.Call(b)
		}
	}
	for _, f := range []uintptr{gpFmtLeft, gpFmtRight} {
		if f != 0 {
			pGdipDeleteStringFormat.Call(f)
		}
	}
	if gpToken != 0 {
		pGdiplusShutdown.Call(gpToken)
		gpToken = 0
	}
	gpReady = false
}

// fillCardBackground 用带 alpha 的卡片颜色填充背景（预乘 alpha）
func fillCardBackground(bits []byte, opacity int) {
	a := uint32(opacity*255/100) & 0xFF
	if a < 1 {
		a = 1
	}
	r := uint32((clrCard >> 0) & 0xFF)
	g := uint32((clrCard >> 8) & 0xFF)
	b := uint32((clrCard >> 16) & 0xFF)

	pb := byte(b * a / 255)
	pg := byte(g * a / 255)
	pr := byte(r * a / 255)
	pa := byte(a)

	for i := 0; i < len(bits); i += 4 {
		bits[i] = pb
		bits[i+1] = pg
		bits[i+2] = pr
		bits[i+3] = pa
	}
}

// compositeText 把文字层（预乘 alpha）合成到背景上，文字保持完全不透明
func compositeText(dst, src []byte) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	for i := 0; i+3 < n; i += 4 {
		a := uint32(src[i+3])
		if a == 0 {
			continue
		}
		inv := 255 - a
		dst[i] = src[i] + byte(uint32(dst[i])*inv/255)
		dst[i+1] = src[i+1] + byte(uint32(dst[i+1])*inv/255)
		dst[i+2] = src[i+2] + byte(uint32(dst[i+2])*inv/255)
		dst[i+3] = src[i+3] + byte(uint32(dst[i+3])*inv/255)
	}
}

func updateLayeredWindow(hwnd uintptr, w, h int32) {
	screen, _, _ := pGetDC.Call(0)
	if screen == 0 {
		return
	}
	defer pReleaseDC.Call(0, screen)

	var wr rect
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))

	dst := point{X: wr.Left, Y: wr.Top}
	src := point{}
	sz := size{CX: w, CY: h}
	blend := blendFunction{BlendOp: acSrcOver, BlendFlags: 0, SourceConstantAlpha: 255, AlphaFormat: acSrcAlpha}

	r, _, err := pUpdateLayeredWindow.Call(hwnd, screen,
		uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&sz)),
		overlayMemDC, uintptr(unsafe.Pointer(&src)),
		0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
	if r == 0 {
		log.Printf("UpdateLayeredWindow 失败: %v", err)
	}
}

func initDPIAwareness() {
	if err := pSetProcessDPIAware.Find(); err == nil {
		pSetProcessDPIAware.Call()
	}
	if err := pGetDpiForSystem.Find(); err == nil {
		if dpi, _, _ := pGetDpiForSystem.Call(); dpi > 0 {
			dpiScale = float64(dpi) / 96
		}
	}
}

func px(v int32) int32 {
	return int32(float64(v)*dpiScale + 0.5)
}

// initFonts 创建设置窗口控件使用的 GDI 字体
func initFonts() {
	fontBody = createOverlayFont(-px(14), 400)
	fontSmall = createOverlayFont(-px(12), 400)
}

func createOverlayFont(height int32, weight int32) uintptr {
	face, _ := syscall.UTF16PtrFromString("Microsoft YaHei UI")
	f, _, _ := pCreateFontW.Call(
		uintptr(height), 0, 0, 0, uintptr(weight),
		0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(face)),
	)
	return f
}

func releaseFonts() {
	for _, f := range []uintptr{fontBody, fontSmall} {
		if f != 0 {
			pDeleteObject.Call(f)
		}
	}
}
