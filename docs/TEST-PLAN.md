# 划词翻译 · 测试实施方案与实测记录

配套代码：`cmd/helper`、`internal/*`、`plugin/`、`scripts/`
被测对象：`magpie-translate-helper`（单文件 exe）+ magpie 插件包
最后执行：2026-11-11，Windows 11 24H2 / Ryzen 5 7640HS / 16GB，Go 1.23.10，magpie 网关 `127.0.0.1:3425`

---

## 1. 测试分层

| 层 | 范围 | 手段 | 是否需要 magpie | 可重复性 |
|---|---|---|---|---|
| L0 单元 | prompt / 语言判定 / LRU / SSE 解析 / 网关探测 / 热键解析 / 黑名单 | `go test ./...`（stdlib testing + `httptest`） | 否 | 完全可重复 |
| L1 接口 | `/api/translate` SSE、错误码映射、Host 校验、设置读写 | `httptest` 挂假网关 | 否 | 完全可重复 |
| L2 域冒烟 | 真 helper + 真网关：探活、翻译、缓存、404 模型、剪贴板、设置 | `scripts/smoke.ps1` | 是 | 可重复（依赖网关在线） |
| L3 桌面 E2E | 热键 → 取词 → 浮窗 → 译文；焦点不抢、剪贴板还原 | 自动化脚本 + 截图 + 窗口文本探针 | 是 | 半自动（受前台窗口/输入法影响） |
| L4 性能/稳定性 | 首字延迟、缓存延迟、常驻内存、网关重启恢复 | 计时埋点 + 任务管理器 | 是 | 半自动 |

设计原则：
- L0/L1 必须在任何改动后全绿才能进 L3；
- L3 里凡是「谁的窗口在前台」「输入法吞字符」这类环境噪声，一律用**窗口文本探针**
  （`EnumChildWindows` + `GetWindowText`）判定，而不是靠肉眼看截图；
- 每个已知不可测项都写进 §7，不假装覆盖。

---

## 2. 环境核对（compiled 前置）

```powershell
go version                                   # go1.23.10 windows/amd64
Invoke-RestMethod http://127.0.0.1:3425/v1/models | Select-Object -Expand data | Select-Object -First 1 id
# group/deepseek-v4.1-flash
```
- magpie 配置目录实测为 `C:\Users\<user>\.config\magpie`（无 `settings.json.port` → 走默认 3425）。
- 网关不需要 API key（`caller-keys.json` 不存在也返回 200）。
- 实测 SSE 格式：`data:{json}`（冒号后无空格）、心跳 `: keepalive`、先 `reasoning_content` 后 `content`。

---

## 3. L0 单元用例

命令：`go test ./... -v`

| 编号 | 用例 | 断言要点 | 结果 |
|---|---|---|---|
| U1 | `TestDetectTarget` | 英→zh、中→en、中英混排→en、日/韩/俄→zh、纯数字符号→zh | PASS |
| U2 | `TestNormalizeTarget` | `en/EN/English`→en，`zh/zh-CN/中文`→zh，`auto/空/乱写`→空 | PASS |
| U3 | `TestSystemPromptStable` | 同目标语言 system 逐字节一致；中英两版不同；非空 | PASS |
| U4 | `TestUserPromptGlossary` | 无术语表时零前缀；有术语表时 `[term]…[/term]` 在原文前；注释/空行/无等号行被丢 | PASS |
| U5 | `TestStreamParsesMagpieFormat` | `data:{...}` 与 `data: {...}` 都认；跳过 `: keepalive`；忽略 `reasoning_content`；`[DONE]` 结束 | PASS |
| U6 | `TestStreamInterruptedKeepsContent` | 上游掐断后保留已收内容 | PASS |
| U7 | `TestStreamStatusMapping` | 404→`ErrModelMissing`，401/403→`ErrAuth` | PASS |
| U8 | `TestStreamRespectsContext` | ctx 150ms 内取消，不卡住 | PASS |
| U9 | `TestGatewayProbeAndModelDefault` | 探到 `/v1/models` 并取第一个 id 作默认模型 | PASS |
| U10 | `TestGatewayDownWhenNothingAnswers` | 全候选失败→`ErrGatewayDown`（候选锁定，不回落真网关） | PASS |
| U11 | `TestCacheLRU` | 命中提升、容量淘汰、key 含目标语言 | PASS |
| U12 | `TestParseHotkey` | `Alt+D`/`alt+d`/`Ctrl+Shift+T`/`Win+F12`/`Ctrl+1` 正确；`Alt`/`Hyper+D`/`F99` 报错 | PASS |
| U13 | `TestLabel` | 语言对/缓存/截断标记 | PASS |
| U14 | `TestNormalize` / `TestBlacklistMatch` | 去 CRLF 与首尾空白；黑名单大小写/空格容错、默认 8 项 | PASS |

