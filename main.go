package main

import (
	"flag"
	"fmt"
	"log"
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
	setupConsole()

	log.SetFlags(0)
	log.SetPrefix("[每日密码] ")

	var opts options
	flag.StringVar(&opts.openid, "openid", "", "直接指定 openid（需配合 -token 使用，跳过登录）")
	flag.StringVar(&opts.token, "token", "", "直接指定 access_token（需配合 -openid 使用）")
	flag.BoolVar(&opts.relogin, "login", false, "强制重新登录")
	flag.BoolVar(&opts.forceQRCode, "qrcode", false, "跳过电脑版 QQ 快捷登录，强制使用手机扫码登录")
	flag.StringVar(&opts.uin, "uin", "", "电脑版 QQ 快捷登录时指定要使用的 QQ 号")
	flag.StringVar(&opts.credFile, "cred", "credentials.json", "登录凭据缓存文件路径")
	flag.StringVar(&opts.qrFile, "qr", "qr.png", "登录二维码图片保存路径")
	overlayMode := flag.Bool("overlay", true, "在屏幕右上角以悬浮窗显示每日密码；-overlay=false 时仅控制台输出")
	flag.Parse()

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
			fmt.Printf("使用缓存的登录凭据（保存于 %s）\n", c.SavedAt.Format("2006-01-02 15:04:05"))
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
	fmt.Printf("登录凭据已保存到 %s\n", path)
}
