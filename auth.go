package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// 三角洲行动在 QQ 互联的 appid
	appID = "101491592"

	qrShowURL  = "https://xui.ptlogin2.qq.com/ssl/ptqrshow?appid=716027609&e=2&l=M&s=3&d=72&v=4&t=0.8668949625611146&daid=383&pt_3rd_aid=101491592&u1=https%3A%2F%2Fgraph.qq.com%2Foauth2.0%2Flogin_jump"
	qrLoginURL = "https://xui.ptlogin2.qq.com/ssl/ptqrlogin"

	qrLoginReferer = "https://xui.ptlogin2.qq.com/cgi-bin/xlogin?appid=716027609&daid=383&style=33&login_text=%E7%99%BB%E5%BD%95&hide_title_bar=1&hide_border=1&target=self&s_url=https%3A%2F%2Fgraph.qq.com%2Foauth2.0%2Flogin_jump&pt_3rd_aid=101491592&theme=2"

	authCodeURL = "https://graph.qq.com/oauth2.0/authorize"
	loginSvrURL = "https://ams.game.qq.com/ams/userLoginSvr"
)

var (
	openidRegexp = regexp.MustCompile(`"openid":"(.*?)"`)
	tokenRegexp  = regexp.MustCompile(`"access_token":"(.*?)"`)
)

// Credentials 是调用游戏接口所需的登录凭据
type Credentials struct {
	OpenID      string    `json:"openid"`
	AccessToken string    `json:"access_token"`
	SavedAt     time.Time `json:"saved_at"`
}

// hash33 是 QQ 登录流程中使用的哈希算法（ptqrtoken / g_tk）
func hash33(s string, seed int32) int32 {
	h := seed
	for i := 0; i < len(s); i++ {
		h += (h << 5) + int32(s[i])
	}
	return h & 0x7fffffff
}

// login 优先尝试电脑版 QQ 本地快捷登录，失败时回退到手机扫码登录
func login(qrPath, preferredUin string, forceQRCode bool) (*Credentials, error) {
	if !forceQRCode {
		cred, err := quickLogin(preferredUin)
		if err == nil {
			return cred, nil
		}
		log.Printf("电脑版 QQ 快捷登录不可用（%v），改用手机扫码登录", err)
	}
	return qrLogin(qrPath)
}

// qrLogin 走完整的 QQ 扫码登录流程，最终换取 openid 和 access_token
func qrLogin(qrPath string) (*Credentials, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	client := newHTTPClient(jar)

	qrsig, err := fetchQRCode(client, qrPath)
	if err != nil {
		return nil, err
	}
	log.Printf("二维码已保存到：%s", qrPath)
	log.Printf("请使用手机 QQ 扫描二维码，并在手机上确认登录")
	openQRCode(qrPath)

	checkURL, err := waitForScan(client, qrsig)
	if err != nil {
		return nil, err
	}

	pskey, err := fetchPSKey(client, checkURL)
	if err != nil {
		return nil, err
	}

	code, err := fetchAuthCode(client, hash33(pskey, 5381))
	if err != nil {
		return nil, err
	}

	return exchangeCode(client, code)
}

// quickAccount 是电脑版 QQ 本地快捷登录接口返回的账号信息
type quickAccount struct {
	Uin      json.Number `json:"uin"`
	Nickname string      `json:"nickname"`
	Port     int         `json:"-"`
}

// quickLogin 通过电脑版 QQ 的本地端口完成快捷登录，无需扫码
func quickLogin(preferredUin string) (*Credentials, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	client := newHTTPClient(jar)
	localClient := newLocalHTTPClient(jar)

	token, err := fetchPtLocalToken(client)
	if err != nil {
		return nil, err
	}

	account, err := pickLocalAccount(localClient, token, preferredUin)
	if err != nil {
		return nil, err
	}
	log.Printf("检测到电脑版 QQ 已登录账号：%s（%s），正在快捷登录", account.Nickname, account.Uin.String())

	keyIndex, err := fetchClientKey(localClient, token, account)
	if err != nil {
		return nil, err
	}

	checkURL, err := quickJump(client, token, account.Uin.String(), keyIndex)
	if err != nil {
		return nil, err
	}

	pskey, err := fetchPSKey(client, checkURL)
	if err != nil {
		return nil, err
	}

	code, err := fetchAuthCode(client, hash33(pskey, 5381))
	if err != nil {
		return nil, err
	}

	return exchangeCode(client, code)
}