---

## 4. L1 接口用例

`internal/api` 里用 `httptest` 起假网关（`/v1/models` + SSE `/v1/chat/completions`）。

| 编号 | 用例 | 断言 | 结果 |
|---|---|---|---|
| A1 | `TestTranslateCachesAndDetectsTarget` | 英→zh、默认模型取 `/v1/models` 首个、第二次 `cached=true` | PASS |
| A2 | `TestTranslateRejectsTooShort` | `<2` 字返回 `ErrTooShort`（文案「没选中文字」） | PASS |
| A3 | `TestTranslateGatewayDown` | 假网关 502 + 候选锁定 → `ErrGatewayDown`，文案含「magpie 未运行」 | PASS |
| A4 | `TestTranslateSSEEndpoint` | `POST /api/translate` 返回 `data:{"delta"}` 与 `data:{"done"}` | PASS |
| A5 | `TestGuardHostBlocksForeignHost` | `Host: evil.example.com` → 403 | PASS |
| A6 | `TestSettingsRoundTrip` | `PUT /api/settings` 落地并被 `Settings()` 读回 | PASS |

---

## 5. L2 域冒烟（真网关）

```powershell
Start-Process .\bin\magpie-translate-helper.exe -WindowStyle Hidden
powershell -ExecutionPolicy Bypass -File scripts\smoke.ps1 -Port 3477
```

2026-11-11 实测输出：

```
[PASS] ping :: version=0.1.0 gateway= hotkey=Alt+D
[PASS] translate :: elapsed=3453ms lines=12
[PASS] cache-hit :: elapsed=15ms
[PASS] short-text :: data: {"error":{"code":"short","message":"没选中文字"}}
[PASS] model-missing :: data: {"error":{"code":"model","message":"模型不可用，请在 magpie → Agents 里选一个"}}
[PASS] clipboard :: write={"ok":true}
[PASS] settings-get :: hotkey=Alt+D target=auto restore=True
ALL PASS
```

补充实测（curl 直连，非脚本）：
- 50 字英文 → 首条 delta ≈ 1.2s，全文 2.9~3.5s（`reasoning_effort:low`）。
- 同文第二次 → 22ms 内返回单条 `delta` + `cached:true`。
- `model:"nope/nope"` → 404 被映射成 `code:"model"`。
- 不存在的模型裸调 404 body 形如 `{"error":{"message":"magpie knows no model ..."}}`。

---

## 6. L3 桌面 E2E 与 L4 性能

### 6.1 用例与结果

| 编号 | 场景 | 步骤 | 期望 | 实测 |
|---|---|---|---|---|
| E1 | 记事本取词 | 记事本贴文本→`Ctrl+A`→`GET /api/selection` | `app=notepad.exe via=clipboard`，文本=选中内容 | PASS，`ms=93`，文本正确 |
| E2 | 剪贴板还原 | 剪贴板写 `ORIGINAL-CLIP`→触发取词→读剪贴板 | 仍为 `ORIGINAL-CLIP` | PASS |
| E3 | 热键全链路 | 选区→`Alt+D` | 日志 `pick ok`，浮窗出现在鼠标右下方 | PASS（`chars=46`，浮窗截图与窗口文本探针均确认） |
| E4 | 浮窗内容 | 探针读子控件文本 | 头部 `EN → 中文`，正文中文译文，底部原文 | PASS：`你好，世界，这是一个简短的翻译测试。` / `Hello world, this is a short translation test.` |
| E5 | 流式渲染 | 观察浮窗逐字出现 | 首字 < 1.5s，随后继续追加 | PASS（截图两个时刻内容不同） |
| E6 | 缓存标记 | 同文连按两次热键 | 第二次几乎立即出现，头部带 `· 缓存` | PASS（`cached:true`，15~22ms） |
| E7 | 未选中文字 | 不选字按热键 | 浮窗显示「没选中文字」，不崩 | PASS（`pick failed ... err=没选中文字`） |
| E8 | 黑名单 | 终端里选中文字按热键 | 不发送 Ctrl+C，提示不支持 | 代码路径已实现，**人工待测**（自动化会误伤终端） |
| E9 | 单实例 | 再启动一个 helper | 新进程直接退出，日志 `port busy` | PASS（日志 `start skipped`） |
| E10 | 网关重启恢复 | 停 magpie→触发（提示未运行）→起 magpie→再触发 | 不重启 helper 即可翻译 | 逻辑为 60s 缓存 + 失败即失效重探；**人工待测** |
| E11 | Esc 关闭 | 浮窗可见按 Esc | 浮窗消失，焦点回到原应用 | **人工待测**（Esc 依赖 LL 钩子，自动化注入会被自身钩子吞掉） |
| E12 | 复制按钮 | 浮窗里点「复制」 | 剪贴板=译文，按钮变「已复制」 | 接口 `/api/clipboard` PASS；按钮点击 **人工待测** |
| E13 | 长文本 | 选中 2498 字按热键 | 应流式输出，最长 55s 后超时提示 | 观察到「翻译中…」持续 >55s 后未及时收尾，见 §8 风险 R1 |
| E14 | DPI/多屏 | 150% 缩放下右下角触发 | 不越出工作区、不模糊 | 代码按 `GetDpiForWindow` + 工作区翻转；本机 100% 缩放，**150% 待测** |

