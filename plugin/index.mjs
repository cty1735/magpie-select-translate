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

// 包里带了 bin/helper.exe 就以包里为准（能跑到这里说明端口上没有活着的 helper）：
// 早先是"已装过就沿用"，结果老用户升级插件后永远停在第一次装的那份旧 exe。
// 装不进去（旧 helper 还留着文件锁）就退回用已装的，至少能用。
const ensureHelper = () => {
  const installed = join(home, "helper.exe")
  const bundled = join(here, "bin", "helper.exe")
  if (!existsSync(bundled)) return existsSync(installed) ? installed : ""
  try {
    mkdirSync(home, { recursive: true })
    copyFileSync(bundled, installed)
    // 顺手留个卸载脚本，用户不用回来翻 node_modules
    const uninstall = join(here, "bin", "uninstall.ps1")
    if (existsSync(uninstall)) copyFileSync(uninstall, join(home, "uninstall.ps1"))
  } catch {
    return existsSync(installed) ? installed : bundled
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
    // 只传端口：设置以 helper 自己的 settings.json 为准，
    // 插件宿主重启时不能拿 options 里的旧值把用户刚改的设置盖回去。
    spawn(exe, ["--port", String(port)], {
      detached: true,
      stdio: "ignore",
      windowsHide: true,
    }).unref()
    return { port }
  },
}