// fetchPtLocalToken 访问 QQ 登录页，获取本地快捷登录所需的 pt_local_token
func fetchPtLocalToken(client *http.Client) (string, error) {
	req, err := http.NewRequest(http.MethodGet, qrLoginReferer, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取 pt_local_token 失败: %w", err)
	}
	defer resp.Body.Close()

	for _, c := range resp.Cookies() {
		if c.Name == "pt_local_token" && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", errors.New("未能获取 pt_local_token（电脑版 QQ 可能未运行）")
}

// pickLocalAccount 轮询本地端口 4301~4309，找到已登录的 QQ 账号
func pickLocalAccount(client *http.Client, token, preferredUin string) (*quickAccount, error) {
	for port := 4301; port <= 4309; port++ {
		accounts, err := fetchLocalAccounts(client, token, port)
		if err != nil {
			continue
		}
		for _, a := range accounts {
			if preferredUin == "" || a.Uin.String() == preferredUin {
				a.Port = port
				return a, nil
			}
		}
	}
	if preferredUin != "" {
		return nil, fmt.Errorf("本地已登录的 QQ 账号中没有 %s", preferredUin)
	}
	return nil, errors.New("未检测到电脑版 QQ 已登录的账号")
}

// fetchLocalAccounts 读取本地端口上的 QQ 账号列表
func fetchLocalAccounts(client *http.Client, token string, port int) ([]*quickAccount, error) {
	u := fmt.Sprintf("https://localhost.ptlogin2.qq.com:%d/pt_get_uins?callback=ptui_getuins_CB&r=0.%016d&pt_local_tk=%s",
		port, rand.Int63n(1e16), url.QueryEscape(token))

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://xui.ptlogin2.qq.com/")
	req.Header.Set("Origin", "https://xui.ptlogin2.qq.com")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("本地端口 %d 返回 HTTP %d", port, resp.StatusCode)
	}

	// 返回格式：var var_sso_uin_list=[{...}];ptui_getuins_CB(var_sso_uin_list);
	start := strings.Index(string(body), "[")
	end := strings.LastIndex(string(body), "]")
	if start < 0 || end <= start {
		return nil, errors.New("无法解析本地账号列表")
	}

	var accounts []*quickAccount
	if err := json.Unmarshal(body[start:end+1], &accounts); err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, errors.New("本地端口上没有已登录的 QQ 账号")
	}
	return accounts, nil
}

var keyIndexRegexp = regexp.MustCompile(`keyindex:\s*(\d+)`)

// fetchClientKey 用本地账号换取 clientkey 和 keyindex（clientkey 由 cookie jar 保存，跳转时自动携带）
func fetchClientKey(client *http.Client, token string, account *quickAccount) (string, error) {
	u := fmt.Sprintf("https://localhost.ptlogin2.qq.com:%d/pt_get_st?clientuin=%s&r=0.%016d&pt_local_tk=%s&callback=__jp0",
		account.Port, url.QueryEscape(account.Uin.String()), rand.Int63n(1e16), url.QueryEscape(token))

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", "https://xui.ptlogin2.qq.com/")
	req.Header.Set("Origin", "https://xui.ptlogin2.qq.com")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取 clientkey 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var clientKey string
	for _, c := range resp.Cookies() {
		if c.Name == "clientkey" && c.Value != "" {
			clientKey = c.Value
		}
	}
	if clientKey == "" {
		return "", fmt.Errorf("未获取到 clientkey: %s", body)
	}

	keyIndex := "1"
	if m := keyIndexRegexp.FindSubmatch(body); len(m) > 1 {
		keyIndex = string(m[1])
	}
	return keyIndex, nil
}

