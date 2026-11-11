# 划词翻译 helper · 实施提示词（Prompt 包）

配套文档：`docs/magpie-select-translate.md`（技术方案）。
用法：把下面每个代码块**整段**贴给 coding agent（Claude Code / Codex / OpenCode 均可），工作目录固定为仓库根。
顺序执行 M0 → M3，每阶段结束按"自检"验收，不通过不进下一阶段。

---

## 0. 主提示词（每次会话开头都贴一次）

```text
你是资深 Windows 桌面工程师。任务：在仓库 D:\work\code\ex\vibecoding\pro2 实现「划词翻译」工具。

【背景】
用户机器上已装 magpie（yetone/magpie，本地 LLM 网关 tray 应用），网关地址 http://127.0.0.1:3425，
提供 OpenAI 兼容接口 /v1/models 与 /v1/chat/completions，支持流式。桌面版通常不需要 API key。
平台：Windows 11 + PowerShell。magpie 插件体系没有全局热键、选区读取、鼠标钩子、桌面浮窗能力，
所以必须自己写一个常驻 helper 进程；magpie 插件包只是包装（安装器 + 设置页 + MCP tool）。

【要交付的东西】
1. cmd/helper（Go）：全局热键、UIA 取词、剪贴板兜底、WebView2 浮窗、流式翻译、LRU 缓存。
2. plugin/（magpie 插件包，ESM）：package.json + index.mjs + page/index.html。
3. scripts/install.ps1 + uninstall.ps1：编译、落地、写自启。
4. README.md：编译与使用说明。

【硬性约束】
- 只监听 127.0.0.1；默认不把原文写入任何日志或文件。
- 取词顺序：UIA TextPattern.GetSelection() → 失败降级"模拟 Ctrl+C 读剪贴板"。
  剪贴板路径必须全量保存并在 defer 中还原；进程名在黑名单（mstsc.exe / WindowsTerminal.exe /
  conhost.exe / 密码管理器 / 游戏）时禁用剪贴板路径。
- UIA 调用必须带 250ms 超时（goroutine + select），超时安静降级，不许卡住 UI。
- 浮窗用 WebView2，窗口样式 WS_POPUP|WS_EX_NOACTIVATE|WS_EX_TOOLWINDOW|WS_EX_TOPMOST，
  不抢焦点；位置跟随鼠标 +16/+20px，超出屏幕工作区则翻转；多屏用 MonitorFromPoint，
  DPI 用 GetDpiForWindow。
- 翻译请求：POST {gateway}/v1/chat/completions，stream:true，temperature:0。
  system prompt 必须逐字节稳定（不许拼时间戳/字数/随机值），术语表等变量一律放 user 消息尾部
  —— 用户在 magpie 侧有真前缀缓存，破坏前缀会明显变贵变慢。
- 网关发现顺序：settings 里的 gateway → 环境变量 MAGPIE_ADDR → 读 %APPDATA%\..\magpie 或
  ~/.config/magpie/settings.json 的 port → 兜底 http://127.0.0.1:3425；用 GET /v1/models 探测。
- 生成的 .cmd / .vbs 必须 CRLF 换行，否则 cmd.exe / WSH 会报语法错误。
- 不要引入重型框架（gin/echo/fx 之类）；net/http + 标准库为主。
- 每一处已知取舍写成注释：// shortcut: <限制>, <何时升级>。

【风格】
- 最小可用：不写没人要求的抽象层、配置项、插件化容器。
- 中文注释只写"为什么"，不写"是什么"。
- 每阶段结束必须能 go build 通过、能跑起来自检，不许留 TODO 当交付物。

现在先只做【M0】，做完停下等我验收，不要顺手做 M1。
```

---

## 1. M0 · 取词可行性验证（0.5 天）

```text
只做取词 spike，不碰 UI、不碰翻译。

新建 cmd/selecttest/main.go：
1. 命令行接收一个等待秒数，sleep 后取当前鼠标位置（GetCursorPos）。
2. 用 IUIAutomation::ElementFromPoint 定位元素，向上最多找 6 层父节点，
   支持 TextPattern 就调 GetSelection().GetText()；支持 ValuePattern 且是输入框就取 value。
   整个 UIA 流程放在 goroutine 里，250ms 超时后返回空。
3. UIA 拿不到就降级：全量读剪贴板 → SendInput 发 Ctrl+C → 等 150ms → 读文本 →
   defer 还原原剪贴板。
4. 打印：命中的 app 进程名、取词方式（uia/clipboard）、耗时 ms、文本前 80 字符。

自检（必须逐条实测并把结果贴给我）：
- Chrome 网页正文划选
- Edge 地址栏文字
- 记事本、Word（或 WPS）正文
- VS Code 编辑器正文
- Windows Terminal 里的输出文字
- QQ 或微信聊天记录
- PDF（Acrobat 或 Edge 内置阅读器）
对每个 app 记录：成功/失败、走哪条路径、耗时。
失败率超过 20% 就停下来，先告诉我哪几个 app 不行再决定策略。
```

---

## 2. M1 · helper 骨架：热键 + 浮窗 + 翻译（1 天）

