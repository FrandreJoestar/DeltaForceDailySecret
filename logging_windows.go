//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// logRingLines 是内存中保留的最近日志条数，用于「打开控制台」时回放
	logRingLines = 300
	// logKeepDays 是磁盘日志的保留天数
	logKeepDays = 7

	logFilePrefix = "deltaforce-secret-"
	logFileSuffix = ".log"
)

// logSink 把日志同时写到三处：内存环形缓冲（供控制台回放）、
// 按天滚动的磁盘文件（GUI 模式下唯一的排障手段）、以及可用时的控制台。
type logSink struct {
	mu      sync.Mutex
	ring    []string
	ringPos int
	ringLen int

	file    *os.File
	fileDay string

	console io.Writer
}

var logSinkInst = &logSink{ring: make([]string, logRingLines)}

// initLogging 初始化日志落地。必须在 setupConsole 之前调用：
// 控制台绑定后会把输出镜像到 logSink，而不是直接改 log 的输出目标。
func initLogging() {
	log.SetFlags(0)
	log.SetPrefix("[每日密码] ")

	// 从终端启动时标准输出可用，日志直接可见
	if stdHandleValid(os.Stdout) {
		logSinkInst.console = os.Stdout
	}

	log.SetOutput(logSinkInst)

	dir := logDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	pruneOldLogs(dir, logKeepDays)
	log.Printf("日志目录：%s（保留 %d 天）", dir, logKeepDays)
}

// Write 实现 io.Writer，统一给每行加上时间戳后分发到环形缓冲、文件和（若已绑定）控制台
func (s *logSink) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\r\n")
	if text == "" {
		return len(p), nil
	}

	stamp := time.Now().Format("15:04:05")
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for _, ln := range parts {
		lines = append(lines, stamp+" "+strings.TrimRight(ln, "\r"))
	}

	s.mu.Lock()
	for _, ln := range lines {
		s.pushLocked(ln)
		s.writeFileLocked(ln)
	}
	console := s.console
	s.mu.Unlock()

	if console != nil {
		for _, ln := range lines {
			_, _ = io.WriteString(console, ln+"\r\n")
		}
	}
	return len(p), nil
}

// setConsole 设置（或替换）控制台镜像目标；传入 nil 表示不再镜像
func (s *logSink) setConsole(w io.Writer) {
	s.mu.Lock()
	s.console = w
	s.mu.Unlock()
}

// replay 把环形缓冲里的最近日志按时间顺序输出到 w
func (s *logSink) replay(w io.Writer) {
	s.mu.Lock()
	lines := make([]string, s.ringLen)
	for i := 0; i < s.ringLen; i++ {
		lines[i] = s.ring[(s.ringPos+i)%len(s.ring)]
	}
	s.mu.Unlock()

	if len(lines) == 0 {
		_, _ = io.WriteString(w, "（暂无历史日志）\r\n")
		return
	}
	fmt.Fprintf(w, "—— 最近 %d 条日志 ——\r\n", len(lines))
	for _, ln := range lines {
		_, _ = io.WriteString(w, ln+"\r\n")
	}
	_, _ = io.WriteString(w, "—— 以下为实时日志 ——\r\n")
}

func (s *logSink) pushLocked(line string) {
	if s.ringLen < len(s.ring) {
		s.ring[(s.ringPos+s.ringLen)%len(s.ring)] = line
		s.ringLen++
		return
	}
	s.ring[s.ringPos] = line
	s.ringPos = (s.ringPos + 1) % len(s.ring)
}

// writeFileLocked 按天滚动写文件；跨天时关闭旧文件并打开新文件
func (s *logSink) writeFileLocked(line string) {
	day := time.Now().Format("2006-01-02")
	if s.file == nil || s.fileDay != day {
		if s.file != nil {
			_ = s.file.Close()
			s.file = nil
		}
		f, err := openLogFile(day)
		if err != nil {
			return
		}
		s.file, s.fileDay = f, day
	}
	_, _ = io.WriteString(s.file, line+"\r\n")
}

// openLogFile 依次尝试候选目录，第一个能真正创建文件的就用它。
// 只判断"目录能不能建"是不够的——目录存在但文件写不进去时，
// 程序会变成一条日志都没有的黑盒，所以这里以实际打开文件为准。
func openLogFile(day string) (*os.File, error) {
	name := logFilePrefix + day + logFileSuffix
	var lastErr error

	for _, dir := range logDirCandidates() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			lastErr = err
			continue
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			return f, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = os.ErrInvalid
	}
	return nil, lastErr
}

// replayLogs 把内存里保留的最近日志打印到当前控制台
func replayLogs() {
	if os.Stdout == nil {
		return
	}
	logSinkInst.replay(os.Stdout)
}

// logDirCandidates 返回按优先级排列的候选日志目录
func logDirCandidates() []string {
	var dirs []string
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		dirs = append(dirs, filepath.Join(base, "DeltaForceDailySecret", "logs"))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "logs"))
	}
	return dirs
}

// logDir 返回日志目录（用于展示）：优先 %LOCALAPPDATA%，
// 建不出来时退回程序目录下的 logs
func logDir() string {
	for _, dir := range logDirCandidates() {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return dir
		}
	}
	return ""
}

// pruneOldLogs 删除超过保留天数的日志，避免长期运行把磁盘占满
func pruneOldLogs(dir string, keepDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, logFilePrefix) || !strings.HasSuffix(name, logFileSuffix) {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, logFilePrefix), logFileSuffix)
		t, err := time.Parse("2006-01-02", day)
		if err != nil || !t.Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}
