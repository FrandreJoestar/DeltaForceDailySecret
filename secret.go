package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const dailySecretURL = "https://comm.ams.game.qq.com/ide/"

// Secret 是某张地图当天的密码
type Secret struct {
	MapName string `json:"mapName"`
	Secret  string `json:"secret"`
}

type secretResponse struct {
	IRet  json.RawMessage `json:"iRet"`
	SMsg  string          `json:"sMsg"`
	JData json.RawMessage `json:"jData"`
}

// jData 结构：{"data":{"code":0,"data":{"list":[...]},"msg":"ok"}}
type secretPayload struct {
	Data struct {
		Code json.RawMessage `json:"code"`
		Data struct {
			List []Secret `json:"list"`
		} `json:"data"`
		Msg string `json:"msg"`
	} `json:"data"`
}

// fetchDailySecret 调用 dfm/center.day.secret 接口获取每日密码
func fetchDailySecret(cred *Credentials) ([]Secret, error) {
	q := url.Values{}
	q.Set("iChartId", "316969")
	q.Set("iSubChartId", "316969")
	q.Set("sIdeToken", "NoOapI")
	q.Set("method", "dfm/center.day.secret")
	q.Set("source", "2")
	q.Set("param", "{}")

	req, err := http.NewRequest(http.MethodPost, dailySecretURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", fmt.Sprintf("openid=%s; acctype=qc; appid=%s; access_token=%s", cred.OpenID, appID, cred.AccessToken))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求每日密码接口失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取每日密码响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("请求每日密码接口失败: HTTP %d, %s", resp.StatusCode, body)
	}

	var top secretResponse
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("解析每日密码响应失败: %w, 原始响应: %s", err, body)
	}
	if ret := codeString(top.IRet); ret != "0" {
		msg := top.SMsg
		if msg == "" {
			msg = string(body)
		}
		return nil, fmt.Errorf("接口返回错误 iRet=%s: %s", ret, msg)
	}

	jData := top.JData
	// jData 可能是对象，也可能是被序列化成字符串的 JSON
	if len(jData) > 0 && jData[0] == '"' {
		var inner string
		if err := json.Unmarshal(jData, &inner); err == nil {
			jData = json.RawMessage(inner)
		}
	}

	var payload secretPayload
	if err := json.Unmarshal(jData, &payload); err != nil {
		return nil, fmt.Errorf("解析每日密码数据失败: %w, 原始响应: %s", err, body)
	}
	if code := codeString(payload.Data.Code); code != "0" {
		return nil, fmt.Errorf("获取每日密码失败 code=%s: %s, 原始响应: %s", code, payload.Data.Msg, body)
	}
	if len(payload.Data.Data.List) == 0 {
		return nil, errors.New("今日暂无每日密码数据")
	}
	return payload.Data.Data.List, nil
}

// codeString 把可能是数字或字符串的状态码统一成字符串
func codeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

// printSecrets 按地图名对齐输出每日密码
func printSecrets(list []Secret) {
	fmt.Printf("三角洲行动 每日密码（%s）\n", time.Now().Format("2006-01-02"))
	fmt.Println(strings.Repeat("-", 36))

	width := 0
	for _, s := range list {
		if w := displayWidth(s.MapName); w > width {
			width = w
		}
	}
	for _, s := range list {
		pad := strings.Repeat(" ", width-displayWidth(s.MapName)+2)
		fmt.Printf("%s%s%s\n", s.MapName, pad, s.Secret)
	}
}

// displayWidth 计算终端显示宽度，中文等全角字符按 2 列计算
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r > 127 {
			w += 2
		} else {
			w++
		}
	}
	return w
}
