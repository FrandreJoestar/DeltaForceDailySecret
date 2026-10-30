//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

// runDoctor 把进程识别与安装探测的全过程打印出来，用于在别人的机器上远程排障。
//
// 设计目标：让用户只用一条命令就能产出一份"能定位问题"的报告，
// 而不是靠反复猜"是不是没装对""是不是权限不够"。
func runDoctor() {
	// 诊断模式不走 runOverlay，这里显式加载配置，报告里的设置才是真实的
	loadAppSettings()

	log.Printf("================ 诊断信息 ================")

	if exe, err := os.Executable(); err == nil {
		log.Printf("程序路径：%s", exe)
	}
	log.Printf("管理员权限：%v", isElevated())
	ourRID, ourOK := currentIntegrityRID()
	if ourOK {
		log.Printf("进程完整性级别：%s", integrityName(ourRID))
	} else {
		log.Printf("进程完整性级别：未知")
	}
	if pid := findProcessID("explorer.exe"); pid != 0 {
		exRID, exOK := processIntegrityRID(pid)
		switch {
		case !exOK:
			log.Printf("explorer.exe 完整性级别：读不到（PID %d）", pid)
			log.Printf("  → 读不到通常意味着资源管理器以**更高权限**运行；")
			log.Printf("    这时它收不到本进程发去的托盘消息（UIPI 拦截），托盘图标就会添加失败")
		default:
			log.Printf("explorer.exe 完整性级别：%s（PID %d）", integrityName(exRID), pid)
			if ourOK && exRID != ourRID {
				log.Printf("  → 两边完整性级别不一致，托盘图标会被 UIPI 拦截")
			}
		}
	} else {
		log.Printf("没有找到 explorer.exe（任务栏没在运行，或进程列表不可见）")
	}
	if parent := parentProcessName(); parent != "" {
		log.Printf("父进程：%s（子进程继承父进程的完整性级别）", parent)
	}
	if tokenRestricted() {
		log.Printf("令牌受限：是 —— 进程由沙盒/受限启动器创建，写用户目录和加托盘图标都会被拒绝")
	} else {
		log.Printf("令牌受限：否")
	}
	log.Printf("数据目录：%s", appDataDir())
	log.Printf("日志目录：%s", logDir())
	log.Printf("配置文件：%s（%s）", settingsFilePath(), fileState(settingsFilePath()))
	log.Printf("凭据文件：%s（%s）", resolveDataFile("credentials.json"), fileState(resolveDataFile("credentials.json")))

	cfg := currentSettings()
	log.Printf("自动识别：%v；手动进程名：%q", cfg.AutoFollow, cfg.FollowExe)
	log.Printf("内置候选：%s", strings.Join(builtinFollowTargets, " / "))

	log.Printf("---------- 托盘图标自检 ----------")
	if ok, detail := probeTrayIcon(); ok {
		log.Printf("结果：%s", detail)
	} else {
		log.Printf("结果：失败 —— %s", detail)
	}

	log.Printf("---------- 注册表安装项 ----------")
	installs := findGameInstalls()
	if len(installs) == 0 {
		log.Printf("没有找到 DisplayName 匹配「三角洲行动 / delta force」的卸载项")
	}
	for _, in := range installs {
		log.Printf("[评分 %d] %s", in.score, in.displayName)
		if in.installDir != "" {
			log.Printf("    InstallLocation：%s（存在=%v）", in.installDir, dirExists(in.installDir))
		}
		if in.iconPath != "" {
			log.Printf("    DisplayIcon：    %s（存在=%v）", in.iconPath, fileExists(in.iconPath))
		}
		if in.uninstall != "" {
			log.Printf("    UninstallString：%s", in.uninstall)
		}
		for _, root := range candidateRoots(in) {
			log.Printf("    候选根目录：%s（存在=%v）", root, dirExists(root))
		}
	}

	log.Printf("---------- 运行中、名字含 delta 的进程 ----------")
	matched := 0
	for lower, original := range processNameSet() {
		if !strings.Contains(lower, "delta") {
			continue
		}
		matched++
		log.Printf("  %-50s 识别为游戏=%v", original, looksLikeGameProcess(lower))
	}
	if matched == 0 {
		log.Printf("（无）如果游戏此刻正在运行，说明进程名里根本没有 delta")
		// 兜底：把当前全部进程打出来，方便远程确认游戏到底叫什么
		var all []string
		for _, original := range processNameSet() {
			all = append(all, original)
		}
		sort.Strings(all)
		log.Printf("---------- 当前全部进程（%d 个） ----------", len(all))
		log.Printf("%s", strings.Join(all, ", "))
	}

	log.Printf("---------- 自动识别结果 ----------")
	targets := detectGameTargets()
	if len(targets) == 0 {
		log.Printf("（空）注册表与进程名都没识别出主程序")
	}
	for _, t := range targets {
		log.Printf("  %s（运行中=%v）", t, isProcessRunning(t))
	}

	log.Printf("---------- 最终跟随目标 ----------")
	final := followTargets()
	for _, t := range final {
		log.Printf("  %s（运行中=%v）", t, isProcessRunning(t))
	}
	if anyProcessRunning(final) {
		log.Printf("结论：有跟随目标正在运行，悬浮窗应当显示")
	} else {
		log.Printf("结论：没有任何跟随目标在运行，悬浮窗不会显示")
		log.Printf("如果游戏其实开着，请把上面「名字含 delta 的进程」整段发给作者")
	}
	log.Printf("==========================================")
}

