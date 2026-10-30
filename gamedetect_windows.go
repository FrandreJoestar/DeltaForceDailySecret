//go:build windows

package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	hkeyLocalMachine = 0x80000002
	keyRead          = 0x20019
)

var (
	pRegEnumKeyExW = advapi32.NewProc("RegEnumKeyExW")

	gameTargetsMu sync.Mutex
	gameTargets   []string
	gameTargetsAt time.Time
)

type uninstallRoot struct {
	hkey uintptr
	path string
}

type gameInstall struct {
	displayName string
	iconPath    string
	uninstall   string
	installDir  string
	score       int
}

// builtinFollowTargets 是内置的游戏主程序候选名，任一在运行就显示悬浮窗；
// 不同发行渠道（WeGame / Steam）的主程序名可能不同
var builtinFollowTargets = []string{
	"DeltaForceClient-Win64-Shipping.exe",
	"DeltaForceClient.exe",
	"Delta Force Client-Win64-Shipping.exe",
}

// followTargets 返回需要跟随的进程名；优先使用自动识别结果，其次用内置候选与手动配置
func followTargets() []string {
	cfg := currentSettings()
	if cfg.AutoFollow {
		if targets := detectGameTargets(); len(targets) > 0 {
			return targets
		}
	}

	targets := append([]string(nil), builtinFollowTargets...)
	if exe := normalizeProcessName(cfg.FollowExe); exe != "" {
		found := false
		for _, t := range targets {
			if strings.EqualFold(t, exe) {
				found = true
				break
			}
		}
		if !found {
			targets = append([]string{exe}, targets...)
		}
	}
	return targets
}

// normalizeProcessName 把用户输入的进程名规范化为可比较的文件名，
// 兼容带引号、带完整路径、省略 .exe 等写法（名称本身允许包含空格）
func normalizeProcessName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, `"'`)
	if name == "" {
		return ""
	}
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if !strings.EqualFold(filepath.Ext(name), ".exe") {
		name += ".exe"
	}
	return name
}

func anyProcessRunning(targets []string) bool {
	if len(targets) == 0 {
		return false
	}
	// 只建一次快照，避免每个候选进程各枚举一遍系统进程
	set := processNameSet()
	for _, t := range targets {
		exe := normalizeProcessName(t)
		if exe == "" {
			continue
		}
		if _, ok := set[strings.ToLower(exe)]; ok {
			return true
		}
	}
	return false
}

// looksLikeGameProcess 判断进程名是否像《三角洲行动》的主程序。
//
// 不同发行渠道（WeGame / Steam / Epic）的主程序名不一样，客户端升级也可能改名，
// 死名单总有漏网的一天；这里用命名特征兜底，并排除启动器、崩溃上报、反作弊等附属进程。
func looksLikeGameProcess(name string) bool {
	n := strings.ToLower(name)
	if !strings.HasSuffix(n, ".exe") {
		return false
	}
	for _, bad := range []string{
		"launcher", "unins", "setup", "crash", "helper", "report",
		"anticheat", "secret", // secret 用于排除本程序自己
	} {
		if strings.Contains(n, bad) {
			return false
		}
	}
	compact := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(n)
	if strings.Contains(compact, "deltaforce") {
		return true
	}
	return strings.Contains(compact, "delta") && strings.Contains(compact, "force")
}

// runningGameProcesses 返回当前正在运行、且名字像是游戏主程序的进程
func runningGameProcesses() []string {
	self := ""
	if exe, err := os.Executable(); err == nil {
		self = strings.ToLower(filepath.Base(exe))
	}

	var found []string
	for lower, original := range processNameSet() {
		if lower == self {
			continue
		}
		if looksLikeGameProcess(lower) {
			found = append(found, original)
		}
	}
	sort.Strings(found)
	return found
}

// mergeTargets 合并两组进程名并去重（大小写不敏感），保持先出现的顺序
func mergeTargets(groups ...[]string) []string {
	seen := make(map[string]bool, 8)
	var out []string
	for _, group := range groups {
		for _, name := range group {
			key := strings.ToLower(name)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, name)
		}
	}
	return out
}

// followSummary 返回一行跟随状态描述，用于设置窗口提示
func followSummary() string {
	targets := followTargets()
	state := "未运行"
	if anyProcessRunning(targets) {
		state = "运行中"
	}
	auto := "开启"
	if !currentSettings().AutoFollow {
		auto = "关闭"
	}
	return fmt.Sprintf("跟随 %d 个候选进程，游戏%s（自动识别：%s）", len(targets), state, auto)
}