```text
实现 cmd/helper/main.go，把 M0 验证过的取词封装成 internal/pick 包（Pick(pt) (string, string, error)）。

要求：
1. 单实例：用命名互斥体或端口占用（127.0.0.1:3477）判断，重复启动直接退出。
2. 全局热键：RegisterHotKey，默认 Alt+D（可被 --hotkey 覆盖）。
3. 浮窗：WebView2（github.com/jchv/go-webview2），加载内嵌 HTML（go:embed），
   窗口样式见主提示词约束；收到译文增量就用 postMessage 推给页面。
4. 翻译：internal/translate 包里实现 Stream(ctx, text, target, model, onDelta)。
   - system prompt 固定常量（中译英/英译中两个版本，按目标语言二选一）。
   - 解析 SSE：逐行读 data:，跳过以 : 开头的心跳和 [DONE]。
   - 55s 超时；新请求先 cancel 旧请求。
5. 快捷键：Esc 关闭浮窗，Ctrl+C 复制译文。
6. 语言判定：正则。/[\u4e00-\u9fff]/ 命中且无拉丁字母 → 目标 English；含假名/谚文/西里尔
   → 目标 zh-CN；纯拉丁 → zh-CN。

自检：
- 在 Chrome 选一段英文按 Alt+D，浮窗在鼠标旁出现，逐字出中文，首字 < 1s。
- 选中文段落，输出英文，段落换行保留。
- Esc 关闭后焦点回到原来的应用（输入光标没丢）。
- 关掉 magpie 网关，浮窗显示"magpie 未运行"，helper 不崩。
```

---

## 3. M2 · 打磨：缓存 / 错误态 / 多屏（1 天）

```text
1. LRU 缓存 512 条，key = sha1(text|target|model)，命中直接上屏（不走网络），
   浮窗右上角显示"缓存"小标记。
2. 错误态：网关连不上 / 401 / 404 模型不可用 / 流中断 / 文本超 5000 字 分别给固定文案，
   流中断保留已收内容并显示"点击重试"。
3. 同文本 1 秒内不重复翻译；<2 字符不翻译。
4. 可选"划词即弹"模式（--trigger select）：SetWindowsHookEx(WH_MOUSE_LL) 监听 LBUTTONUP，
   UP-DOWN 位移 > 4px 且间隔 < 1.5s 视为划选；默认关闭。
5. 黑名单 --blacklist 逗号分隔进程名，命中则完全不响应。
6. 多显示器 + 125%/150% DPI：浮窗不越界、不模糊（GetDpiForWindow + AdjustWindowRectEx）。
7. 术语表 --glossary-file：每行 "原文=译文"，拼进 user 消息尾部，不动 system。

自检：技术方案文档 §10 的 9 条验收标准逐条跑一遍，把结果列成表格给我。
```

---

## 4. M3 · magpie 插件包 + 安装脚本（0.5 天）

```text
1. plugin/package.json（照抄技术方案 §6.2，字段名以 magpie 的 docs/reference.md 为准，
   不确定的字段先省略，别猜）。
2. plugin/index.mjs：default export { id, server }。server 里：
   - GET http://127.0.0.1:<port>/api/ping 探活（800ms 超时）；
   - 活着就直接返回；不活就 detached spawn helper.exe（windowsHide，不阻塞）；
   - helper 不存在时返回 { error: "helper 未安装：先运行 scripts/install.ps1" }。
3. helper 补三个接口：/api/ping、/api/translate（SSE）、/api/settings（GET/PUT 读写 json）。
4. plugin/page/index.html 设置页：
   - await magpie.ready（10s 内），读 magpie.options.get() 渲染表单（模型 / 目标语言 /
     触发方式 / 热键 / 术语表），保存走 magpie.options.set() 并做 ≥1s 节流。
   - 注意：页面在 sandbox + CSP connect-src 'none' 里，绝对不要写 fetch。
5. scripts/install.ps1：go build -ldflags "-H windowsgui" → 复制到
   %LOCALAPPDATA%\magpie-translate\helper.exe → 在启动文件夹写 start-helper.vbs（CRLF）
   → 立即启动一次。scripts/uninstall.ps1 反向清理。
6. 可选：注册 MCP tool translate_text（走同一个 HTTP 调用，不要重复实现翻译逻辑）。

自检：
- magpie plugin add ./plugin 之后，magpie 里出现设置页，改热键 → 重启 helper 后生效。
- 杀掉 helper 进程，重启 magpie，helper 被自动拉起。
- 自启的 .vbs 双击无黑框闪现。
- 重启 Windows 后 helper 自动在运行，Alt+D 可用。
```

---

## 5. 纠正话术（agent 跑偏时直接贴）

```text
停。你超出了范围：
- 不要为"以后可能要用"加配置文件/抽象接口/插件注册表。
- 不要写单元测试脚手架之外的额外测试框架，用 stdlib testing 就够。
- 不要改 magpie 本体代码，插件与 helper 都是外部代码。
- 不要新增依赖除非标准库确实做不到（WebView2 绑定、UIA COM 绑定除外）。
先告诉我当前 diff 有哪几个文件、每个文件解决什么问题，再继续。
```

```text
我不需要"更好的架构"，我需要现在就能按 Alt+D 出译文。
按以下顺序自查并修：① 能不能 go build；② Alt+D 有没有反应；③ 浮窗有没有出现；
④ 网关请求发出去了吗（在 magpie 日志里看得到吗）；⑤ 首字延迟多少。
把每一步的实际观察结果贴出来，不要只说"已完成"。
```

---

## 6. 环境前置（贴给 agent 前先自己确认）

```powershell
go version                      # 需要 >= 1.21
Get-Command magpie -ErrorAction SilentlyContinue   # 可选，用 tray 版即可
Invoke-RestMethod http://127.0.0.1:3425/v1/models | ConvertTo-Json -Depth 3 | Select-Object -First 1
# 能返回 models 列表说明网关正常
reg query "HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" /v pv
# 有输出说明 WebView2 Runtime 已装（Win11 一般自带）
```
