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

首次运行若没有可用凭据，会优先尝试电脑版 QQ 快捷登录；若未检测到已登录的 QQ，则改为生成二维码等待手机扫码（二维码图片、配置、凭据都保存在 `%LOCALAPPDATA%\DeltaForceDailySecret\`，见下文）。

### 数据文件位置

| 文件 | 位置 |
| --- | --- |
| 配置 | `%LOCALAPPDATA%\DeltaForceDailySecret\settings.json` |
| 登录凭据 | `%LOCALAPPDATA%\DeltaForceDailySecret\credentials.json` |
| 二维码 | `%LOCALAPPDATA%\DeltaForceDailySecret\qr.png` |
| 日志 | `%LOCALAPPDATA%\DeltaForceDailySecret\logs\`（该位置建不出来时自动退回程序目录下的 `logs\`） |

放在用户目录是为了**不需要管理员权限也能写入**：程序装在 `Program Files`、或者放在启用了「受控文件夹访问」的桌面时，往程序目录写文件会被拒绝。

> 兼容旧版本：如果程序目录下已经存在 `settings.json` / `credentials.json`，程序会继续使用那一份，不会自动搬家、不会丢配置。

### 管理员权限

**默认只在必要的时候提权**，正常环境不打扰用户：

| 启动方式 | 行为 |
| --- | --- |
| 资源管理器双击（Medium + 非受限令牌） | 以当前身份运行，无 UAC |
| 被沙盒 / 安全软件「安全运行」拉起（受限令牌或 Low 完整性） | **自动**请求管理员权限，拿到干净令牌 |
| `-elevate=true` | 强制提权 |
| `-elevate=false` | 强制不提权 |

判断依据是 `IsTokenRestricted` 和进程完整性级别：受限令牌的进程写不了 `%LOCALAPPDATA%`（即使 ACL 给了 FullControl —— 受限 SID 没被授权就会拒绝），也加不上托盘图标，属于"跑起来但残废"，所以自动换成提权后的干净令牌。

- 提权失败（例如在 UAC 里点了「否」）会弹框提示并退出，不会以半残状态运行。
- 提权后 `Local\` 命名互斥体的单实例检查才生效，重复启动只会唤醒已有悬浮窗。
- 诊断模式 `-overlay=false` 和 `-doctor` 永远不提权。

> **检测游戏进程本身不需要管理员权限。** `CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS)` 枚举进程名、读取 `HKLM\...\Uninstall` 都是标准用户就能做的，`-doctor` 在未提权状态下可以完整跑完整个识别流程并打印每步结果。所以"普通身份启动时识别不到游戏"不是权限问题，请跑 `-doctor` 看具体是哪一步没匹配上。

> 自动提权会导致每次启动弹一次 UAC（只在受限环境里）。想要"无提示开机自启"，可以取消程序内的开机自启，改用「任务计划程序」新建一个登录时触发的任务并勾选「使用最高权限运行」。

### 托盘菜单

| 菜单项        | 说明            |
| ---------- | ------------- |
| 显示 / 隐藏悬浮窗 | 与全局快捷键等价      |
| 打开设置       | 打开设置窗口        |
| 打开控制台      | 附加一个控制台查看运行日志 |
| 退出         | 结束程序          |

**右键点击悬浮窗本体也能打开同一个菜单** —— 托盘图标因为权限/任务栏异常加不上时，这是唯一的操作入口（悬浮窗右下角的提示也写着「右键菜单 · 按住拖动」）。

### 设置项

| 设置项          | 说明                                                                                  |
| ------------ | ----------------------------------------------------------------------------------- |
| 开机自启         | 写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`                             |
| 显示/隐藏悬浮窗快捷键  | 点击「更改」后按下新的组合键，需包含 Ctrl / Alt / Shift，Esc 取消                                        |
| 自动识别三角洲行动主程序 | 通过注册表自动识别并跟随游戏进程（推荐）                                                                |
| 手动进程名        | 自动识别失败时使用的进程名，默认为 `DeltaForceClient-Win64-Shipping.exe`；名称含空格也可以，支持粘贴完整路径或省略 `.exe` |
| 悬浮窗背景不透明度    | 20-100，仅影响背景，文字不受影响                                                                 |
| 悬浮窗位置        | 按住悬浮窗拖动即可移动，位置会自动记住（写入 `settings.json` 的 `overlay_x` / `overlay_y`）                          |

设置保存在 `%LOCALAPPDATA%\DeltaForceDailySecret\settings.json`。未手动指定进程名时，默认跟随 `DeltaForceClient-Win64-Shipping.exe` 的启动与退出。

### 单实例与日志