// logFollowDiagnostics 输出完整的跟随配置，便于排查识别问题
func logFollowDiagnostics() {
	cfg := currentSettings()
	targets := followTargets()
	log.Printf("自动识别：%v；手动进程名：%q", cfg.AutoFollow, cfg.FollowExe)
	log.Printf("跟随目标：%s", strings.Join(targets, " / "))
	for _, t := range targets {
		log.Printf("  %s -> %v", t, isProcessRunning(t))
	}
}

// detectGameTargets 通过注册表定位三角洲行动主程序，结果带缓存
func detectGameTargets() []string {
	gameTargetsMu.Lock()
	defer gameTargetsMu.Unlock()

	ttl := 10 * time.Minute
	if len(gameTargets) == 0 {
		ttl = time.Minute
	}
	if time.Since(gameTargetsAt) < ttl {
		return append([]string(nil), gameTargets...)
	}

	installs := findGameInstalls()
	// 注册表识别 + 运行中进程名兜底：后者能救回"安装信息缺失或客户端改过名"的情况
	targets := mergeTargets(resolveMainExecutables(installs), runningGameProcesses())

	changed := len(targets) != len(gameTargets)
	if !changed {
		for i := range targets {
			if !strings.EqualFold(targets[i], gameTargets[i]) {
				changed = true
				break
			}
		}
	}
	gameTargets = targets
	gameTargetsAt = time.Now()

	if changed {
		if len(targets) > 0 {
			log.Printf("自动识别到游戏进程: %s", strings.Join(targets, ", "))
		} else {
			log.Printf("未能识别到三角洲行动主程序（注册表和运行进程都不匹配），可在设置里手动填写进程名")
		}
	}
	return append([]string(nil), targets...)
}