### 6.2 性能基线（实测）

| 指标 | 目标 | 实测 |
|---|---|---|
| 热键→取词完成（剪贴板法） | <150ms | 70~93ms |
| 取词→首字（含网络，reasoning low） | <600ms | ≈1.2s（网关侧思考时间，非本地瓶颈） |
| 缓存命中→全量显示 | <50ms | 15~22ms |
| 常驻内存 | <120MB | 单进程 ≈12MB（纯 Win32，无 WebView2 子进程） |
| 空闲 CPU | ~0% | 消息循环阻塞在 `GetMessageW`，无轮询 |
| 产物大小 | <12MB | 6.3MB |

### 6.3 自动化辅助

- 取词自检工具：`go run ./cmd/selecttest --wait 5 --verbose`（打完字切窗口，看 `app/via/ms/text`）。
- 窗口探针（判断浮窗是否真的画出内容）：
  ```powershell
  # EnumWindows → 找 class=magpie-translate-popover → EnumChildWindows 打印每个子控件文本
  ```
  这条比截图可靠，是本轮定位「浮窗白板 / 窗口未显示」的关键工具。

---

## 7. 本轮测试发现并修复的缺陷

| # | 现象 | 根因 | 修复 |
|---|---|---|---|
| B1 | `/api/clipboard` 报 `Access is denied`，随后系统剪贴板被永久锁死（`Get-Clipboard` 挂起 60s） | `OpenClipboard`/`CloseClipboard` 是线程亲和的，Go 协程中途换线程导致 Close 落到别的线程 | 每个剪贴板函数 `runtime.LockOSThread()` |
| B2 | 热键路径取词失败，但直接调接口成功 | `GetKeyState` 只看调用线程自己的按键状态，取词 goroutine 上永远「没按 Alt」 | 改 `GetAsyncKeyState` + 释放后等 40ms |
| B3 | 偶尔取到与本选区无关的剪贴板内容 | 只比对序列号，别的程序同时写也会命中 | 取词前先 `EmptyClipboard`，只认之后出现的内容 |
| B4 | 浮窗是一块白板 | WebView2 子窗口在宿主 `SW_HIDE`/再显示后不再绘制（探针见子窗口 `visible=False`） | 弃用 WebView2，改纯 Win32 弹窗 + `EDIT`/`STATIC`/`BUTTON`；顺带省掉 Runtime 依赖与 100MB+ 内存 |
| B5 | 换成 Win32 后浮窗仍不显示 | 建窗口时没给 `WS_VISIBLE`，`MoveWindow` 不会让窗口可见 | 建好后先挪到屏幕外再 `ShowWindow(SW_SHOWNOACTIVATE)` |
| B6 | 单测暴露 `onDelta` 为 nil 时 panic | `Stream` 无条件回调 | 加 nil 判断（顺带覆盖 HTTP 层不传回调的用法） |
| B7 | 网关测试误命中本机真实 magpie 导致误判 | 候选列表总有 `127.0.0.1:3425` 兜底 | 增加 `Gateway.Candidates` 锁定候选（测试/单网关场景） |
| B8 | **浮窗头部显示「翻译中…」而正文一直是空的**（翻译其实成功了） | `winapi.U()` 把 Go 字符串转成 `uintptr` 后直接传给 Win32：调用期间没有任何引用，GC 可能回收那块 UTF-16 缓冲区，`SetWindowTextW` 读到已释放内存 | 改 `UTF16()` 返回 `*uint16` + `defer runtime.KeepAlive(ptr)`。**教训：Go 调 Win32 传字符串必须 KeepAlive** |
| B9 | 长文本/上游繁忙时浮窗长时间停在「翻译中…」，看起来像卡死 | 报错/结果只有终态，没有中间反馈 | 加 5s 看门狗：每 5s 记 `translate waiting Ns` 并把标题改成「等待上游… Ns」 |
| B10 | 浮窗会莫名其妙自己消失 | Esc 钩子里把 `lParam`（KBDLLHOOKSTRUCT 指针）的低字节当按键码：任何键只要地址低字节撞上 `0x1B` 就被当成 Esc | 正确解引用 `KBDLLHOOKSTRUCT.VkCode` 再比较 |
| B11 | 按钮丑、没有点击反馈，浮窗拖不动 | 用的是系统 BUTTON 控件（Win11 经典样式 + 焦点虚线框）；浮窗没做命中测试 | 去掉 BUTTON 子窗口，改父窗口自绘两块矩形：hover 底色 + 文字变亮 + 手型光标（WM_SETCURSOR）、按下更深；顶部 34px 返回 `HTCAPTION` 支持拖动，拖过之后内容变化只改高度不跳回鼠标旁 |

