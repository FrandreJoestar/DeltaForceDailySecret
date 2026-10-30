//go:build windows

package main

import (
	"fmt"
	"os"
)

const attachParentProcess = ^uintptr(0)

var (
	pAttachConsole    = kernel32.NewProc("AttachConsole")
	pAllocConsole     = kernel32.NewProc("AllocConsole")
	pGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	pSetConsoleTitleW = kernel32.NewProc("SetConsoleTitleW")

	consoleCreated bool
)

// setupConsole 从终端启动时复用父控制台；双击启动时保持无控制台窗口
func setupConsole() {
	if stdHandleValid(os.Stdout) || stdHandleValid(os.Stderr) {
		return
	}
	if r, _, _ := pAttachConsole.Call(attachParentProcess); r != 0 {
		bindConsole()
	}
}

func stdHandleValid(f *os.File) bool {
	if f == nil {
		return false
	}
	_, err := f.Stat()
	return err == nil
}

// ensureConsole 保证有可用的控制台（控制台模式使用）。
// 标准输出已经重定向到文件或管道时不要新建控制台：新建之后 bindConsole
// 会把 os.Stdout 换成 CONOUT$，用户重定向到文件的输出就跑到新窗口里去了。
func ensureConsole() {
	if stdHandleValid(os.Stdout) {
		return
	}
	if w, _, _ := pGetConsoleWindow.Call(); w != 0 {
		return
	}
	openConsole()
}

// openConsole 打开或唤出控制台窗口，用于查看运行日志
func openConsole() {
	if w, _, _ := pGetConsoleWindow.Call(); w != 0 {
		pShowWindow.Call(w, swShowNormal)
		pSetForegroundWindow.Call(w)
		fmt.Println("控制台已打开，可查看运行日志。")
		return
	}
	if r, _, _ := pAllocConsole.Call(); r == 0 {
		return
	}
	consoleCreated = true
	bindConsole()
	pSetConsoleTitleW.Call(utf16Ptr("三角洲行动 · 每日密码"))
	// 新建的控制台里没有历史输出，先把内存中保留的最近日志回放出来
	replayLogs()
	fmt.Println("控制台已打开，可查看运行日志。")
	logFollowDiagnostics()
}

// bindConsole 把标准输入输出重新绑定到当前控制台。
// 日志由 logSink 负责落盘，这里只是把它额外镜像到控制台，
// 因此不能再用 log.SetOutput 覆盖，否则文件日志会中断。
func bindConsole() {
	if out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
		logSinkInst.setConsole(out)
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = in
	}
}

// waitConsoleExit 控制台模式下若控制台是程序自己创建的，退出前等待回车，避免窗口一闪而过
func waitConsoleExit() {
	if !consoleCreated {
		return
	}
	fmt.Print("\n按回车键退出...")
	fmt.Scanln()
}