func fileState(path string) string {
	if _, err := os.Stat(path); err != nil {
		return "不存在"
	}
	return "存在"
}

// probeTrayIcon 用当前权限真实地添加一次托盘图标再移除，
// 用来区分"图标加不上"和"加上了但被系统折叠隐藏"这两种情况。
func probeTrayIcon() (bool, string) {
	hInst, _, _ := pGetModuleHandleW.Call(0)
	class, err := syscall.UTF16PtrFromString("STATIC")
	if err != nil {
		return false, err.Error()
	}

	// 一个隐藏的顶层窗口即可，不需要消息循环
	hwnd, _, callErr := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(class)), 0,
		uintptr(wsPopup), 0, 0, 1, 1, 0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return false, fmt.Sprintf("创建探测窗口失败（%v）", callErr)
	}
	defer pDestroyWindow.Call(hwnd)

	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = trayIconID
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmAppTray
	nid.HIcon, _, _ = pLoadIconW.Call(0, 32512)
	tip, _ := syscall.UTF16FromString("三角洲行动 · 每日密码（自检）")
	copy(nid.SzTip[:], tip)

	r, _, callErr := pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		return false, fmt.Sprintf(
			"Shell_NotifyIcon(NIM_ADD) 返回 0（%v）。若错误是 Access is denied，"+
				"说明本进程的完整性级别低于资源管理器，消息被 UIPI 拦掉了", callErr)
	}
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	return true, "成功添加并已立即移除；如果运行程序时看不到图标，说明是被系统折叠进了「^」隐藏区"
}

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// findProcessID 返回指定进程名第一个实例的 PID
func findProcessID(name string) uint32 {
	snap, _, _ := pCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == 0 || snap == ^uintptr(0) {
		return 0
	}
	defer pCloseHandle.Call(snap)

	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	r, _, _ := pProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for r != 0 {
		if strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), name) {
			return entry.ProcessID
		}
		r, _, _ = pProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return 0
}

// parentProcessName 返回父进程的可执行文件名。
// 进程的完整性级别由父进程继承，所以"是谁把我拉起来的"直接决定了权限环境。
func parentProcessName() string {
	self := uint32(os.Getpid())

	snap, _, _ := pCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == 0 || snap == ^uintptr(0) {
		return ""
	}
	defer pCloseHandle.Call(snap)

	names := make(map[uint32]string, 256)
	parents := make(map[uint32]uint32, 256)

	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	r, _, _ := pProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for r != 0 {
		names[entry.ProcessID] = syscall.UTF16ToString(entry.ExeFile[:])
		parents[entry.ProcessID] = entry.ParentProcessID
		r, _, _ = pProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return names[parents[self]]
}
