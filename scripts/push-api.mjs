// 用 GitHub REST API 直接造一个提交（git push 连不上时的兜底）。
// 用法：$env:GH_TOKEN="..." ; node .tmp/push-api.mjs
import fs from "node:fs"

const T = process.env.GH_TOKEN
const OWNER = "cty1735"
const REPO = "magpie-select-translate"

const api = async (path, method = "GET", body) => {
  const res = await fetch(`https://api.github.com${path}`, {
    method,
    headers: {
      Authorization: `token ${T}`,
      "User-Agent": "opencode",
      "content-type": "application/json",
      accept: "application/vnd.github+json",
    },
    body: body ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status}: ${text.slice(0, 400)}`)
  return text ? JSON.parse(text) : {}
}

// 要提交的文件：命令行参数（默认推整个 plugin 包）
const files = process.argv.slice(2)
if (files.length === 0) {
  console.error("用法: node scripts/push-api.mjs <file> [file...]")
  process.exit(1)
}
const message =
  process.env.PUSH_MESSAGE ||
  "更新插件包到 0.1.1（已发布 npm：magpie-select-translate@0.1.1）"

const head = await api(`/repos/${OWNER}/${REPO}/commits/main`)
const baseTree = head.commit.tree.sha
console.log("base commit", head.sha, "tree", baseTree)

const entries = []
for (const f of files) {
  const buf = fs.readFileSync(f)
  const blob = await api(`/repos/${OWNER}/${REPO}/git/blobs`, "POST", {
    content: buf.toString("base64"),
    encoding: "base64",
  })
  entries.push({ path: f, mode: "100644", type: "blob", sha: blob.sha })
  console.log("blob", f, blob.sha, buf.length, "bytes")
}

const tree = await api(`/repos/${OWNER}/${REPO}/git/trees`, "POST", {
  base_tree: baseTree,
  tree: entries,
})
const commit = await api(`/repos/${OWNER}/${REPO}/git/commits`, "POST", {
  message,
  tree: tree.sha,
  parents: [head.sha],
})
console.log("new commit", commit.sha)

const ref = await api(`/repos/${OWNER}/${REPO}/git/refs/heads/main`, "PATCH", {
  sha: commit.sha,
  force: false,
})
console.log("main ->", ref.object.sha)