func findGameInstalls() []gameInstall {
	roots := []uninstallRoot{
		{hkeyCurrentUser, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{hkeyLocalMachine, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{hkeyLocalMachine, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	}

	var installs []gameInstall
	for _, root := range roots {
		for _, name := range enumSubKeys(root.hkey, root.path) {
			sub := root.path + `\` + name
			var h syscall.Handle
			if r, _, _ := pRegOpenKeyExW.Call(root.hkey, utf16Ptr(sub), 0, keyRead, uintptr(unsafe.Pointer(&h))); r != 0 {
				continue
			}
			display := regStringValue(h, "DisplayName")
			score := gameNameScore(display)
			if score == 0 {
				pRegCloseKey.Call(uintptr(h))
				continue
			}
			installs = append(installs, gameInstall{
				displayName: display,
				iconPath:    cleanRegPath(regStringValue(h, "DisplayIcon")),
				uninstall:   cleanRegPath(regStringValue(h, "UninstallString")),
				installDir:  cleanRegPath(regStringValue(h, "InstallLocation")),
				score:       score,
			})
			pRegCloseKey.Call(uintptr(h))
		}
	}
	return installs
}

func enumSubKeys(hkey uintptr, path string) []string {
	var h syscall.Handle
	if r, _, _ := pRegOpenKeyExW.Call(hkey, utf16Ptr(path), 0, keyRead, uintptr(unsafe.Pointer(&h))); r != 0 {
		return nil
	}
	defer pRegCloseKey.Call(uintptr(h))

	var names []string
	var buf [256]uint16
	for i := uint32(0); i < 4096; i++ {
		size := uint32(len(buf))
		r, _, _ := pRegEnumKeyExW.Call(
			uintptr(h), uintptr(i),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
			0, 0, 0, 0,
		)
		if r != 0 {
			break
		}
		names = append(names, syscall.UTF16ToString(buf[:size]))
	}
	return names
}

func regStringValue(h syscall.Handle, name string) string {
	var typ, size uint32
	if r, _, _ := pRegQueryValueExW.Call(
		uintptr(h), utf16Ptr(name), 0,
		uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)),
	); r != 0 || size == 0 || size > 1<<16 {
		return ""
	}

	buf := make([]uint16, size/2+1)
	if r, _, _ := pRegQueryValueExW.Call(
		uintptr(h), utf16Ptr(name), 0,
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
	); r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func gameNameScore(name string) int {
	n := strings.ToLower(name)
	if n == "" {
		return 0
	}
	// 排除助手、精彩时刻等附属程序
	if strings.Contains(n, "助手") || strings.Contains(n, "精彩时刻") {
		return 0
	}
	if strings.Contains(n, "三角洲行动") {
		return 3
	}
	if strings.Contains(n, "delta force") || strings.Contains(n, "deltaforce") {
		return 2
	}
	if strings.Contains(n, "三角洲") {
		return 1
	}
	return 0
}

// cleanRegPath 把注册表里的 DisplayIcon / UninstallString 转成纯路径
func cleanRegPath(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, `"`)

	// 去掉图标序号，例如 "C:\a.exe",0 或 C:\a.exe,0
	if i := strings.LastIndex(v, ","); i > 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(v[i+1:])); err == nil {
			v = v[:i]
		}
	}
	// 去掉命令行参数，只保留 .exe
	if i := strings.Index(strings.ToLower(v), ".exe"); i >= 0 {
		v = v[:i+4]
	}
	v = strings.Trim(v, `" `)
	return expandPercentEnv(v)
}

func expandPercentEnv(s string) string {
	for {
		i := strings.Index(s, "%")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+1:], "%")
		if j < 0 {
			return s
		}
		key := s[i+1 : i+1+j]
		val, ok := os.LookupEnv(key)
		if !ok {
			return s
		}
		s = s[:i] + val + s[i+1+j+1:]
	}
}

func resolveMainExecutables(installs []gameInstall) []string {
	var best *gameInstall
	for i := range installs {
		if best == nil || installs[i].score > best.score {
			best = &installs[i]
		}
	}
	if best == nil {
		return nil
	}

	roots := candidateRoots(*best)
	var found []string

	// 1) 常见相对路径
	relCandidates := []string{
		`Game\DeltaForce\Binaries\Win64\DeltaForceClient-Win64-Shipping.exe`,
		`Delta Force\Game\DeltaForce\Binaries\Win64\DeltaForceClient-Win64-Shipping.exe`,
		`Game\DeltaForce\Binaries\Win64\DeltaForceClient.exe`,
		`Binaries\Win64\DeltaForceClient-Win64-Shipping.exe`,
	}
	for _, root := range roots {
		for _, rel := range relCandidates {
			p := filepath.Join(root, rel)
			if fileExists(p) {
				found = append(found, p)
			}
		}
		if len(found) > 0 {
			break
		}
	}

	// 2) 有界搜索主程序
	if len(found) == 0 {
		for _, root := range roots {
			if p := searchGameClientExe(root); p != "" {
				found = append(found, p)
				break
			}
		}
	}

	// 3) 回退到启动器（仅当文件真实存在）
	if len(found) == 0 {
		for _, p := range []string{best.iconPath, best.uninstall} {
			lower := strings.ToLower(p)
			if fileExists(p) && (strings.Contains(lower, "delta") || strings.Contains(lower, "launcher")) {
				found = append(found, p)
			}
		}
	}

	seen := map[string]bool{}
	var targets []string
	for _, p := range found {
		name := filepath.Base(p)
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, name)
	}
	return targets
}

func candidateRoots(g gameInstall) []string {
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		for _, r := range roots {
			if strings.EqualFold(r, p) {
				return
			}
		}
		roots = append(roots, p)
	}

	if g.installDir != "" {
		add(g.installDir)
	}
	for _, p := range []string{g.iconPath, g.uninstall} {
		if p == "" {
			continue
		}
		dir := filepath.Dir(p)
		add(dir)
		add(filepath.Dir(dir))
		add(filepath.Dir(filepath.Dir(dir)))
	}
	return roots
}

// searchGameClientExe 在安装目录内有界搜索主程序
func searchGameClientExe(root string) string {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return ""
	}
	lowerRoot := strings.ToLower(root)
	// 避免误扫整个盘符或系统目录
	if filepath.Clean(root) == filepath.VolumeName(root)+`\` || strings.Contains(lowerRoot, `\windows`) {
		return ""
	}

	baseDepth := strings.Count(filepath.Clean(root), string(os.PathSeparator))
	found := ""
	visited := 0

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			depth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - baseDepth
			if depth > 6 {
				return filepath.SkipDir
			}
			visited++
			if visited > 40000 {
				return filepath.SkipAll
			}
			return nil
		}

		name := strings.ToLower(d.Name())
		if !strings.HasSuffix(name, ".exe") {
			return nil
		}
		if strings.Contains(name, "launcher") || strings.Contains(name, "unins") || strings.Contains(name, "setup") {
			return nil
		}
		// 文件名可能包含空格或分隔符，比较前压缩掉
		compact := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(name)
		if strings.Contains(name, "shipping") || (strings.Contains(compact, "deltaforce") && strings.Contains(compact, "client")) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
