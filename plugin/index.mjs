// magpie 插件入口：只负责探活 + 拉起 helper，翻译逻辑全在 helper 里。
import { spawn } from "node:child_process"
import { copyFileSync, existsSync, mkdirSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const home = join(process.env.LOCALAPPDATA || process.env.HOME || ".", "magpie-translate")

const alive = async (port) => {
  try {
    const res = await fetch(`http://127.0.0.1:${port}/api/ping`, {
      signal: AbortSignal.timeout(800),
    })
    return res.ok
  } catch {
    return false
  }
}

// 优先用已安装（%LOCALAPPDATA%）的那份；npm 包自带 bin/helper.exe 时就地装过去。
// 装到固定目录是为了让设置、日志、卸载脚本只有一份。
const ensureHelper = () => {
  const installed = join(home, "helper.exe")
  if (existsSync(installed)) return installed
  const bundled = join(here, "bin", "helper.exe")
  if (!existsSync(bundled)) return ""
  try {
    mkdirSync(home, { recursive: true })
    copyFileSync(bundled, installed)
    // 顺手留个卸载脚本，用户不用回来翻 node_modules
    const uninstall = join(here, "bin", "uninstall.ps1")
    if (existsSync(uninstall)) copyFileSync(uninstall, join(home, "uninstall.ps1"))
  } catch {
    return bundled // 装不过去就直接跑包里的那份
  }
  return installed
}

export default {
  id: "magpie-select-translate",
  server: async (_input, options) => {
    const port = Number(options?.port) || 3477
    if (await alive(port)) return { port }

    const exe = ensureHelper()
    if (!exe) {
      return {
        port,
        error:
          "helper 未安装：运行仓库里的 scripts/install.ps1，或安装带 bin/helper.exe 的发行包",
      }
    }
    // 页面只能写 magpie.options，所以设置由这里转成命令行交给 helper（helper 落地成自己的 settings.json）
    const args = ["--port", String(port)]
    for (const k of ["hotkey", "model", "target", "glossary"]) {
      const v = options?.[k]
      if (v) args.push(`--${k === "glossary" ? "glossary-text" : k}`, String(v))
    }
    spawn(exe, args, {
      detached: true,
      stdio: "ignore",
      windowsHide: true,
    }).unref()
    return { port }
  },
}