- **单实例**：程序只允许运行一份。重复启动时不会出现第二个悬浮窗，而是把已有悬浮窗唤出来（并在 15 秒内不会被"跟随游戏进程"的逻辑自动隐藏）。
- **日志**：始终写入 `%LOCALAPPDATA%\DeltaForceDailySecret\logs\deltaforce-secret-YYYY-MM-DD.log`，按天滚动，保留 7 天。GUI 模式下没有控制台，出问题时先看这个文件。
- **控制台回放**：托盘菜单「打开控制台」会把内存中保留的最近 300 条日志先打印出来，再接续实时输出，所以事后打开也能看到之前发生的错误。

### 命令行参数

| 参数         | 默认值                | 说明                               |
| ---------- | ------------------ | -------------------------------- |
| `-overlay` | `true`             | 以悬浮窗显示；`-overlay=false` 时仅在控制台输出 |
| `-doctor`  | `false`            | 打印游戏进程/安装识别诊断后退出，**不需要管理员权限**，见下文 |
| `-elevate` | 自动               | 不写=仅在受限/降权环境自动提权；`true` 强制提权，`false` 强制不提权 |
| `-openid`  | <br />             | 直接指定 openid，需配合 `-token`，跳过登录    |
| `-token`   | <br />             | 直接指定 access\_token，需配合 `-openid` |
| `-login`   | `false`            | 强制重新登录                           |
| `-qrcode`  | `false`            | 跳过电脑版 QQ 快捷登录，强制手机扫码             |
| `-uin`     | <br />             | 快捷登录时指定使用的 QQ 号                  |
| `-cred`    | `%LOCALAPPDATA%\...\credentials.json` | 凭据缓存文件路径 |
| `-qr`      | `%LOCALAPPDATA%\...\qr.png`           | 二维码图片保存路径 |

示例：

```powershell
# 只在控制台输出一次结果
.\deltaforce-secret.exe -overlay=false

# 强制重新扫码登录
.\deltaforce-secret.exe -login -qrcode

# 识别不到游戏时先跑这个，把输出发给作者
.\deltaforce-secret.exe -doctor
```

### 识别不到游戏怎么办

程序按三步确定"游戏是否在运行"：

1. 从注册表 `Uninstall` 项里找《三角洲行动》的安装记录，解析主程序路径；
2. 把**当前所有运行中的进程**和命名特征比对（名字里同时出现 delta 与 force，且不是启动器/反作弊/崩溃上报）；
3. 前两步都没结果时，再退回内置候选名 + 设置里的手动进程名。

第 2 步是兜底：不同发行渠道（WeGame / Steam / Epic）的主程序名不一样，客户端升级也可能改名。如果还是识别不到，直接跑：

```powershell
.\deltaforce-secret.exe -doctor
```

输出里会列出：是否具备管理员权限、找到的注册表安装项、候选根目录、**当前所有名字含 delta 的进程**、以及最终跟随目标及其运行状态。这份输出不含任何凭据，可以放心发给别人排障。

如果游戏正在运行但「名字含 delta 的进程」为空，报告里还会附上当前全部进程名，从里面找到游戏主程序后，填到设置的「手动进程名」即可。

## 构建方法

环境要求：

```powershell
# 1. 拉取代码
git clone https://github.com/FrandreJoestar/DeltaForceDailySecret.git
cd DeltaForceDailySecret

# 2. 编译（GUI 子系统，双击不弹黑框）
go build -ldflags "-H windowsgui" -o deltaforce-secret.exe .

# 可选：调试版本，保留控制台便于看日志
go build -o deltaforce-secret-debug.exe .
```

推荐用构建脚本编译，它会在链接前写入 Windows 版本资源（公司、产品名、文件版本），exe 属性里不再是空白信息：

```powershell
# 默认版本 1.0.0.0
powershell -ExecutionPolicy Bypass -File .\build.ps1

# 指定版本号
powershell -ExecutionPolicy Bypass -File .\build.ps1 -Version 1.2.0.0

# 调试版（保留控制台）
powershell -ExecutionPolicy Bypass -File .\build.ps1 -Console
```

脚本通过 `go run github.com/josephspurrier/goversioninfo` 生成 `resource_windows_amd64.syso` 再编译，首次运行需要联网下载该工具；下载失败时会给出警告并继续编译（只是不带版本信息）。

> 写入版本资源只影响“文件属性”里显示的发布者名称与版本号，**不会**消除 SmartScreen 的“未知发布者”提示——那需要商业代码签名证书，自签名证书对 SmartScreen 无效。

交叉编译（在非 Windows 机器上构建 Windows 版本）：

```bash
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o deltaforce-secret.exe .
```

建议再加 `-trimpath -s -w`：体积从约 9.9 MB 降到 7.0 MB，并去掉构建机上的源码路径。