---

## 8. 未覆盖项与风险（诚实清单）

| # | 项 | 影响 | 处理 |
|---|---|---|---|
| R1 | 2000+ 字长文本在 `reasoning_effort:low` 下可能超过 55s | 浮窗长时间停在「翻译中…」，超时后提示可重试 | 建议把长文本在 UI 层限制到 ~1500 字，或按段落分片；待用长文本实测后定阈值 |
| R2 | UIA TextPattern 未实现 | 终端/Electron 取词只能靠剪贴板法或黑名单 | 按方案留 v1.1；先用黑名单挡住危险进程 |
| R3 | 非 HGLOBAL 剪贴板格式不还原 | 复制图片后划词会丢图 | 代码里有 `shortcut:` 注释，需要时改 DuplicateHandle 全格式还原 |
| R4 | Esc 依赖可见期临时 LL 键盘钩子 | 部分安全软件会告警 | 备选：点击浮窗外关闭；已在注释里写明 |
| R5 | magpie 插件设置页未在真机 `magpie plugin add` 验证 | manifest 字段以社区 `@magpie-community/page-chat` 为样本，可能与当前版本有差异 | 待装一次插件核对；helper 不依赖插件，功能不受影响 |
| R6 | `go vet` 对 `GlobalLock` 返回值报 `possible misuse of unsafe.Pointer` | 这是 Win32 唯一可行写法 | `go build` 正常；如需 CI 静音，把 clip 相关文件排除 unsafeptr 检查 |
| R7 | 150% DPI / 多屏未实测 | 浮窗可能位置或字号偏 | 代码按 `GetDpiForWindow` + 工作区翻转写，需要实机确认 |
| R8 | 前景窗口被其他程序抢走（微信通知）时取词会打到错误的应用 | 取到别人的选区 | 取词前记录 `GetForegroundWindow` 的进程名并在日志里输出，便于排查；已实测到该噪声 |

---

## 9. 回归清单（每次改动后按顺序跑）

```powershell
go build ./...                                  # 1 编译
go vet ./...                                    # 2 静态检查（预期只剩 unsafeptr 提示）
go test ./... -count=1                          # 3 L0+L1 全绿
go build -ldflags "-H windowsgui -s -w" -o bin\magpie-translate-helper.exe ./cmd/helper
Get-Process magpie-translate-helper -EA SilentlyContinue | Stop-Process -Force
Start-Process .\bin\magpie-translate-helper.exe -WindowStyle Hidden
powershell -ExecutionPolicy Bypass -File scripts\smoke.ps1     # 4 L2
go run ./cmd/selecttest --wait 5 --verbose                     # 5 L3 取词
# 6 L3 全链路：选区 → Alt+D → 看浮窗；用窗口探针核对文案
```
