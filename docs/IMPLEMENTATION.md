# 划词翻译 · 详细实施技术方案

配套：`docs/magpie-select-translate.md`（架构取舍）、`docs/IMPLEMENT-PROMPT.md`（提示词）
目标仓库：`D:\work\code\ex\vibecoding\pro2`
平台：Windows 11 / amd64 / Go 1.21+ / 网关 `http://127.0.0.1:3425`

---

## 1. 交付物与文件树

```
pro2/
├─ cmd/helper/main.go            # 入口：参数、单实例、热键循环、服务启动、浮窗生命周期   ~220 行
├─ internal/pick/
│   ├─ pick.go                   # 取词编排：黑名单 → 剪贴板法（v1）→ UIA（v1.1）        ~150 行
│   ├─ clipboard.go              # 剪贴板全量存取 + 还原                                ~180 行
│   └─ uia_windows.go            # UIA COM 调用（可选，v1.1）                            ~260 行
├─ internal/translate/
│   ├─ gateway.go                # 网关地址发现（settings.json / MAGPIE_ADDR / 探测）     ~90 行
│   ├─ stream.go                 # 请求 + SSE 解析 + single-flight                        ~140 行
│   ├─ prompt.go                 # system 常量 + user 组装 + 语言判定                     ~80 行
│   └─ cache.go                  # LRU 512                                            ~70 行
├─ internal/popover/popover.go   # WebView2 窗口：创建/定位/DPI/显示隐藏/键盘             ~230 行
├─ internal/api/server.go        # /api/ping /api/translate /api/settings / (UI)          ~160 行
├─ internal/store/settings.go    # settings.json 读写 + 默认值                            ~80 行
├─ web/index.html + app.js + style.css  # 浮窗 UI（go:embed）                             ~340 行
├─ plugin/package.json · index.mjs · page/                                               # magpie 插件包
├─ scripts/install.ps1 · uninstall.ps1 · start-helper.vbs                                # 安装/自启
└─ docs/*
```

合计约 **2000 行**，其中 Go 1600 / 前端 340 / 脚本 60。

---

## 2. 技术选型

| 需要 | 选型 | 理由 / 替代 |
|---|---|---|
| 语言 | **Go 1.21**（单文件 exe，无运行时） | 与 magpie 同栈；C# 需装 runtime，Node 需 Bun |
| Win32 调用 | `golang.org/x/sys/windows` + `NewLazySystemDLL` | 标准做法，不需要 cgo |
| 浮窗 | **WebView2**（`github.com/jchv/go-webview2`，win32 变体） | 省掉自绘文本排版；Win11 自带 Runtime |
| 托盘 | 无（v1 不做托盘） | 用启动文件夹 + 任务管理器关；减少 200 行 |
| UIA 取词 | `go-ole` 手写 vtable（**v1.1 才做**） | v1 用剪贴板法，见 §4.2 |
| HTTP | `net/http` + `http.Flusher` | 零依赖 |
| 缓存 | `container/list` + map | 不引第三方 LRU |

**为什么不引 gin/echo**：3 个路由，`http.ServeMux` 够。

---

## 3. 进程与线程模型

```
main (runtime.LockOSThread)
 ├─ 解析参数 → 加载 settings.json → 派生默认值
 ├─ 单实例锁（127.0.0.1:3477 绑定失败即退出；另加 --port 顺延 3478/3479/3480）
 ├─ go api.Serve()             # net/http，127.0.0.1 only
 ├─ go popover.Run(wvCh)       # WebView2：必须固定在一个 OS 线程，创建一次复用
 ├─ RegisterHotKey(Alt+D)      # hwnd=0 → WM_HOTKEY 进本线程消息队列
 └─ GetMessage 循环            # 主线程
       ├─ WM_HOTKEY  → go onTrigger(cursor)
       ├─ WM_APP+1   → 页面请求"关闭浮窗"
       └─ WM_QUIT
```

