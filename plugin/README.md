# magpie-select-translate

划词翻译：在任意程序里选中文字，按 `Alt+D`，鼠标旁弹出浮窗，流式显示 magpie 模型的译文。
Select-to-translate for magpie: select text anywhere, press `Alt+D`, and the translation streams in a popover next to the cursor.

## 它是什么

- 一个 magpie **页面插件**（Plugins → 划词翻译）：配置热键、模型、目标语言、术语表。
- 一个原生 helper 进程 `helper.exe`（Go，单文件，约 6MB）：全局热键、取词、浮窗、调网关。
  翻译请求走 `{gateway}/v1/chat/completions`（SSE、`temperature:0`、`reasoning_effort:low`）。
- 插件被禁用或加载失败也不影响 helper 自启（启动文件夹独立）。

## 安装

```sh
magpie plugin add magpie-select-translate      # 从 npm（或 @scope/magpie-select-translate）
magpie plugin add ./plugin                     # 从本地目录
```

helper 由插件在加载时拉起；包里有 `bin/helper.exe` 时会自动装到
`%LOCALAPPDATA%\magpie-translate\helper.exe`。没带二进制时自行编译：

```powershell
go build -ldflags "-H windowsgui -s -w" -o "$env:LOCALAPPDATA\magpie-translate\helper.exe" ./cmd/helper
```

## 用法

1. 选中文字（浏览器、记事本、Word、VS Code……）。
2. 按热键（默认 `Alt+D`）。
3. 浮窗里可以选中复制，点「复制」写入剪贴板；`Esc` 关闭；按住顶部可拖动。

`Windows Terminal`、`conhost`、远程桌面、密码管理器、资源管理器默认在黑名单里：
Ctrl+C 在这些程序里有副作用，命中时只提示「该应用不支持划词取词」。

## 设置项

| 键 | 说明 |
|---|---|
| `port` | helper 本地端口，默认 3477 |
| `hotkey` | 全局热键，默认 `Alt+D`（改完要重启 helper） |
| `model` | 模型 id，留空用网关 `/v1/models` 的第一个 |
| `target` | `auto` / `zh` / `en` |
| `glossaryFile` | 术语表文件，每行 `原文=译文` |

设置页在沙箱里没有网络（`connect-src 'none'`），所以它只读写 `magpie.options`；
翻译状态请看浮窗本身。

## 隐私

- helper 只监听 `127.0.0.1`，校验 `Host`。
- 日志只有时间、事件、耗时、字数，**不记原文**。
- 取词后会把剪贴板全量还原（图片等非 HGLOBAL 格式除外）。

## 卸载

1. `magpie plugin remove <包名>` 移除插件。
2. 结束残留的 helper：任务管理器结束 `magpie-translate-helper.exe`，
   或运行 `%LOCALAPPDATA%\magpie-translate\uninstall.ps1`（插件首次启动时会把这份脚本拷过去）。

helper 只在 magpie 启动插件时被拉起，magpie 不开它就不会常驻，也不写注册表。

## License

MIT