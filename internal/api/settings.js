// 设置页脚本，只跑在 helper 自己的页面里。token 由 helper 打开 html 时注入，
// 页面被别的站点装在 iframe 里也读不到它，所以接口只认这个头。
const TOKEN = "__TOKEN__"

const $ = (id) => document.getElementById(id)

const api = async (path, init = {}) => {
  const res = await fetch(path, {
    ...init,
    cache: "no-store",
    headers: { "X-Helper-Token": TOKEN, "Content-Type": "application/json" },
  })
  if (!res.ok) throw new Error((await res.text()).trim() || "HTTP " + res.status)
  return res.json()
}

const status = (msg, bad) => {
  $("status").textContent = msg
  $("status").className = bad ? "bad" : ""
}

let cfg = {}

// 模型列表来自网关 /v1/models：默认第一个就是网关的默认模型，留空即用它。
async function loadModels() {
  const data = await api("/api/models")
  const list = $("models")
  list.textContent = ""
  for (const id of data.models || []) {
    const opt = document.createElement("option")
    opt.value = id
    list.appendChild(opt)
  }
  if (data.error) $("modelTip").textContent = "读取网关模型失败：" + data.error
  else if (!(data.models || []).length) $("modelTip").textContent = "网关没有给出任何模型"
  else {
    let tip = `共 ${data.models.length} 个模型；留空用网关默认（${data.default}）`
    if (data.current) tip += `；当前固定用 ${data.current}`
    $("modelTip").textContent = tip
  }
}

async function load() {
  cfg = await api("/api/settings")
  $("hotkey").value = cfg.hotkey || ""
  $("target").value = ["auto", "zh", "en"].includes(cfg.target) ? cfg.target : "auto"
  $("model").value = cfg.model || ""
  $("glossary").value = cfg.glossary || ""
  status("")
  // 先读模型：这一步会解析网关，之后 ping 里的网关地址才是真的（否则一直显示“探测中”）
  await loadModels().catch((e) => { $("modelTip").textContent = "读取网关模型失败：" + e.message })
  try {
    const ping = await api("/api/ping")
    $("foot").textContent = `helper ${ping.version} · 127.0.0.1:${ping.port} · 网关 ${ping.gateway || "探测中"}`
  } catch { /* 版本信息只是给自己看的 */ }
}

async function save() {
  $("save").disabled = true
  status("保存中…")
  try {
    cfg = await api("/api/settings", {
      method: "PUT",
      body: JSON.stringify({
        ...cfg,
        hotkey: $("hotkey").value.trim(),
        target: $("target").value,
        model: $("model").value.trim(),
        glossary: $("glossary").value,
      }),
    })
    status("已保存，立即生效 · " + new Date().toLocaleTimeString())
    loadModels().catch(() => {})
  } catch (e) {
    status("保存失败：" + e.message, true)
  } finally {
    $("save").disabled = false
  }
}

$("save").addEventListener("click", save)
load().catch((e) => status("读取设置失败：" + e.message, true))