> 当前的可执行文件是**运行时自动提权**（`ShellExecuteW` + `runas`），所以 `go build` 一条命令即可，不需要资源编译步骤。代价是 exe 图标不会带 UAC 盾牌、双击时会先起一个未提权的中间进程。如果想要"Windows 层面强制要求管理员"的效果，可以用 `rsrc` 之类的工具把下面这份清单编译成 `.syso` 一起链接：
>
> ```xml
> <?xml version="1.0" encoding="UTF-8" standalone="yes"?>
> <assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
>   <trustInfo xmlns="urn:schemas-microsoft-com:asm.v2">
>     <security>
>       <requestedPrivileges>
>         <requestedExecutionLevel level="requireAdministrator" uiAccess="false"/>
>       </requestedPrivileges>
>     </security>
>   </trustInfo>
>   <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
>     <application>
>       <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
>     </application>
>   </compatibility>
> </assembly>
> ```

## 常见问题

### 「Windows 已保护你的电脑 / 发布者未知」

这是 **SmartScreen**，不是杀毒报毒，也和权限无关：

> Windows 已保护你的电脑
> Microsoft Defender SmartScreen 阻止了无法识别的应用启动。
> 应用：deltaforce-secret.exe　发行者：发布者未知

它只在 exe 带 **Mark-of-the-Web**（从浏览器 / QQ / 微信下载来的）时触发，判定依据是"这个发行者有没有信誉"——而本程序没有代码签名，所以永远是「发布者未知」。命令行启动、自己本地编译出来直接运行的 exe 不会带 MOTW，所以不会弹。

三种处理：

```powershell
# 1. 双击弹窗里点「更多信息」→「仍要运行」（只对这一次有效）
# 2. 去掉文件的 MOTW 标记，之后永久不再弹
Unblock-File .\deltaforce-secret.exe
#    或者：右键 exe → 属性 → 常规 → 勾选「解除锁定」→ 确定
# 3. 用 7-Zip / WinRAR 解压压缩包（第三方解压工具通常不会传播 MOTW）
```

要彻底、体面地解决只能**代码签名**：OV 证书需要积累信誉，EV 证书立刻获得 SmartScreen 信任。自签名证书没用（不在受信任根里）。

### 托盘里看不到程序图标

先点任务栏右下角的 `^`（展开隐藏的图标）确认一下——Windows 默认把新出现的托盘图标放进折叠区，把它们拖到任务栏上就会常驻。

如果折叠区里也没有，跑一次自检：

```powershell
.\deltaforce-secret.exe -doctor
```

关注这几行：

| 输出 | 含义 | 处理 |
| --- | --- | --- |
| `进程完整性级别：Low（受限 / 沙盒）` | 程序被沙盒/受限启动器降权运行 | 换正常方式启动（不要经过沙箱类工具） |
| `explorer.exe 完整性级别：High（管理员）`，或 `读不到` | **资源管理器是以管理员身份运行的** | 见下面「重启资源管理器」 |
| `托盘图标自检：结果：成功添加并已移除` | 当前身份能加图标 | 那就是被折叠进 `^` 了 |
| `结果：失败 —— … Access is denied` | 被 UIPI 拦了 | 按上面两行定位是哪一侧权限更高 |

**为什么会出现"必须以管理员运行才有托盘图标"**：Windows 的 UIPI（用户界面特权隔离）只允许消息从**低完整性级别发往不高于自己**的进程。托盘图标本质上是把你的窗口句柄通过 WM_COPYDATA 告诉资源管理器，所以：

- 程序 Medium + 资源管理器 Medium → 正常（绝大多数情况）
- 程序 Medium + 资源管理器 **High** → 被拦，`Shell_NotifyIcon` 返回 `Access is denied`，托盘里永远没有图标
- 程序 **Low**（被沙盒/受限启动器拉起来）+ 资源管理器 Medium → 同样被拦
- 程序 High（管理员）+ 资源管理器 High/Medium → 能通（高可以往下发）

进程的完整性级别**继承自父进程**，所以真正的判据是"谁把你拉起来的"。程序每次启动都会在日志里写一行：

```
启动：管理员=false；本进程=Medium（普通用户），explorer.exe=Medium（普通用户），父进程=explorer.exe
```

- `父进程=explorer.exe` + 两边都 Medium + `受限令牌=false` → 托盘理应正常，若仍失败请看日志里的具体错误；
- `本进程=Low` 或 `受限令牌=true` → 是被沙盒/受限启动器拉起来的（这类令牌连用户目录都写不进去），换正常方式启动；
- `explorer.exe=读不到` 或 `High` → 资源管理器本身是管理员权限，见下面「重启资源管理器」。

重启资源管理器到正常级别（三选一）：

```text
① 注销并重新登录（最干净）
② 用【非管理员】的命令提示符：taskkill /f /im explorer.exe && start explorer.exe
③ 任务管理器（确保不是以管理员身份打开的）→ 详细信息 → 选中 explorer.exe → 重新启动
```

