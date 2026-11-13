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
| `hotkey` | 全局热键，默认 `Alt+D` |
| `model` | 模型 id，留空用网关 `/v1/models` 的第一个 |
| `target` | `auto` / `zh` / `en` |
| `glossary` | 术语表正文，每行 `原文=译文` |

设置页由 helper 自己提供：打开插件的页面会直接跳到 <http://127.0.0.1:3477/>（端口可用 `port` 改），
那里能读网关的模型列表，热键、模型、目标语言、术语表都在那儿改——热键改完立即生效，其余保存即生效。
插件只把 `port` 传给 helper，其它设置一律以 helper 的 `settings.json` 为准
（`%LOCALAPPDATA%\magpie-translate\settings.json`），magpie 重启时不会拿旧 `options` 把你刚改的设置盖回去。
翻译状态请看浮窗本身。

## 隐私

- helper 只监听 `127.0.0.1`，校验 `Host`；带浏览器 `Origin` 的请求必须带设置页的随机令牌（每次启动重新生成，只注入给设置页自己的脚本）才放行，写请求必须是 `application/json`。
- 网关密钥从 magpie 的 `caller-keys.json` 只读取用，只放内存，不落日志。
- 日志只有时间、事件、耗时、字数，**不记原文**。
- 取词后会把剪贴板全量还原（图片等非 HGLOBAL 格式除外）。

## 卸载

1. `magpie plugin remove <包名>` 移除插件。
2. 结束残留的 helper：任务管理器结束 `helper.exe`，
   或运行 `%LOCALAPPDATA%\magpie-translate\uninstall.ps1`（插件首次启动时会把这份脚本拷过去）。

helper 只在 magpie 启动插件时被拉起，magpie 不开它就不会常驻，也不写注册表。

## 更新

- 0.3.0：设置页改由 helper 自己提供（能读网关模型列表，热键改完立即生效，不用重启 helper）；浮窗里原文和译文合成一块可滚动内容，长原文不再被截断在 3 行；`scripts\install.ps1` 现在真的会先停掉旧 helper（之前按错的进程名杀，升级后跑的还是老版本）。
- 0.2.2：显示名改成包名 `magpie-select-translate`（`magpie.name` 之前是 "Select Translate"，发现页卡片上看到的就是它）。
- 0.2.1：修安全漏洞：本地接口拒绝带浏览器 `Origin` 的请求，写请求必须是 `application/json`；术语表只接收正文（`glossary`），不再按路径读本地文件。`package.json` 去掉 BOM。
- 0.2.0：浮窗加了浅绿描边；正文改成整行取整、滚动条常驻，长译文不会再被切掉半行；顶部可以直接按住拖动（之前 `WM_NCHITTEST` 把屏幕坐标当客户区坐标用，拖不动）；标题改父窗口自绘，修掉连续取词时的残影。
- 0.1.1：设置页和 `magpie.summary` 补全，能被 magpie 的 Discover 展示。

## License

MIT