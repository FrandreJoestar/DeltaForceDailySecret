//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	tokenQuery          = 0x0008
	tokenElevationClass = 20
	// TokenIntegrityLevel；读出来的 RID 决定进程能不能给任务栏发消息
	tokenIntegrityLevelClass = 25
	processQueryLimitedInfo  = 0x1000

	mbOK            = 0x00000000
	mbIconError     = 0x00000010
	mbSetForeground = 0x00010000

	// ShellExecuteW 返回值大于 32 才算成功
	shellExecuteSuccess = 32

	// 提权尝试标记：拿不到管理员权限时避免反复重启自身
	envElevationTried = "DELTAFORCE_SECRET_ELEVATION_TRIED"
)

// sidAndAttributes / tokenMandatoryLabel 对应 Win32 的 TOKEN_MANDATORY_LABEL
type sidAndAttributes struct {
	Sid        unsafe.Pointer
	Attributes uint32
}

type tokenMandatoryLabel struct {
	Label sidAndAttributes
}

var (
	pOpenProcessToken    = advapi32.NewProc("OpenProcessToken")
	pGetTokenInformation = advapi32.NewProc("GetTokenInformation")
	pIsTokenRestricted   = advapi32.NewProc("IsTokenRestricted")
	pGetCurrentProcess   = kernel32.NewProc("GetCurrentProcess")
	pOpenProcess         = kernel32.NewProc("OpenProcess")
	pLocalFree           = kernel32.NewProc("LocalFree")
	pShellExecuteW       = shell32.NewProc("ShellExecuteW")
	pMessageBoxW         = user32.NewProc("MessageBoxW")
)

// tokenElevationInfo 对应 Win32 的 TOKEN_ELEVATION
type tokenElevationInfo struct {
	TokenIsElevated uint32
}

// isElevated 判断当前进程是否以管理员身份（高完整性级别）运行
func isElevated() bool {
	proc, _, _ := pGetCurrentProcess.Call()

	var token syscall.Handle
	if r, _, _ := pOpenProcessToken.Call(proc, tokenQuery, uintptr(unsafe.Pointer(&token))); r == 0 {
		return false
	}
	defer pCloseHandle.Call(uintptr(token))

	var info tokenElevationInfo
	size := uint32(unsafe.Sizeof(info))
	if r, _, _ := pGetTokenInformation.Call(
		uintptr(token), tokenElevationClass,
		uintptr(unsafe.Pointer(&info)),
		uintptr(size), uintptr(unsafe.Pointer(&size)),
	); r == 0 {
		return false
	}
	return info.TokenIsElevated != 0
}

// tokenIntegrityRID 读取令牌的完整性级别 RID：0x1000 低 / 0x2000 中 / 0x3000 高
func tokenIntegrityRID(token syscall.Handle) (uint32, bool) {
	// TokenIntegrityLevel 会把 SID 内联在 TOKEN_MANDATORY_LABEL 之后，
	// 所以必须先按系统要求的大小取一次，不能直接用结构体大小
	var size uint32
	pGetTokenInformation.Call(
		uintptr(token), tokenIntegrityLevelClass, 0, 0,
		uintptr(unsafe.Pointer(&size)),
	)
	if size == 0 {
		return 0, false
	}

	buf := make([]byte, size)
	if r, _, _ := pGetTokenInformation.Call(
		uintptr(token), tokenIntegrityLevelClass,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(size),
		uintptr(unsafe.Pointer(&size)),
	); r == 0 {
		return 0, false
	}

	label := (*tokenMandatoryLabel)(unsafe.Pointer(&buf[0]))
	if label.Label.Sid == nil {
		return 0, false
	}
	// SID 布局：Revision(1) + SubAuthorityCount(1) + IdentifierAuthority(6) + SubAuthority[0]
	// 完整性级别 SID 形如 S-1-16-<RID>，所以第一个子权威就是级别
	rid := *(*uint32)(unsafe.Add(label.Label.Sid, 8))
	return rid, true
}

// integrityName 把完整性级别 RID 翻译成可读名称
func integrityName(rid uint32) string {
	switch {
	case rid >= 0x5000:
		return "Protected"
	case rid >= 0x4000:
		return "System"
	case rid >= 0x3000:
		return "High（管理员）"
	case rid >= 0x2100:
		return "Medium Plus"
	case rid >= 0x2000:
		return "Medium（普通用户）"
	case rid >= 0x1000:
		return "Low（受限 / 沙盒）"
	default:
		return "Untrusted"
	}
}

// currentIntegrityRID 返回当前进程的完整性级别 RID
func currentIntegrityRID() (uint32, bool) {
	proc, _, _ := pGetCurrentProcess.Call()

	var token syscall.Handle
	if r, _, _ := pOpenProcessToken.Call(proc, tokenQuery, uintptr(unsafe.Pointer(&token))); r == 0 {
		return 0, false
	}
	defer pCloseHandle.Call(uintptr(token))

	return tokenIntegrityRID(token)
}

