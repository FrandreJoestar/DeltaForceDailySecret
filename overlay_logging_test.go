//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestTokenElevationStructSize(t *testing.T) {
	// TOKEN_ELEVATION 只有一个 DWORD，结构体大小错了会让 GetTokenInformation 静默失败
	if got := unsafe.Sizeof(tokenElevationInfo{}); got != 4 {
		t.Fatalf("tokenElevationInfo size = %d, want 4", got)
	}
	// 只要求能安全调用，不校验返回值（取决于测试进程本身是否已提权）
	_ = isElevated()
}

func TestBuildArgLine(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"-overlay=false"}, "-overlay=false"},
		{[]string{"-cred", "C:\\a b\\c.json"}, `-cred "C:\a b\c.json"`},
		{[]string{"-qr", `C:\x"y.png`}, `-qr "C:\x\"y.png"`},
		{[]string{"-uin", ""}, `-uin ""`},
	}
	for _, c := range cases {
		if got := buildArgLine(c.args); got != c.want {
			t.Errorf("buildArgLine(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestRoundCoverageShape(t *testing.T) {
	const w, h = int32(246), int32(200)
	dpiScale = 1
	mask := roundCoverage(w, h)
	if len(mask) != int(w*h) {
		t.Fatalf("mask size = %d, want %d", len(mask), w*h)
	}

	at := func(x, y int32) byte { return mask[int(y)*int(w)+int(x)] }

	if v := at(0, 0); v != 0 {
		t.Errorf("corner (0,0) = %d, want 0", v)
	}
	if v := at(w-1, h-1); v != 0 {
		t.Errorf("corner (w-1,h-1) = %d, want 0", v)
	}
	if v := at(w/2, h/2); v != 255 {
		t.Errorf("center = %d, want 255", v)
	}
	if v := at(w/2, 0); v != 255 {
		t.Errorf("top edge middle = %d, want 255", v)
	}
	if v := at(0, h/2); v != 255 {
		t.Errorf("left edge middle = %d, want 255", v)
	}

	// 角落必须有半透明过渡像素，否则说明又退化成硬裁剪了
	soft := 0
	for y := int32(0); y < 12; y++ {
		for x := int32(0); x < 12; x++ {
			if v := at(x, y); v > 0 && v < 255 {
				soft++
			}
		}
	}
	if soft == 0 {
		t.Errorf("corner has no anti-aliased pixels")
	}

	// 同尺寸应命中缓存
	if &roundCoverage(w, h)[0] != &mask[0] {
		t.Errorf("roundCoverage did not reuse cache")
	}
}

func TestLooksLikeGameProcess(t *testing.T) {
	cases := map[string]bool{
		"DeltaForceClient-Win64-Shipping.exe":   true,
		"Delta Force Client-Win64-Shipping.exe": true,
		"deltaforce.exe":                        true,
		"DELTAFORCE-Win64-Shipping.exe":         true,
		"DeltaForceLauncher.exe":                false, // 启动器不算游戏主程序
		"DeltaForceClientHelper.exe":            false,
		"DeltaForceCrashReporter.exe":           false,
		"deltaforce-secret.exe":                 false, // 不能把自己识别成游戏
		"explorer.exe":                          false,
		"notepad.exe":                           false,
		"UnrealEditor.exe":                      false,
		"wegame.exe":                            false,
	}
	for name, want := range cases {
		if got := looksLikeGameProcess(name); got != want {
			t.Errorf("looksLikeGameProcess(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMergeTargets(t *testing.T) {
	got := mergeTargets(
		[]string{"A.exe", "B.exe"},
		[]string{"b.EXE", "C.exe", ""},
	)
	want := []string{"A.exe", "B.exe", "C.exe"}
	if len(got) != len(want) {
		t.Fatalf("mergeTargets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mergeTargets[%d] = %q, want %q（顺序与去重）", i, got[i], want[i])
		}
	}
}

func TestLogSinkRing(t *testing.T) {
	s := &logSink{ring: make([]string, 3)}
	var console bytes.Buffer
	s.setConsole(&console)

	for _, line := range []string{"one\n", "two\r\n", "three\n", "four\n", "five\n"} {
		if _, err := s.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	s.mu.Lock()
	var got []string
	for i := 0; i < s.ringLen; i++ {
		got = append(got, s.ring[(s.ringPos+i)%len(s.ring)])
	}
	s.mu.Unlock()

	if len(got) != 3 {
		t.Fatalf("ring kept %d lines, want 3", len(got))
	}
	for i, want := range []string{"three", "four", "five"} {
		if !strings.HasSuffix(got[i], want) {
			t.Errorf("ring[%d] = %q, want suffix %q", i, got[i], want)
		}
	}
	if n := strings.Count(console.String(), "\r\n"); n != 5 {
		t.Errorf("console lines = %d, want 5 (%q)", n, console.String())
	}

	var replay bytes.Buffer
	s.replay(&replay)
	if !strings.Contains(replay.String(), "three") || !strings.Contains(replay.String(), "five") {
		t.Errorf("replay missing lines: %q", replay.String())
	}
}

func TestPruneOldLogs(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	fresh := time.Now().Format("2006-01-02")
	for _, day := range []string{old, fresh} {
		if err := os.WriteFile(filepath.Join(dir, logFilePrefix+day+logFileSuffix), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	pruneOldLogs(dir, 7)

	if _, err := os.Stat(filepath.Join(dir, logFilePrefix+old+logFileSuffix)); !os.IsNotExist(err) {
		t.Errorf("old log not pruned (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, logFilePrefix+fresh+logFileSuffix)); err != nil {
		t.Errorf("fresh log removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.txt")); err != nil {
		t.Errorf("unrelated file removed: %v", err)
	}
}

func TestClampOverlayPos(t *testing.T) {
	dpiScale = 1
	// 远在屏幕外时，至少保留 80px 可见
	x, y := clampOverlayPos(-9999, -9999, 246, 200)
	if x < -246+80 || y < -200+80 {
		t.Errorf("clamp left/top failed: %d,%d", x, y)
	}
}

// TestLogSinkWritesFile 验证按天滚动的文件日志真的落盘了
func TestLogSinkWritesFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LocalAppData", dir)

	s := &logSink{ring: make([]string, logRingLines)}
	if _, err := s.Write([]byte("测试日志 A\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := s.Write([]byte("测试日志 B\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want := filepath.Join(dir, "DeltaForceDailySecret", "logs",
		logFilePrefix+time.Now().Format("2006-01-02")+logFileSuffix)
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("读日志文件失败: %v", err)
	}
	for _, line := range []string{"测试日志 A", "测试日志 B"} {
		if !strings.Contains(string(data), line) {
			t.Errorf("日志文件缺少 %q，内容：%q", line, string(data))
		}
	}
	// 每行都要带时间戳，否则无法定位问题发生的时刻
	if !strings.Contains(string(data), time.Now().Format("15:04:")) {
		t.Errorf("日志行缺少时间戳：%q", string(data))
	}
	if err := s.file.Close(); err != nil {
		t.Errorf("关闭日志文件失败: %v", err)
	}
}

func TestPickDataPath(t *testing.T) {
	only := func(paths ...string) func(string) bool {
		return func(p string) bool {
			for _, v := range paths {
				if v == p {
					return true
				}
			}
			return false
		}
	}

	cases := []struct {
		name   string
		def    string
		legacy string
		exists func(string) bool
		want   string
	}{
		{"新位置已有文件", `C:\new\a.json`, `C:\old\a.json`, only(`C:\new\a.json`), `C:\new\a.json`},
		{"只有旧文件时兼容旧位置", `C:\new\a.json`, `C:\old\a.json`, only(`C:\old\a.json`), `C:\old\a.json`},
		{"都没有时用新位置", `C:\new\a.json`, `C:\old\a.json`, only(), `C:\new\a.json`},
		{"两个位置相同", `C:\a.json`, `C:\a.json`, only(`C:\a.json`), `C:\a.json`},
		{"无法定位旧位置", `C:\new\a.json`, "", only(), `C:\new\a.json`},
	}
	for _, c := range cases {
		if got := pickDataPath(c.def, c.legacy, c.exists); got != c.want {
			t.Errorf("%s: pickDataPath = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAppDataDirIsAbsolute(t *testing.T) {
	dir := appDataDir()
	if dir == "" {
		t.Fatal("appDataDir 返回空")
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("appDataDir = %q，应为绝对路径（否则从开机自启启动时会写到工作目录）", dir)
	}
	// settings 和 credentials 必须落在同一个目录，避免二者分散在两处
	if got, want := filepath.Dir(resolveDataFile("credentials.json")), dir; got != want {
		t.Errorf("凭据目录 = %q, 期望 %q", got, want)
	}
}