要点：
- **WebView2 只创建一次**，之后 `ShowWindow(SW_SHOWNOACTIVATE)` / `SetWindowPos` / `ShowWindow(SW_HIDE)`。每次重建要 ~300ms，不能接受。
- 取词/翻译都在 goroutine，**绝不在消息循环里做 IO**。
- 页面与 Go 的通道只有一个方向：Go → 页面 `Eval("window.onPicked({...})")`；反向走 HTTP（页面 fetch 本地 3477）。

---

## 4. 取词实现

### 4.1 触发

```go
// 热键：默认 Alt+D。modAlt=0x0001, vk='D'=0x44, id 随便固定 0x4D50
r, _, _ := procRegisterHotKey.Call(0, 0x4D50, 0x0001, 0x44)
```

可选"划词即弹"（默认关）：`SetWindowsHookEx(WH_MOUSE_LL)`，回调里只做轻量判断后 `PostMessage(WM_APP+2)`，不做事。

```
DOWN: 记 (pt, t)
UP:   if |pt-up|>4 && up-t<1500ms && 前台进程不在黑名单 → 触发
```

> 钩子回调里禁止任何 IO/COM，否则系统全局输入卡顿。

### 4.2 v1 取词：剪贴板法（全覆盖，30 行逻辑）

```go
func Selection(hwndTarget uintptr) (string, error) {
    seq := GetClipboardSequenceNumber()
    releaseModifiers()            // ★ 关键：先把 Alt/Ctrl/Shift/Win 全部 keyup
    sendCtrlC()
    if txt, ok := waitClipboardChange(seq, 400*time.Millisecond); ok {
        return txt, nil
    }
    return "", ErrNoSelection
}
```

`releaseModifiers` 必须做：用户按 `Alt+D` 时 Alt 还按着，此时合成 `Ctrl+C` 会变成 `Ctrl+Alt+C`，很多 app 不认。

`waitClipboardChange` 用 `GetClipboardSequenceNumber` 轮询（20ms 间隔，上限 400ms），比固定 sleep 快 3~5 倍，也避免"内容没变被当成取词成功"：

```go
for deadline.After(time.Now()) {
    if GetClipboardSequenceNumber() != seq {
        if s, err := readUnicodeText(); err == nil && s != "" { return s, true }
    }
    time.Sleep(20 * time.Millisecond)
}
```

安全边界（**必须做，否则会丢用户数据**）：
1. `OpenClipboard` 最多重试 5 次 × 20ms（别的进程可能占着）。
2. 保存**所有 `HGLOBAL` 型格式**（`EnumClipboardFormats` → `GetClipboardData` → `GlobalSize/GlobalLock` 复制字节）+ `CF_UNICODETEXT`。
3. 还原在 `defer`：`EmptyClipboard()` → 逐格式 `GlobalAlloc(GMEM_MOVEABLE)` + `GlobalLock/memcpy` + `SetClipboardData`。
4. 非 HGLOBAL 型（`CF_BITMAP`、`CF_ENHMETAFILE`、私有格式里带句柄的）不还原。
   `// shortcut: 图片/图元文件型剪贴板不还原，用户复制图片后划词翻译会丢图；需要时改为 DuplicateHandle + 全格式还原`

黑名单（默认）：`mstsc.exe, WindowsTerminal.exe, conhost.exe, openconsole.exe, KeePass.exe, 1Password.exe, Bitwarden.exe, explorer.exe`（explorer 里 Ctrl+C 是复制文件）。命中则**直接返回"不支持该应用"**，不尝试任何发送。

### 4.3 v1.1 取词：UIA（可选升级）

收益：不碰剪贴板、终端里也能用、无 Alt 键干扰。
代价：Electron/部分 app 无文本模式；跨进程调用可能挂 200ms~2s。

调用序列（`go-ole` + `NewLazyDLL("uiautomationcore.dll")`，或直接 `ole.CreateInstance`）：

```
CLSID_CUIAutomation = {FF48DBA4-60EF-4201-AA87-54103EEF594E}
1. CoInitializeEx(APARTMENTTHREADED)
2. NewIUIAutomation → ElementFromPoint({x,y})  → IUIAutomationElement
3. loop ≤6 层:
     el.GetCurrentPattern(UIA_TextPatternId=10014) → ITextPattern
     rng = GetSelection(); if rng != nil → rng.GetText(-1) 非空即命中
     el = el.GetCurrentPattern(UIA_TreeWalkerId?) / 用 ControlViewWalker.GetParentElement
4. 失败 → 返回 ErrNoSelection → 上层降级剪贴板法
```

