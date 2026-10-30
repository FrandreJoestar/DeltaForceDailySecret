package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"
)

// options 是登录与取数相关的命令行配置
type options struct {
	openid      string
	token       string
	relogin     bool
	forceQRCode bool
	uin         string
	credFile    string
	qrFile      string
}

func main() {
	// 先建好日志落地（环形缓冲 + 文件），再绑定控制台，
	// 这样从终端启动和控制台模式下的日志也能进文件
	initLogging()
	setupConsole()

	var opts options
	flag.StringVar(&opts.openid, "openid", "", "直接指定 openid（需配合 -token 使用，跳过登录）")
	flag.StringVar(&opts.token, "token", "", "直接指定 access_token（需配合 -openid 使用）")
	flag.BoolVar(&opts.relogin, "login", false, "强制重新登录")
	flag.BoolVar(&opts.forceQRCode, "qrcode", false, "跳过电脑版 QQ 快捷登录，强制使用手机扫码登录")
	flag.StringVar(&opts.uin, "uin", "", "电脑版 QQ 快捷登录时指定要使用的 QQ 号")
	// 默认写到用户目录而不是当前工作目录：从开机自启/快捷方式启动时
	// 工作目录并不一定是程序目录，用相对路径会读到别处（甚至读不到）
	flag.StringVar(&opts.credFile, "cred", resolveDataFile("credentials.json"), "登录凭据缓存文件路径")
	flag.StringVar(&opts.qrFile, "qr", filepath.Join(appDataDir(), "qr.png"), "登录二维码图片保存路径")
	overlayMode := flag.Bool("overlay", true, "在屏幕右上角以悬浮窗显示每日密码；-overlay=false 时仅控制台输出")
	doctorMode := flag.Bool("doctor", false, "打印游戏进程/安装识别诊断后退出（不需要管理员权限）")
	requireAdmin := flag.Bool("elevate", false, "强制以管理员身份运行；不写则只在受限/降权环境下自动提权")
	flag.Parse()

	// 诊断模式：只读地打印识别过程，不创建窗口、不登录、不提权
	if *doctorMode {
		ensureConsole()
		runDoctor()
		waitConsoleExit()
		return
	}

	if !*overlayMode {
		ensureConsole()
		secrets, err := opts.fetchSecrets()
		if err != nil {
			log.Fatalf("获取每日密码失败: %v", err)
		}
		printSecrets(secrets)
		waitConsoleExit()
		return
	}

	// 权限策略（默认"该提才提"）：
	//   - 显式 -elevate=true  → 一定提权；
	//   - 显式 -elevate=false → 一定不提权；
	//   - 都没写（默认）       → 只有检测到受限环境才提权。
	//
	// "受限环境"指被沙盒/安全软件用受限令牌拉起，或被降到 Low 完整性级别：
	// 这种进程写不了用户目录、也加不上托盘图标，不提权就是残废的。
	// 正常从资源管理器启动的进程（Medium + 非受限令牌）不会被提权打扰。
	//
	// 单实例检查必须放在提权之后：如果先抢互斥体，未提权的中间进程会短暂占住它，
	// 导致真正提权后的进程误判"已有实例在运行"。
	switch {
	case flagPassed("elevate") && *requireAdmin:
		if !ensureElevated() {
			return
		}
	case flagPassed("elevate") && !*requireAdmin:
		log.Printf("按 -elevate=false 以当前身份运行（管理员=%v）", isElevated())
	case processNeedsElevation():
		log.Printf("检测到受限运行环境（%s），自动请求管理员权限", integrityContext())
		if !ensureElevated() {
			return
		}
	default:
		log.Printf("以当前身份运行（管理员=%v，受限令牌=false）", isElevated())
	}

	// 已有实例在运行时只会唤醒它的悬浮窗，本次启动直接退出
	if !acquireSingleInstance() {
		return
	}

	if err := runOverlay(opts.fetchSecrets); err != nil {
		log.Fatalf("悬浮窗启动失败: %v", err)
	}
}

// fetchSecrets 读取缓存凭据（或重新登录）并获取每日密码
func (o options) fetchSecrets() ([]Secret, error) {
	var (
		cred      *Credentials
		fromCache bool
	)

	switch {
	case o.openid != "" && o.token != "":
		cred = &Credentials{OpenID: o.openid, AccessToken: o.token, SavedAt: time.Now()}
	case o.relogin:
		// 强制重新登录，cred 保持为空
	default:
		if c, err := loadCredentials(o.credFile); err == nil {
			cred = c
			fromCache = true
			log.Printf("使用缓存的登录凭据（保存于 %s）", c.SavedAt.Format("2006-01-02 15:04:05"))
		}
	}

	if cred == nil {
		var err error
		cred, err = login(o.qrFile, o.uin, o.forceQRCode)
		if err != nil {
			return nil, fmt.Errorf("登录失败: %w", err)
		}
		saveCredentialsOrWarn(o.credFile, cred)
	}

	secrets, err := fetchDailySecret(cred)
	if err != nil && fromCache {
		log.Printf("使用缓存凭据获取失败（%v），重新登录", err)
		cred, err = login(o.qrFile, o.uin, o.forceQRCode)
		if err == nil {
			saveCredentialsOrWarn(o.credFile, cred)
			secrets, err = fetchDailySecret(cred)
		}
	}
	if err != nil {
		return nil, err
	}
	return secrets, nil
}

func saveCredentialsOrWarn(path string, cred *Credentials) {
	if err := saveCredentials(path, cred); err != nil {
		log.Printf("保存登录凭据失败: %v", err)
		return
	}
	log.Printf("登录凭据已保存到 %s", path)
}

// flagPassed 判断某个命令行参数是否被显式指定过。
// 用来区分"用户没写 -elevate"（自动模式）和"用户显式写了 -elevate=false"。
func flagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
