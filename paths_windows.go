//go:build windows

package main

import (
	"os"
	"path/filepath"
)

var appDataDirOnce string

// appDataDir 返回配置、凭据、二维码等运行数据的存放目录。
//
// 优先 %LOCALAPPDATA%\DeltaForceDailySecret：这个位置标准用户可写，
// 不需要管理员权限，也不会因为程序装在 Program Files 而写不进去。
// 取不到用户目录时退回程序目录，保持便携使用的能力。
func appDataDir() string {
	if appDataDirOnce != "" {
		return appDataDirOnce
	}

	dir := ""
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		candidate := filepath.Join(base, "DeltaForceDailySecret")
		if err := os.MkdirAll(candidate, 0o700); err == nil {
			dir = candidate
		}
	}
	if dir == "" {
		if exe, err := os.Executable(); err == nil {
			dir = filepath.Dir(exe)
		} else {
			dir = "."
		}
	}
	appDataDirOnce = dir
	return dir
}

// resolveDataFile 决定某个数据文件实际用的路径。
//
// 新位置（%LOCALAPPDATA%）有文件就用新位置；否则如果程序目录下还留着
// 旧版本的 settings.json / credentials.json，就继续用旧的，避免升级后
// 配置丢失、凭据被当成不存在而重新弹扫码。
func resolveDataFile(name string) string {
	def := filepath.Join(appDataDir(), name)

	legacy := ""
	if exe, err := os.Executable(); err == nil {
		legacy = filepath.Join(filepath.Dir(exe), name)
	}

	return pickDataPath(def, legacy, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

// pickDataPath 是 resolveDataFile 的纯逻辑部分，便于单测
func pickDataPath(def, legacy string, exists func(string) bool) string {
	if exists(def) || legacy == "" || legacy == def {
		return def
	}
	if exists(legacy) {
		return legacy
	}
	return def
}