**索引与 IID 必须现场校准**，不要凭记忆写死：先写一个 `cmd/vtableprobe`，用 `ole.QueryInterface` 拿到接口后 dump 前 40 个函数指针地址 + 用 `GetProcAddress` 对照导出名，把正确的 vtable 偏移写进常量。文档里不预设索引值。

UIA 调用统一包一层超时：

```go
func withTimeout(d time.Duration, f func() (string, error)) (string, error) {
    ch := make(chan struct{ s string; e error }, 1)
    go func() { s, e := f(); ch <- struct{ s string; e error }{s, e} }()
    select {
    case r := <-ch:  return r.s, r.e
    case <-time.After(d): return "", ErrUIATimeout   // goroutine 泄漏 1 个，可接受
    }
}
```

### 4.4 后处理

| 规则 | 值 |
|---|---|
| 去空白 | `strings.TrimSpace`，去掉中文字符间换行造成的多余空格除外 |
| 最短 | `< 2` 字符不翻 |
| 最长 | `> 5000` 字符截断并提示 |
| 去重 | 同文本（sha1）1s 内不重复 |

---

## 5. 翻译链路

### 5.1 网关发现（`gateway.go`）

优先级：

1. 命令行 `--gateway`
2. 环境变量 `MAGPIE_ADDR`（`host:port` → `http://host:port`）
3. `<configDir>/settings.json` 里的 `port`；`configDir` 依次找 `%MAGPIE_CONFIG_DIR%`、`%APPDATA%\magpie`、`<exe 目录>\data`
4. `http://127.0.0.1:3425`

逐个 `GET /v1/models`（2.5s 超时）探测，第一个 200 的即为 base；结果缓存 60s（`sync.Mutex` + 时间戳）。
鉴权：`MAGPIE_API_KEY` / `MAGPIE_GATEWAY_KEY` → 否则读 `caller-keys.json` 首个 `secret` → 都没有就不带 header（桌面版正常可用）。

### 5.2 请求与 SSE

```go
body := map[string]any{
    "model": model, "stream": true, "temperature": 0,
    "messages": []map[string]string{
        {"role": "system", "content": systemPrompt(target)},
        {"role": "user",   "content": userPrompt(text, glossary)},
    },
}
```

解析（`bufio.Scanner`，buffer 1MB）：

```
逐行 line：
  跳过空行、以 ":" 开头的心跳、以 "event:" 开头的行
  去掉前缀 "data: "
  "data: [DONE]" → 结束
  json.Unmarshal → choices[0].delta.content（**忽略 delta.reasoning_content**）
  非空 → onDelta(s)，flush 给前端
```

细节：
- 上游返回非 200：读前 512 字节 body 作为错误摘要，映射成用户文案（§9）。
- 流中断（read error 且已收到内容）→ 保留已有内容，标记 `interrupted: true`。
- 超时 55s（`context.WithTimeout`），新请求先 `cancel()` 旧的（`atomic.Pointer[context.CancelFunc]`）。

### 5.3 Prompt（**逐字节稳定**，保护前缀缓存）

`prompt.go` 里两个常量，只有目标语言不同：

```go
const sysToZH = `You are a translation engine. Translate the user's text into Simplified Chinese.
Output only the translation. Preserve Markdown, code blocks, inline code, URLs, emails,
numbers, units and placeholders ({{}}, %s, {0}) exactly. Keep proper nouns unchanged.
Do not explain, do not add notes, do not wrap the answer in quotes.`

const sysToEN = `You are a translation engine. Translate the user's text into English.
Output only the translation. Preserve Markdown, code blocks, inline code, URLs, emails,
numbers, units and placeholders ({{}}, %s, {0}) exactly. Keep proper nouns unchanged.
Do not explain, do not add notes, do not wrap the answer in quotes.`
```

