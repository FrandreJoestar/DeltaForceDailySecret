//go:build windows

package main

import (
	"log"
	"path/filepath"
	"syscall"
	"unsafe"
)

// openQRCode 用系统默认图片查看器打开二维码，方便手机扫码。
//
// 这里用 ShellExecuteW 而不是原来的 rundll32 url.dll,FileProtocolHandler：
// rundll32 是被大量恶意软件滥用的系统代理程序，杀软对"程序启动 rundll32
// 打开一个刚写出来的文件"这类行为比较敏感，换成 ShellExecute 的 open 动词
// 既能少一份误报风险，也更容易被用户机上的默认看图程序正确处理。
func openQRCode(qrPath string) {
	absPath, err := filepath.Abs(qrPath)
	if err != nil {
		log.Printf("解析二维码路径失败: %v", err)
		return
	}

	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return
	}
	file, err := syscall.UTF16PtrFromString(absPath)
	if err != nil {
		return
	}

	r, _, _ := pShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0, 0, uintptr(swShowNormal),
	)
	if r <= shellExecuteSuccess {
		log.Printf("打开二维码图片失败（返回码 %d），请手动打开 %s", r, absPath)
	}
}