// quickJump 用 clientkey 换取登录 check_sig 地址
func quickJump(client *http.Client, token, uin, keyIndex string) (string, error) {
	q := url.Values{}
	q.Set("clientuin", uin)
	q.Set("keyindex", keyIndex)
	q.Set("pt_aid", "716027609")
	q.Set("daid", "383")
	q.Set("u1", "https://graph.qq.com/oauth2.0/login_jump")
	q.Set("pt_local_tk", token)
	q.Set("pt_3rd_aid", appID)
	q.Set("ptopt", "1")
	q.Set("style", "40")

	req, err := http.NewRequest(http.MethodGet, "https://ssl.ptlogin2.qq.com/jump?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", "https://xui.ptlogin2.qq.com/")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("快捷登录跳转失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// 返回格式：ptui_qlogin_CB('0', 'check_sig 地址', '');
	parts := strings.Split(string(body), "'")
	if len(parts) < 4 || parts[1] != "0" {
		return "", fmt.Errorf("快捷登录失败: %s", body)
	}
	return parts[3], nil
}

func newHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar:     jar,
		Timeout: 20 * time.Second,
		// 登录各步骤都需要在 302 处停下来读取 Location / Set-Cookie
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// newLocalHTTPClient 访问电脑版 QQ 的本地端口，其 HTTPS 证书是自签名的
func newLocalHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           nil,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// fetchQRCode 获取登录二维码图片和 qrsig
func fetchQRCode(client *http.Client, qrPath string) (string, error) {
	resp, err := client.Get(qrShowURL)
	if err != nil {
		return "", fmt.Errorf("获取二维码失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("获取二维码失败: HTTP %d", resp.StatusCode)
	}

	var qrsig string
	for _, c := range resp.Cookies() {
		if c.Name == "qrsig" {
			qrsig = c.Value
		}
	}
	if qrsig == "" {
		return "", errors.New("获取二维码失败: 响应中没有 qrsig")
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取二维码图片失败: %w", err)
	}
	if err := os.WriteFile(qrPath, data, 0644); err != nil {
		return "", fmt.Errorf("保存二维码图片失败: %w", err)
	}
	return qrsig, nil
}

// waitForScan 轮询扫码状态，成功后返回 check_sig 地址
func waitForScan(client *http.Client, qrsig string) (string, error) {
	u, err := url.Parse(qrLoginURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("ptqrtoken", strconv.Itoa(int(hash33(qrsig, 0))))
	q.Set("login_sig", qrsig)
	q.Set("aid", "716027609")
	q.Set("daid", "383")
	q.Set("pt_3rd_aid", appID)
	q.Set("has_onekey", "1")
	q.Set("js_ver", "25112611")
	q.Set("ptredirect", "0")
	q.Set("pt_js_version", "42f2bcc1")
	q.Set("ptlang", "2052")
	q.Set("h", "0")
	q.Set("t", "1")
	q.Set("g", "1")
	q.Set("from_ui", "1")
	q.Set("u1", "https://graph.qq.com/oauth2.0/login_jump")
	u.RawQuery = q.Encode()

	confirmed := false
	for {
		req, err := http.NewRequest(http.MethodGet, u.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Referer", qrLoginReferer)

		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("查询扫码状态失败: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("读取扫码状态失败: %w", readErr)
		}

		// 返回格式：ptuiCB('0','0','地址','0','提示信息','昵称');
		parts := strings.Split(string(body), "'")
		if len(parts) < 6 {
			return "", fmt.Errorf("无法解析扫码结果: %s", body)
		}

		switch parts[1] {
		case "0":
			if len(parts) > 9 {
				log.Printf("扫码结果：%s", parts[9])
			}
			return parts[5], nil
		case "1":
			return "", errors.New("已取消登录")
		case "65":
			return "", errors.New("二维码已失效，请重新运行程序")
		case "66":
			// 二维码尚未被扫描，继续等待
		case "67":
			if !confirmed {
				log.Printf("已扫描二维码，请在手机上确认登录")
				confirmed = true
			}
		default:
			return "", fmt.Errorf("扫码登录失败: %s", body)
		}
		time.Sleep(time.Second)
	}
}

// fetchPSKey 用 check_sig 地址换取 p_skey
func fetchPSKey(client *http.Client, checkURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, checkURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取 p_skey 失败: %w", err)
	}
	defer resp.Body.Close()

	for _, c := range resp.Cookies() {
		if c.Name == "p_skey" && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", errors.New("获取 p_skey 失败：QQ 登录态可能已失效")
}

// fetchAuthCode 使用 p_skey 计算出的 g_tk 申请授权 code
func fetchAuthCode(client *http.Client, gtk int32) (string, error) {
	form := url.Values{}
	form.Set("response_type", "code")
	form.Set("client_id", appID)
	form.Set("redirect_uri", "https://milo.qq.com/comm-htdocs/login/qc_redirect.html?parent_domain=https://df.qq.com&isMiloSDK=1&isPc=1")
	form.Set("scope", "")
	form.Set("state", "STATE")
	form.Set("switch", "")
	form.Set("form_ptlogin", "1")
	form.Set("src", "1")
	form.Set("update_auth", "1")
	form.Set("openapi", "1010")
	form.Set("auth_time", strconv.FormatInt(time.Now().UnixMilli(), 10))
	form.Set("ui", "4777C1C4-3B06-42EF-A369-E2A4DA051A14")
	form.Set("g_tk", strconv.Itoa(int(gtk)))

	req, err := http.NewRequest(http.MethodPost, authCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://xui.ptlogin2.qq.com/")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取授权 code 失败: %w", err)
	}
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	if location == "" {
		return "", errors.New("获取授权 code 失败：未返回回调地址")
	}

	callback, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("解析回调地址失败: %w", err)
	}
	code := callback.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("获取授权 code 失败：回调地址中没有 code（%s）", location)
	}
	return code, nil
}

// exchangeCode 用授权 code 换取 openid 和 access_token
func exchangeCode(client *http.Client, code string) (*Credentials, error) {
	u, err := url.Parse(loginSvrURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("a", "qcCodeToOpenId")
	q.Set("appid", appID)
	q.Set("redirect_uri", "https://milo.qq.com/comm-htdocs/login/qc_redirect.html")
	q.Set("callback", "miloJsonpCb_80466")
	q.Set("qc_code", code)
	q.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://df.qq.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("换取 openid 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 openid 响应失败: %w", err)
	}

	openid := openidRegexp.FindSubmatch(body)
	token := tokenRegexp.FindSubmatch(body)
	if len(openid) < 2 || len(token) < 2 {
		return nil, fmt.Errorf("解析 openid/access_token 失败: %s", body)
	}

	return &Credentials{
		OpenID:      string(openid[1]),
		AccessToken: string(token[1]),
		SavedAt:     time.Now(),
	}, nil
}

func loadCredentials(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.OpenID == "" || c.AccessToken == "" {
		return nil, errors.New("凭据文件内容不完整")
	}
	return &c, nil
}

func saveCredentials(path string, c *Credentials) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
