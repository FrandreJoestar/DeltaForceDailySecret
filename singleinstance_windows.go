//go:build windows

package main

import (
	"log"
	"syscall"
	"unsafe"
)

const (
	// 命名互斥体；Local\ 前缀表示每个登录会话一个实例（多用户 / 远程桌面互不影响）
	instanceMutexName = `Local\DeltaForceDailySecret.SingleInstance`

	errnoAlreadyExists = syscall.Errno(183) // ERROR_ALREADY_EXISTS

	// 第二次启动时投递给已有实例，用于把悬浮窗唤出来
	wmAppShowMe = 0x8006
)

var (
	pCreateMutexW = kernel32.NewProc("CreateMutexW")
	pFindWindowW  = user32.NewProc("FindWindowW")
	pSetLastError = kernel32.NewProc("SetLastError")

	// instanceMutex 持有到进程退出；系统会自动释放
	instanceMutex uintptr
)

// acquireSingleInstance 尝试取得单实例互斥体。
// 返回 false 表示已有实例在运行：此时会唤醒它的悬浮窗，调用方应当直接退出。
func acquireSingleInstance() bool {
	name, err := syscall.UTF16PtrFromString(instanceMutexName)
	if err != nil {
		return true
	}

	// CreateMutexW 成功创建新互斥体时不会重置 last error，
	// 必须先清零，否则可能读到上一次调用残留的 183
	pSetLastError.Call(0)
	h, _, callErr := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		// 拿不到互斥体时宁可允许启动，也不要因为系统异常让程序打不开
		log.Printf("创建单实例互斥体失败（%v），跳过单实例检查", callErr)
		return true
	}
	if callErr == errnoAlreadyExists {
		pCloseHandle.Call(h)
		log.Printf("程序已在运行，唤出已有悬浮窗")
		notifyRunningInstance()
		return false
	}

	instanceMutex = h
	return true
}

// notifyRunningInstance 找到已有实例的悬浮窗，让它显示出来
func notifyRunningInstance() {
	className, err := syscall.UTF16PtrFromString(overlayClassName)
	if err != nil {
		return
	}
	hwnd, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
	if hwnd == 0 {
		log.Printf("已定位到运行中的实例，但暂未找到悬浮窗（可能仍在启动中）")
		return
	}
	pPostMessageW.Call(hwnd, wmAppShowMe, 0, 0)
}