// processIntegrityRID 返回指定进程的完整性级别 RID。
// 读不到时通常意味着目标进程的完整性级别比调用者更高（强制完整性策略禁止读高）。
func processIntegrityRID(pid uint32) (uint32, bool) {
	h, _, _ := pOpenProcess.Call(processQueryLimitedInfo, 0, uintptr(pid))
	if h == 0 {
		return 0, false
	}
	defer pCloseHandle.Call(h)

	var token syscall.Handle
	if r, _, _ := pOpenProcessToken.Call(h, tokenQuery, uintptr(unsafe.Pointer(&token))); r == 0 {
		return 0, false
	}
	defer pCloseHandle.Call(uintptr(token))

	return tokenIntegrityRID(token)
}

// processNeedsElevation 判断当前环境是否需要提权才能正常工作。
//
// 返回 true 的典型情况：进程被沙盒 / 安全软件的「安全运行」用受限令牌拉起，
// 或者被降到 Low 完整性级别。这种进程写不了用户目录、也加不上托盘图标，
// 不换一个干净的令牌就只能是残废状态。
// 正常从资源管理器启动的进程（Medium + 非受限令牌）返回 false，不会打扰用户。
func processNeedsElevation() bool {
	if tokenRestricted() {
		return true
	}
	if rid, ok := currentIntegrityRID(); ok && rid < 0x2000 {
		return true
	}
	return false
}

// tokenRestricted 判断当前进程令牌是否是"受限令牌"。
//
// 沙盒/安全软件（包括各类受限启动器）会用 CreateRestrictedToken 造一个令牌，
// 它的访问检查会多一道"受限 SID"限制：即使 ACL 里给用户开了 FullControl，
// 只要那张 ACL 没有授权受限 SID，写入照样 Access is denied。
// 这是"ACL 明明允许却写不进去"的唯一常见解释，比完整性级别更具决定性。
func tokenRestricted() bool {
	proc, _, _ := pGetCurrentProcess.Call()

	var token syscall.Handle
	if r, _, _ := pOpenProcessToken.Call(proc, tokenQuery, uintptr(unsafe.Pointer(&token))); r == 0 {
		return false
	}
	defer pCloseHandle.Call(uintptr(token))

	r, _, _ := pIsTokenRestricted.Call(uintptr(token))
	return r != 0
}

// integrityContext 返回"本进程 / explorer.exe"的完整性级别摘要。
// 托盘图标依赖 WM_COPYDATA 发给资源管理器，两边级别不一致就会被 UIPI 拦掉，
// 所以出问题时这行日志就是最关键的证据。
func integrityContext() string {
	ours := "未知"
	if rid, ok := currentIntegrityRID(); ok {
		ours = integrityName(rid)
	}

	explorer := "未找到"
	if pid := findProcessID("explorer.exe"); pid != 0 {
		if rid, ok := processIntegrityRID(pid); ok {
			explorer = integrityName(rid)
		} else {
			explorer = "读不到（级别高于本进程）"
		}
	}

	parent := parentProcessName()
	if parent == "" {
		parent = "未知"
	}
	return fmt.Sprintf("本进程=%s，explorer.exe=%s，父进程=%s，受限令牌=%v",
		ours, explorer, parent, tokenRestricted())
}

// ensureElevated 保证当前进程以管理员身份运行。
//
// 已提权时返回 true，调用方继续初始化；未提权时用 UAC（runas 动词）
// 重新拉起自身并返回 false，调用方必须直接退出，不要创建窗口或抢单实例互斥体。
func ensureElevated() bool {
	if isElevated() {
		return true
	}

	// 已经提过一次仍然没权限（UAC 被取消、策略禁止等），不再重试以免无限重启
	if os.Getenv(envElevationTried) != "" {
		log.Printf("当前进程不是管理员身份，且已尝试过提权，请手动右键「以管理员身份运行」")
		showMessageBox("需要管理员权限",
			"本程序需要以管理员身份运行，但自动提权没有成功。\r\n\r\n请右键点击程序，选择「以管理员身份运行」。")
		return false
	}

	exe, err := os.Executable()
	if err != nil {
		log.Printf("获取程序路径失败，无法提权: %v", err)
		showMessageBox("无法以管理员身份启动", "无法定位程序自身路径，请右键选择「以管理员身份运行」。")
		return false
	}

	// 子进程继承环境变量，据此判断"已经提过权"
	if err := os.Setenv(envElevationTried, "1"); err != nil {
		log.Printf("设置提权标记失败: %v", err)
	}

	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)

	var params uintptr
	if line := buildArgLine(os.Args[1:]); line != "" {
		p, _ := syscall.UTF16PtrFromString(line)
		params = uintptr(unsafe.Pointer(p))
	}

	r, _, _ := pShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		params, 0, uintptr(swShowNormal),
	)
	if r <= shellExecuteSuccess {
		log.Printf("提权启动失败（返回码 %d），可能是取消了 UAC 提示", r)
		showMessageBox("无法以管理员身份启动",
			"请求管理员权限失败，可能是在 UAC 提示中点了「否」。\r\n\r\n请右键点击程序，选择「以管理员身份运行」。")
		return false
	}

	log.Printf("已请求管理员权限，正在以管理员身份重新启动")
	return false
}

// buildArgLine 把参数重新拼成命令行：含空格、制表符或引号的参数加双引号
func buildArgLine(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t") || strings.Contains(a, `"`) {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// showMessageBox 弹一个置顶的错误提示框（GUI 模式下没有控制台，必须靠它告知用户）
func showMessageBox(title, text string) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	m, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	pMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		mbIconError|mbOK|mbSetForeground,
	)
}