user 侧**拼接顺序固定**（术语表/上下文都放这里，绝不进 system）：

```
[term] 原文=译文
[term] …
[/term]
<待翻译文本>
```

> 前缀缓存命中的前提是 system（+ 前面消息）字节完全一致。上面前缀 `[term]…` 只有用户配了术语表才出现；一旦出现，缓存命中率下降，属可接受代价。

### 5.4 语言判定（`prompt.go`）

```go
var (
  reCJK, _   = regexp.Compile(`[\x{4e00}-\x{9fff}\x{3400}-\x{4dbf}]`)
  reKana, _  = regexp.Compile(`[\x{3040}-\x{30ff}]`)
  reHangul,_ = regexp.Compile(`[\x{ac00}-\x{d7af}]`)
  reCyr, _   = regexp.Compile(`[\x{0400}-\x{04ff}]`)
  reLatin,_  = regexp.Compile(`[A-Za-z]`)
)
```
判定表：

| 条件 | target |
|---|---|
| CJK 且无 Latin | `en` |
| Kana / Hangul / Cyrillic / 其他非拉丁 | `zh` |
| 纯 Latin | `zh` |
| 用户 `--target` 指定了值 | 用指定值（强制） |

### 5.5 缓存（`cache.go`）

```go
key := sha1hex(text + "|" + target + "|" + model)
```
命中直接返回全文 + `cached: true`；容量 512，`container/list` LRU；只在内存，不落盘。

---

## 6. 浮窗（`popover.go` + `web/`）

### 6.1 关键决策：浮窗页面自己拉流

浮窗不是"Go 渲染然后推给页面"，而是：

```
Go 取词成功 → wv.Eval(`window.onPicked("...")`)
页面 → fetch('/api/translate', {method:POST, body}) → 自己读 ReadableStream 渲染
```

好处：Go 侧零增量渲染代码，服务器和 UI 同源（`http://127.0.0.1:3477/`），无 CORS、无 postMessage 桥。**省掉一整圈 IPC。**

### 6.2 窗口属性

```go
// 创建时
style   := WS_POPUP
exStyle := WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW | WS_EX_TOPMOST
// 显示时
ShowWindow(hwnd, SW_SHOWNOACTIVATE)   // ★ 不用 SW_SHOW，否则抢焦点
```

| 行为 | 作用 |
|---|---|
| `WS_EX_NOACTIVATE` | 弹出后用户的输入光标不动，能继续打字 |
| `WS_EX_TOOLWINDOW` | 不进 Alt+Tab |
| `WS_EX_TOPMOST` | 盖住全屏浏览器 |

**代价**：窗口拿不到键盘焦点 → `Esc` 关不掉。
解法：浮窗可见时临时挂 `WH_KEYBOARD_LL`，只拦 `VK_ESCAPE`（吞掉并 `WM_APP+1` 关闭），隐藏时立刻 `UnhookWindowsHookEx`。
`// shortcut: Esc 靠临时键盘钩子，只拦 Esc 一个键；若被安全软件报毒，改为点击浮窗外部关闭 + 右上角关闭按钮`

复制译文：页面按钮 → `POST /api/clipboard {text}` → Go `OpenClipboard/SetClipboardData`。
（不用 `navigator.clipboard`：无焦点窗口在 WebView2 里会被拒。）

### 6.3 定位与 DPI

```go
pt := cursorPos()
r  := rcWork(MonitorFromPoint(pt))
w, h := windowSize()                 // 对话框 420 宽，高度 = min(内容, 420)
x, y := pt.X+16, pt.Y+20

if x+w > r.Right  { x = pt.X - w - 16 }        // 右侧放不下 → 翻到左边
if y+h > r.Bottom { y = pt.Y - h - 20 }        // 下方放不下 → 翻到上方
if x < r.Left     { x = r.Left + 8 }
if y < r.Top      { y = r.Top + 8 }

// 逻辑像素 → 物理像素（WebView2 需要物理坐标）
scale := float64(GetDpiForWindow(hwnd)) / 96.0
SetWindowPos(hwnd, HWND_TOPMOST, x, y, int(float64(w)*scale), int(float64(h)*scale), SWP_NOACTIVATE)
```