修好之后就不需要 `-elevate=true` 了。

如果两侧级别一致、自检也成功，但运行程序时仍然看不到图标，看日志（`%LOCALAPPDATA%\DeltaForceDailySecret\logs\`）：

- `托盘图标已添加` → 加上了，属于被折叠/被隐藏；
- `添加托盘图标失败（…），稍后重试` → 程序会每 2 秒重试、最多 30 次（开机时任务栏还没就绪很常见）；
- `托盘图标连续 30 次添加失败` → 任务栏进程（`explorer.exe`）没在运行。

即使暂时没有托盘图标，全局快捷键（默认 `Ctrl+Alt+D`）仍可显示/隐藏悬浮窗。

### 杀毒软件报毒

程序本身不做任何可疑的事，但下面这些行为组合在一起，命中杀软启发式规则的概率并不低：

| 行为 | 触发点 |
| --- | --- |
| 未签名的单文件 exe、没有版本信息 | 全部 |
| 写入 `HKCU\...\Run` 开机自启 | 设置里的「开机自启」 |
| 创建置顶、无边框、不可激活的隐藏窗口 | 悬浮窗 |
| 枚举系统进程（`CreateToolhelp32Snapshot`） | 跟随游戏进程 |
| 访问 `localhost:4301-4309` 并关闭证书校验（QQ 本地端口是自签证书） | 电脑版 QQ 快捷登录 |
| 收集 `openid` / `access_token` 并请求腾讯接口 | 登录与取数 |
| 扫描注册表 Uninstall 项、遍历游戏安装目录 | 自动识别主程序 |

排查顺序：

1. 先确认是谁拦的：Windows 安全中心 →「保护历史记录」；或 `Get-MpThreatDetection`。
2. 如果是「智能应用控制」（Win11）或 SmartScreen 的"未知发布者"，那是**没有签名/没有信誉**，提权解决不了。
3. 如果是实时保护报毒，把文件名和检测名 submit 到 <https://www.microsoft.com/wdsi/filesubmission> 报误报，通常 1–3 天会修正。
4. 长期方案是给 exe 做代码签名（OV/EV 证书）；签名同时能解决 SmartScreen 和"每次升级都被拦"的问题。

## 依赖库

**Go 标准库**（无任何第三方模块，`go.mod` 中没有 require 项）：

`net/http`、`crypto/tls`、`encoding/json`、`flag`、`os`、`path/filepath`、`runtime`、`sync`、`sync/atomic`、`syscall`、`time`、`unsafe`、`io`、`io/fs`、`log`、`math`、`strconv`、`strings`、`fmt`

**Windows 系统 DLL**（通过 `syscall.NewLazyDLL` 直接调用，随系统提供，无额外运行库）：

| DLL            | 用途                                         |
| -------------- | ------------------------------------------ |
| `user32.dll`   | 窗口创建、消息循环、托盘回调、全局快捷键、分层窗口、`SetWindowPos` 等 |
| `gdi32.dll`    | DIB 位图、绘图对象                               |
| `gdiplus.dll`  | 文字抗锯齿渲染（GDI+ 单独图层后再与背景合成，保证文字不透明）          |
| `shell32.dll`  | `Shell_NotifyIconW` 托盘图标                   |
| `advapi32.dll` | 注册表读写（开机自启、识别游戏安装信息）                       |
| `kernel32.dll` | 进程快照、控制台附加、模块句柄、单实例互斥体                     |

**仅构建期使用**：`github.com/josephspurrier/goversioninfo`（由 `build.ps1` 通过 `go run` 调用，只用于生成版本资源 `.syso`，不会进入 `go.mod`，也不是运行期依赖）。

## 项目结构

```
main.go                   // 命令行入口、参数解析、凭据与登录调度
auth.go                   // QQ 快捷登录与扫码登录，换取 openid / access_token
secret.go                 // 每日密码接口调用与结果解析、控制台输出
overlay_windows.go        // 悬浮窗：分层窗口、逐像素 alpha 渲染、位置记忆、消息循环
tray_windows.go           // 托盘图标、托盘菜单、设置窗口与快捷键录制
settings_windows.go       // 配置读写（原子写入）、开机自启、跟随进程监视
gamedetect_windows.go     // 通过注册表识别三角洲行动主程序
console_windows.go        // 无窗口启动与按需打开控制台
logging_windows.go        // 日志：内存环形缓冲 + 按天滚动的文件日志 + 控制台回放
singleinstance_windows.go // 单实例互斥体，重复启动时唤醒已有悬浮窗
build.ps1                 // 构建脚本：生成版本资源后编译
versioninfo.json          // 版本资源模板（公司、产品名、版本号）
```

