# 三角洲行动每日密码（DeltaForceDailySecret）

一个用 Go 编写的 Windows 桌面小工具：自动获取《三角洲行动》每日地图密码，并以桌面右上角的悬浮窗展示。

纯 Win32 API 实现，无第三方依赖，常驻内存约 35 MB。

## 功能

- **每日密码获取**：调用官方每日密码接口，展示各张地图对应的密码；成功获取后等到次日 00:05 自动刷新，失败则 30 分钟后重试。
- **两种登录方式**
  - 电脑版 QQ 本地快捷登录：自动探测本机已登录的 QQ（localhost 4301-4309 端口），无需扫码。
  - 手机扫码登录：生成 `qr.png` 二维码，用手机 QQ 扫码确认。
  - 登录凭据缓存到 `credentials.json`，接口失效时自动重新登录。
- **悬浮窗**
  - 置顶、无边框、圆角，固定在屏幕右上角，不占用任务栏。
  - 鼠标按住可拖动，不会抢走焦点。
  - 背景透明度可调（20-100），文字始终不透明。
- **托盘与设置**
  - 托盘菜单：显示/隐藏悬浮窗、打开设置、打开控制台、退出。
  - 设置窗口：开机自启、显示/隐藏快捷键、跟随程序、背景透明度。
  - 全局快捷键默认 `Ctrl+Alt+D`，支持自定义（录制时暂停原快捷键，避免冲突）。
- **随游戏自动显示/隐藏**
  - 通过注册表（`Uninstall` 项）自动定位《三角洲行动》安装目录与主程序，游戏启动时显示悬浮窗，退出后隐藏。
  - 自动识别失败时可手动填写进程名作为兜底。
- **静默启动**：以 GUI 子系统编译，双击启动不弹命令行窗口；需要日志时可在托盘菜单打开控制台。

## 使用方法

直接双击 `deltaforce-secret.exe`，程序会在屏幕右上角显示悬浮窗，并在托盘创建图标。

首次运行若没有可用凭据，会优先尝试电脑版 QQ 快捷登录；若未检测到已登录的 QQ，则改为生成二维码等待手机扫码（二维码保存在程序目录的 `qr.png`，同时会在控制台输出提示）。

### 托盘菜单

| 菜单项 | 说明 |
| --- | --- |
| 显示 / 隐藏悬浮窗 | 与全局快捷键等价 |
| 打开设置 | 打开设置窗口 |
| 打开控制台 | 附加一个控制台查看运行日志 |
| 退出 | 结束程序 |

### 设置项

| 设置项 | 说明 |
| --- | --- |
| 开机自启 | 写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` |
| 显示/隐藏悬浮窗快捷键 | 点击「更改」后按下新的组合键，需包含 Ctrl / Alt / Shift，Esc 取消 |
| 自动识别三角洲行动主程序 | 通过注册表自动识别并跟随游戏进程（推荐） |
| 手动进程名 | 自动识别失败时使用的进程名，默认为 `DeltaForceClient-Win64-Shipping.exe` |
| 悬浮窗背景不透明度 | 20-100，仅影响背景，文字不受影响 |

设置保存在程序目录的 `settings.json`。未手动指定进程名时，默认跟随 `DeltaForceClient-Win64-Shipping.exe` 的启动与退出。

### 命令行参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-overlay` | `true` | 以悬浮窗显示；`-overlay=false` 时仅在控制台输出 |
| `-openid` | | 直接指定 openid，需配合 `-token`，跳过登录 |
| `-token` | | 直接指定 access_token，需配合 `-openid` |
| `-login` | `false` | 强制重新登录 |
| `-qrcode` | `false` | 跳过电脑版 QQ 快捷登录，强制手机扫码 |
| `-uin` | | 快捷登录时指定使用的 QQ 号 |
| `-cred` | `credentials.json` | 凭据缓存文件路径 |
| `-qr` | `qr.png` | 二维码图片保存路径 |

示例：

```powershell
# 只在控制台输出一次结果
.\deltaforce-secret.exe -overlay=false

# 强制重新扫码登录
.\deltaforce-secret.exe -login -qrcode
```

## 构建方法

环境要求：

- Windows 10/11（x64）
- Go 1.26 或更高版本
- 不需要 CGO，不需要额外的编译工具链

```powershell
# 1. 拉取代码
git clone https://github.com/FrandreJoestar/DeltaForceDailySecret.git
cd DeltaForceDailySecret

# 2. 编译（GUI 子系统，双击不弹黑框）
go build -ldflags "-H windowsgui" -o deltaforce-secret.exe .

# 可选：调试版本，保留控制台便于看日志
go build -o deltaforce-secret-debug.exe .
```

交叉编译（在非 Windows 机器上构建 Windows 版本）：

```bash
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o deltaforce-secret.exe .
```

## 依赖库

**Go 标准库**（无任何第三方模块，`go.mod` 中没有 require 项）：

`net/http`、`crypto/tls`、`encoding/json`、`flag`、`os`、`path/filepath`、`runtime`、`sync`、`sync/atomic`、`syscall`、`time`、`unsafe`、`io/fs`、`log`、`strconv`、`strings`、`fmt`

**Windows 系统 DLL**（通过 `syscall.NewLazyDLL` 直接调用，随系统提供，无额外运行库）：

| DLL | 用途 |
| --- | --- |
| `user32.dll` | 窗口创建、消息循环、托盘回调、全局快捷键、分层窗口、`SetWindowPos` 等 |
| `gdi32.dll` | DIB 位图、绘图对象、圆角区域 |
| `gdiplus.dll` | 文字抗锯齿渲染（GDI+ 单独图层后再与背景合成，保证文字不透明） |
| `shell32.dll` | `Shell_NotifyIconW` 托盘图标 |
| `advapi32.dll` | 注册表读写（开机自启、识别游戏安装信息） |
| `kernel32.dll` | 进程快照、控制台附加、模块句柄 |

## 项目结构

```
main.go                // 命令行入口、参数解析、凭据与登录调度
auth.go                // QQ 快捷登录与扫码登录，换取 openid / access_token
secret.go              // 每日密码接口调用与结果解析、控制台输出
overlay_windows.go     // 悬浮窗：分层窗口、逐像素 alpha 渲染、消息循环
tray_windows.go        // 托盘图标、托盘菜单、设置窗口与快捷键录制
settings_windows.go    // 配置读写、开机自启、跟随进程监视
gamedetect_windows.go  // 通过注册表识别三角洲行动主程序
console_windows.go     // 无窗口启动与按需打开控制台
```

## 说明与注意

- 登录凭据 `credentials.json` 与本地配置 `settings.json` 均在程序目录生成，其中包含 access_token，请勿提交到公开仓库（仓库已通过 `.gitignore` 排除）。
- 密码数据来自官方接口，最终以游戏内显示为准；接口若调整可能导致获取失败，程序会在悬浮窗中提示错误并定时重试。
- 程序仅支持 Windows，悬浮窗与托盘均为 Win32 原生实现。
- 本项目仅供学习与个人使用，与游戏官方无关。