高度自适应：页面渲染后 `POST /api/size {h}` → Go `SetWindowPos` 改高度（最多 3 次，避免反复抖动）。

### 6.4 页面结构（`web/index.html`）

```
┌──────────────────────────────┐
│  EN → 中文          [复制][×] │  ← 头部：语言对 + 操作
├──────────────────────────────┤
│  译文（流式逐字，可选中）      │  ← 主区，滚动
│                              │
├──────────────────────────────┤
│ 原文摘要（2 行，灰色可展开）   │  ← 折叠区
└──────────────────────────────┘
```

CSS 要点：
- 深/浅色跟随 `prefers-color-scheme`；
- `border-radius:12px` + `box-shadow` + 背景 `rgba(255,255,255,.96)`（窗口本身透明，靠 `DwmExtendFrameIntoClientArea` 或直接不透明窗口——**v1 用不透明窗口**，圆角靠 `SetWindowRgn`，省掉分层窗口的一堆坑）；
- 字号 14px，行高 1.6，`user-select: text` 允许复制。

---

## 7. 本地 HTTP API（`server.go`）

全部只绑 `127.0.0.1`；校验 `Host` 为 `127.0.0.1:<port>` 或 `localhost:<port>`（防浏览器 CSRF）。

| 方法 | 路径 | 入参 | 出参 |
|---|---|---|---|
| GET | `/api/ping` | — | `{"ok":true,"version":"0.1.0","port":3477,"gateway":"http://127.0.0.1:3425","helper":true}` |
| POST | `/api/translate` | `{"text":"","target":"auto","model":""}` | `text/event-stream`：`data:{"delta":"…"}` … `data:{"done":true,"cached":false,"ms":420}` |
| POST | `/api/selection` | — | 触发一次取词，返回 `{"text":""}`（调试用） |
| GET/PUT | `/api/settings` | JSON | 同 settings.json |
| POST | `/api/clipboard` | `{"text":""}` | `{"ok":true}` |
| POST | `/api/size` | `{"h":180}` | `{"ok":true}` |
| GET | `/` | — | 浮窗 UI（`go:embed`） |

不设 token：监听回环 + Host 校验足够；插件页面在 iframe 里被 CSP 禁网，调不到。

---

## 8. magpie 插件包（`plugin/`）

作用仅三件：让用户从 magpie 里看到/配置它、`server` hook 负责拉起 helper、注册 MCP 工具。

```jsonc
// plugin/package.json（字段形状以 magpie docs/reference.md 为准）
{
  "name": "magpie-select-translate",
  "version": "0.1.0",
  "type": "module",
  "main": "./index.mjs",
  "keywords": ["magpie-plugin", "magpie", "translation"],
  "engines": { "magpie": ">=0.1.1340" },
  "magpie": {
    "name": "划词翻译",
    "summary": { "zh": "选中即译，浮窗流式翻译。", "en": "Select-to-translate popover." },
    "page": { "title": "Translate", "file": "page/index.html",
              "service": { "port": 3477, "option": "port" } },
    "options": { "port": 3477, "model": "", "target": "auto",
                 "trigger": "hotkey", "hotkey": "Alt+D", "glossary": "" }
  }
}
```

```js
// plugin/index.mjs —— 只负责探活 + 拉起，不做翻译
import { spawn } from "node:child_process"
import { existsSync } from "node:fs"
import { join, dirname } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const exe = join(here, "..", "bin", "magpie-translate-helper.exe")

const alive = async (port) => {
  try { return (await fetch(`http://127.0.0.1:${port}/api/ping`,
    { signal: AbortSignal.timeout(800) })).ok } catch { return false }
}

