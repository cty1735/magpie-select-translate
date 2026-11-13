# 打包与发布：让用户在 magpie 里搜到它

结论先放这：**能不能被搜到，只取决于 `registry.json` 里有没有你那条记录。**
它不在 npm、不在 GitHub star 数里，就在 magpie-community/plugins 那个仓库的 `registry.json`。
所以流程是：把自己的包发出去 → 给那个仓库提一个 PR 加一行 → 合并后几小时内出现在
Plugins → Discover 的搜索里。

---

## 0. 两种上架方式，选一个

| 方式 | 包放哪 | 谁发版本 | 适合 |
|---|---|---|---|
| A. 进官方仓库（推荐首次） | `magpie-community/plugins/packages/<name>/` | 仓库 CI 自动发 npm + dist | 想省掉 npm 账号和发布脚本 |
| B. 自己维护仓库 | 自己的 GitHub + 自己的 npm 包名 | 你自己 `npm publish` | 想保留节奏/私有 CI（`@guotree/magpie-websearch` 就是这种） |

两种都要给 magpie-community/plugins 提 PR 加 `registry.json`（B 方式 PR 里只改一行）。
下面的打包步骤对两种都一样。

---

## 1. 打包（仓库已就绪）

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build-plugin.ps1
```

它做三件事：
1. 交叉编译 helper 到 `plugin/bin/helper.exe`（`-H windowsgui -s -w`，约 6.3MB）
2. `npm pack`，把 `files` 里的东西打进 tarball（`index.mjs` / `page/` / `bin/` / `README.md`）
3. 打印后续命令

为什么要把 exe 塞进 npm 包：用户的机器上没有 Go 编译器，`bin/helper.exe` 是唯一能
"装上就能用"的办法。6MB 的包对 npm 不算大（`page-agent-console` 首次安装 46MB）。

`plugin/index.mjs` 里的顺序是：先用 `%LOCALAPPDATA%\magpie-translate\helper.exe`，
没有就把包里的 `bin/helper.exe` 拷过去再启动 —— 设置和日志永远只有一份。

---

## 2. 包名：两个选择

- **进官方仓库**：必须叫 `@magpie-community/page-<name>`，本次即
  `@magpie-community/page-select-translate`。`package.json` 的 `name` 要改成这个。
- **自己发 npm**：用你自己拥有的 scope，例如 `@yourname/magpie-select-translate`
  （`npm whoami` 确认；没 scope 就直接用 `magpie-select-translate`，先查 `npm view <name>` 有没有被占）。

其余字段照 `plugin/package.json` 抄就行，关键是 `magpie.page` 的形状：

```json
"magpie": {
  "page": {
    "title": "Translate",
    "titles": { "zh": "划词翻译", "en": "Translate" },
    "file": "page/index.html"
  },
  "options": { "port": 3477 }
}
```

页面只是跳板：`page/index.html` 读出 `options.port` 后 `location.replace` 到 helper 自己的设置页
（模型下拉要实时读网关，沙箱页面连不上任何地址）。真正的设置存在 helper 的
`%LOCALAPPDATA%\magpie-translate\settings.json`，`options` 里只留 `port` 一个字段——
插件宿主重启时不能拿 options 里的旧值把用户在设置页刚改的配置盖回去。

**不要写 `page.service`**：那是给"magpie web 远程访问"转发用的，helper 只听 127.0.0.1，
声明了反而要维护远程路径。helper 由 `server` 钩子拉起。
页面不读任何 magpie 数据，所以**不需要** `permissions`（读什么才声明什么，
多声明会被审核挡下来）。

---

## 3. registry.json 记录（这就是"能被搜到"的那一行）

```json
{
  "package": "@magpie-community/page-select-translate",
  "name": "magpie-select-translate",
  "kind": "page",
  "summary": {
    "en": "Select text anywhere and press a hotkey: a popover next to the cursor streams the translation from magpie's own models. Runs a local helper on 127.0.0.1 that does the selection capture, the popup and the gateway call; the hotkey, model, target language and glossary are set in the page. Windows only.",
    "zh": "在任意程序里选中文字按热键，鼠标旁的浮窗流式显示 magpie 模型的译文。在本机 127.0.0.1 跑一个 helper，负责取词、浮窗和调网关；热键、模型、目标语言、术语表在页面里设置。仅支持 Windows。"
  }
}
```

字段规则（来自 `community-README.md` 的 "The market" 一节）：

- `package`：npm 上的包名
- `name`：Discover 里显示的名字
- `kind`：纯页面插件写 `"page"`；有 `providers` 的是供应商插件
- `icon`：可选，用 magpie 自带的图标名，或指向仓库里的 svg 的 raw URL
- `summary`：**en 和 zh 都要写**，这句就是用户判断"装不装"的唯一依据

同名渠道去重只针对"供应商登录"，页面插件不冲突，不用解释差异。

---

## 4. 提 PR

```powershell
gh repo clone magpie-community/plugins
cd plugins
# A 方式：把 plugin/ 整个拷成 packages/select-translate/，改 name 为 @magpie-community/page-*
#         再把上面那条 registry 记录插进 registry.json 的 plugins 数组
# B 方式：只改 registry.json 一行
npx bun scripts/check.mjs select-translate   # 官方自检脚本，本地先跑一遍
gh pr create --title "Add page-select-translate" --body "…"
```

PR 里说清楚三件事，审核基本就过了：**做什么、Windows only、helper 是本地 exe 且只监听回环**。

合并后：`main` 上 bump 版本 → `publish.yml` 把包打到 `dist` 分支并存进 npm；
magpie 每几小时拉一次 registry.json（拉不到时用内置副本），用户就能在
Plugins → Discover 里搜到 `select translate` / `划词翻译`。

---

## 5. 没上架之前用户怎么装

照样能装，只是搜不到：

```powershell
magpie plugin add @yourname/magpie-select-translate   # npm 包名
magpie plugin add D:\path\to\pro2\plugin              # 本地目录（开发时）
```

---

## 6. 两个坑，先说清楚

1. **首次发布不能走 trusted publishing**。npm 的 Trusted Publisher 只能给"已存在"的包设置，
   所以 A 方式第一次需要一个 magpie-community npm org 的 owner 手动发一次（CI 会在
   `on-npm` 检查里提示，不影响合并）。B 方式你就是 owner，`npm publish` 自己发就行。
2. **`helper.exe` 只编了 windows/amd64**。仓库里的 `build-plugin.ps1` 可以传 `-GoArch arm64`
   另打一份；上架时在 summary 里写明 Windows only，macOS/Linux 用户装不上会来问。

---

## 7. 版本节奏建议

- 插件（`package.json`）和 helper（`cmd/helper`）用同一个版本号，`/api/ping` 里的 `version`
  就是用户上报问题时的依据。
- 只改 helper：也要 bump 插件版本并重新 `npm pack`，否则包里的 exe 还是旧的。
- 改完记得更新 `plugin/README.md` 与 registry `summary`——两者是审核看的东西。