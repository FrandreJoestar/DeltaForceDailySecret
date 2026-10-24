//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	hkeyCurrentUser = 0x80000001
	keyQueryValue   = 0x0001
	keySetValue     = 0x0002
	regSZ           = 1

	modAlt     = 0x0001
	modControl = 0x0002
	modShift   = 0x0004

	defaultHotkeyMod = modControl | modAlt
	defaultHotkeyVK  = 0x44 // D

	defaultOpacity = 90
	minOpacity     = 20
	maxOpacity     = 100

	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "DeltaForceDailySecret"

	th32csSnapProcess = 0x00000002
)

// appSettings 是保存在 settings.json 中的配置
type appSettings struct {
	HotkeyMod  uint32 `json:"hotkey_mod"`
	HotkeyVK   uint32 `json:"hotkey_vk"`
	FollowExe  string `json:"follow_exe"`
	Opacity    int    `json:"opacity"`
	AutoFollow bool   `json:"auto_follow"`
}

var (
	appSettingsMu   sync.Mutex
	appSettingsPath string
	appCfg          = appSettings{HotkeyMod: defaultHotkeyMod, HotkeyVK: defaultHotkeyVK, Opacity: defaultOpacity, AutoFollow: true}
)

var (
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW  = advapi32.NewProc("RegDeleteValueW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")

	pCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	pProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	pProcess32NextW           = kernel32.NewProc("Process32NextW")
	pCloseHandle              = kernel32.NewProc("CloseHandle")
)

// processEntry32 对应 Win32 的 PROCESSENTRY32W
type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	CntThreads      uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

func settingsFilePath() string {
	if appSettingsPath != "" {
		return appSettingsPath
	}
	exe, err := os.Executable()
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(filepath.Dir(exe), "settings.json")
}

func loadAppSettings() {
	appSettingsPath = settingsFilePath()
	data, err := os.ReadFile(appSettingsPath)
	if err != nil {
		return
	}
	var raw struct {
		HotkeyMod  uint32 `json:"hotkey_mod"`
		HotkeyVK   uint32 `json:"hotkey_vk"`
		FollowExe  string `json:"follow_exe"`
		Opacity    int    `json:"opacity"`
		AutoFollow *bool  `json:"auto_follow"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	s := appSettings{
		HotkeyMod:  raw.HotkeyMod,
		HotkeyVK:   raw.HotkeyVK,
		FollowExe:  raw.FollowExe,
		Opacity:    raw.Opacity,
		AutoFollow: true, // 旧版配置没有该字段时默认开启自动识别
	}
	if raw.AutoFollow != nil {
		s.AutoFollow = *raw.AutoFollow
	}
	// 旧版配置文件没有 opacity 字段，使用默认值
	if s.Opacity == 0 {
		s.Opacity = defaultOpacity
	}
	s.Opacity = clampOpacity(s.Opacity)
	appSettingsMu.Lock()
	appCfg = s
	appSettingsMu.Unlock()
}

func saveAppSettings(s appSettings) error {
	s.Opacity = clampOpacity(s.Opacity)
	appSettingsMu.Lock()
	appCfg = s
	appSettingsMu.Unlock()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsFilePath(), data, 0600)
}

// clampOpacity 把不透明度限制在允许范围内
func clampOpacity(v int) int {
	if v < minOpacity {
		return minOpacity
	}
	if v > maxOpacity {
		return maxOpacity
	}
	return v
}

func currentSettings() appSettings {
	appSettingsMu.Lock()
	defer appSettingsMu.Unlock()
	return appCfg
}

func utf16Ptr(s string) uintptr {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return 0
	}
	return uintptr(unsafe.Pointer(p))
}

// autostartEnabled 查询注册表，判断是否已设置开机自启
func autostartEnabled() bool {
	var key syscall.Handle
	r, _, _ := pRegOpenKeyExW.Call(hkeyCurrentUser, utf16Ptr(runKeyPath), 0, keyQueryValue, uintptr(unsafe.Pointer(&key)))
	if r != 0 {
		return false
	}
	defer pRegCloseKey.Call(uintptr(key))

	buf := make([]uint16, 512)
	size := uint32(len(buf) * 2)
	var typ uint32
	r, _, _ = pRegQueryValueExW.Call(
		uintptr(key), utf16Ptr(runValueName), 0,
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	return r == 0
}

// setAutostart 写入或删除 HKCU Run 项
func setAutostart(enable bool) error {
	var key syscall.Handle
	r, _, _ := pRegOpenKeyExW.Call(hkeyCurrentUser, utf16Ptr(runKeyPath), 0, keySetValue, uintptr(unsafe.Pointer(&key)))
	if r != 0 {
		return fmt.Errorf("打开注册表失败（code %d）", r)
	}
	defer pRegCloseKey.Call(uintptr(key))

	if !enable {
		pRegDeleteValueW.Call(uintptr(key), utf16Ptr(runValueName))
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	command := `"` + exe + `"`
	value, err := syscall.UTF16FromString(command)
	if err != nil {
		return err
	}
	r, _, _ = pRegSetValueExW.Call(
		uintptr(key), utf16Ptr(runValueName), 0, regSZ,
		uintptr(unsafe.Pointer(&value[0])), uintptr(len(value)*2),
	)
	if r != 0 {
		return fmt.Errorf("写入注册表失败（code %d）", r)
	}
	return nil
}

// isProcessRunning 通过进程快照判断指定进程名是否在运行
func isProcessRunning(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	snap, _, _ := pCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == 0 || snap == ^uintptr(0) {
		return false
	}
	defer pCloseHandle.Call(snap)

	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	r, _, _ := pProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for r != 0 {
		if strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), name) {
			return true
		}
		r, _, _ = pProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return false
}

// startFollowWatcher 每 3 秒检查一次目标进程，状态变化时通知界面线程
func startFollowWatcher(hwnd uintptr) {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		lastKey := ""
		lastRunning := false
		for range ticker.C {
			targets := followTargets()
			if len(targets) == 0 {
				lastKey, lastRunning = "", false
				continue
			}
			key := strings.Join(targets, "|")
			running := anyProcessRunning(targets)
			if key != lastKey {
				lastKey, lastRunning = key, running
				pPostMessageW.Call(hwnd, wmAppFollow, boolToUintptr(running), 0)
				continue
			}
			if running != lastRunning {
				lastRunning = running
				pPostMessageW.Call(hwnd, wmAppFollow, boolToUintptr(running), 0)
			}
		}
	}()
}

// applyFollowNow 保存设置后立即按当前进程状态调整悬浮窗
func applyFollowNow(hwnd uintptr) {
	// 自动识别可能触发磁盘扫描，放到后台避免阻塞界面线程
	go func() {
		targets := followTargets()
		if len(targets) == 0 {
			return
		}
		pPostMessageW.Call(hwnd, wmAppFollow, boolToUintptr(anyProcessRunning(targets)), 0)
	}()
}

func boolToUintptr(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}