export default {
  id: "magpie-select-translate",
  server: async (_input, options) => {
    const port = Number(options?.port) || 3477
    if (await alive(port)) return { port }
    if (!existsSync(exe)) return { port, error: "helper 未安装：先运行 scripts/install.ps1" }
    spawn(exe, ["--port", String(port)], { detached: true, stdio: "ignore", windowsHide: true }).unref()
    return { port }
  },
}
```

设置页限制（务必遵守）：`sandbox` + `connect-src 'none'`，**页面内不能 fetch**；只读写 `magpie.options`；模型下拉靠 `magpie.read("providers:list")`，拿不到就让用户手填。

---

## 9. 错误码与文案

| 内部错误 | 判定 | 浮窗文案 |
|---|---|---|
| `ErrGatewayDown` | 探测全失败 | `magpie 未运行，请启动后重试` |
| `ErrModelMissing` | 404 `model not found` | `模型不可用，请在 magpie → Agents 里选一个` |
| `ErrAuth` | 401/403 | `magpie 拒绝了这个请求，检查 provider 登录状态` |
| `ErrInterrupted` | 流中断、已有增量 | 保留内容 + `…（连接中断，点击重试）` |
| `ErrTooLong` | len>5000 | `已截断，仅翻译前 5000 字` |
| `ErrNoSelection` | 取词空 | `没选中文字`（1.5s 自动消失） |
| `ErrBlacklisted` | 命中黑名单 | `该应用不支持划词取词` |

---

## 10. 测试计划

### 10.1 取词矩阵（M0 必跑，结果填表）

| 应用 | 剪贴板法 | UIA | 备注 |
|---|---|---|---|
| Chrome 网页正文 | | | |
| Edge 地址栏 | | | 地址栏 Ctrl+C 会复制，需确认 |
| 记事本 | | | |
| Word / WPS 正文 | | | 表格内取词 |
| VS Code 编辑器 | | | Electron，UIA 通常不行 |
| Windows Terminal | | | 剪贴板法危险，仅 UIA |
| QQ / 微信聊天 | | | Electron/自绘 |
| PDF（Edge 内置） | | | 常选中整段 |
| 输入框内选字 | | | 别把整框内容当选中 |

### 10.2 功能用例

1. 英→中、中→英、日→中、空选区、单字符、5000+ 字符。
2. 缓存：同文连按两次，第二次 <50ms 且带 `cached` 标记。
3. 打断：上一次还在流式时按热键，旧请求必须 `cancel`，新旧内容不串。
4. 网关：停 magpie → 恢复，中间不崩、不需要重启 helper。
5. 剪贴板：先复制一段富文本再划词，结束后剪贴板内容不变（文本验证）。
6. 多屏 150% DPI：右下角触发，浮窗不出屏、不模糊。
7. 焦点：浮窗出现时正在记事本打字，打字不中断；Esc 关闭。

### 10.3 性能基线

| 指标 | 目标 | 测量点 |
|---|---|---|
| 热键 → 取词完成 | <150ms（剪贴板法）/ <250ms（UIA） | helper 日志 `pick_ms` |
| 取词 → 首字 | <600ms（含网络） | `ttfb_ms` |
| 缓存命中 → 全量显示 | <50ms | `cached_ms` |
| 常驻内存 | <120MB（WebView2 占大头） | 任务管理器 |
| 空闲 CPU | ~0% | 消息循环只在事件时醒 |

日志：`%LOCALAPPDATA%\magpie-translate\helper.log`，只记 `时间 事件 耗时 字数`，**不记原文**（`--debug-text` 才记，默认关）。

---

## 11. WBS 与工期

| 编号 | 任务 | 依赖 | 人日 |
|---|---|---|---|
| T1 | 工程骨架 + 参数解析 + 单实例 + 日志 | — | 0.3 |
| T2 | 剪贴板法取词（含全量保存/还原、修饰键上行） | T1 | 0.6 |
| T3 | 取词矩阵实测 9 个 app，填表 | T2 | 0.3 |
| T4 | 网关发现 + SSE 流式翻译 + prompt | T1 | 0.5 |
| T5 | WebView2 浮窗（创建一次 + 定位 + DPI + Esc 钩子） | T1 | 0.8 |
| T6 | 页面 UI（流式渲染 / 复制 / 折叠原文 / 主题） | T5 | 0.5 |
| T7 | 缓存 + single-flight + 错误文案 | T4,T6 | 0.3 |
| T8 | 语言判定 + 术语表 + 黑名单 | T4 | 0.2 |
| T9 | 插件包 + 设置页 + MCP tool | T4 | 0.5 |
| T10 | install/uninstall/自启脚本（CRLF 的 .vbs） | T5 | 0.3 |
| T11 | 验收用例 + 性能基线 + README | 全部 | 0.5 |
| **合计** | | | **4.8 人日** |

可并行：T2/T4 与 T5 互不依赖。

---

## 12. 风险登记册

| # | 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|---|
| R1 | 剪贴板法在终端/密码管理器触发 Ctrl+C 造成误操作 | 中 | **高（数据/命令）** | 黑名单硬编码 + 默认不监听终端；`--allow-clipboard-in-terminal` 显式开启 |
| R2 | 剪贴板还原不完整（图片/私有格式丢失） | 高 | 中 | 只保证 HGLOBAL 型；设置项可关掉"取词后还原" |
| R3 | WebView2 Runtime 缺失 | 低 | 高 | 启动检测 `GetAvailableCoreWebView2BrowserVersionString`，缺失时提示装 Evergreen Runtime，并降级为"无 UI，只写剪贴板" |
| R4 | 无焦点窗口收不到 Esc | 高 | 低 | 临时 LL 键盘钩子，仅可见期启用 |
| R5 | 杀软对全局键鼠钩子报毒 | 中 | 高 | 默认 `--trigger hotkey` 不装鼠标钩子；只注册 `RegisterHotKey` |
| R6 | 网关端口被改 / magpie 未跑 | 中 | 中 | 四级地址发现 + 60s 重探 + 明确文案 |
| R7 | 模型返回解释性废话 | 中 | 低 | prompt 约束 + `temperature:0` + 前两行启发式剥离（`^Here is` / `^Sure` / `^翻译：`） |
| R8 | 前缀缓存被术语表破坏 | 中 | 低（只影响成本） | 术语表放 user 尾部；system 只有一个可变点是目标语言 |
| R9 | UIA 跨进程挂起 | 中 | 中 | 250ms 超时 + goroutine；UIA 只是 v1.1 优化，v1 不依赖 |
| R10 | magpie plugin host 被别的插件拖挂 | 中 | 低 | helper 独立自启（启动文件夹 .vbs），不依赖插件加载 |

---

## 13. 验收清单（交付前逐条打勾）

```
[ ] go build ./... 无警告，产物单 exe < 12MB
[ ] Alt+D 在 Chrome / 记事本 / VS Code 各出一次译文
[ ] 浮窗出现时用户能继续在别的窗口打字（焦点未被抢）
[ ] Esc 关闭；复制按钮把译文写进剪贴板
[ ] 剪贴板原文在取词后被正确还原（先复制 HTML 富文本验证）
[ ] 终端里划词不会触发 Ctrl+C（黑名单生效）
[ ] 停掉 magpie → 提示"magpie 未运行"；重启 magpie → 无需重启 helper 即可翻译
[ ] 同文第二次命中缓存（<50ms）
[ ] 150% DPI 双屏右下角触发不越界
[ ] 日志不含原文（grep 日志确认）
[ ] magpie plugin add 后设置页可读写 options；杀掉 helper 重启 magpie 会自动拉起
[ ] 重启 Windows 后 helper 自动运行，Alt+D 可用
[ ] Ctrl+C 中断翻译流不留僵尸 goroutine（--debug 下看 goroutine 数）
```

---

## 14. 一天内能跑起来的最短路径

想先看到效果，跳过 §4.3、§6.3 的 DPI 细节、§7 的 settings 接口：

1. `main.go`：`RegisterHotKey` + 剪贴板取词 + `fmt.Println` 译文（不写浮窗）→ **2 小时**
2. 换成打印到控制台窗口，或者干脆：取词直接把译文写回剪贴板 + 系统通知 → **+1 小时**
3. 再接 WebView2 浮窗 → 再花 1 天

先验证"取词准不准 + 译文质量够不够"，再投 UI 的时间。
