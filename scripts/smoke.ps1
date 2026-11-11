# 冒烟测试：对着真实运行的 helper + magpie 网关跑一轮接口用例。
#
#   powershell -ExecutionPolicy Bypass -File scripts\smoke.ps1 -Port 3477
#
param([int]$Port = 3477, [string]$Model = "")

$ErrorActionPreference = "Continue"
$base = "http://127.0.0.1:$Port"
$utf8 = New-Object System.Text.UTF8Encoding($false)
$tmp = Join-Path $env:TEMP "magpie-smoke.json"
$fail = 0

function Post($path, $obj) {
    [IO.File]::WriteAllText($tmp, ($obj | ConvertTo-Json -Compress), $utf8)
    return curl.exe -sN -X POST "$base$path" -H "content-type: application/json" --data-binary "@$tmp"
}

function Step($name, $ok, $detail) {
    $tag = if ($ok) { "PASS" } else { "FAIL"; $script:fail++ }
    Write-Host ("[{0}] {1} :: {2}" -f $tag, $name, $detail)
}

# 1. 探活
try {
    $ping = Invoke-RestMethod "$base/api/ping" -TimeoutSec 3
    Step "ping" ($ping.ok -eq $true) ("version=$($ping.version) gateway=$($ping.gateway) hotkey=$($ping.hotkey)")
} catch {
    Step "ping" $false $_.Exception.Message
    exit 1
}

# 2. 正常翻译 + 首字延迟
$sw = [Diagnostics.Stopwatch]::StartNew()
$out = Post "/api/translate" @{ text = "Hello world, this is a smoke test."; target = ""; model = $Model }
$sw.Stop()
$joined = $out -join "`n"
$hasDelta = $joined -match '"delta"'
$hasDone = $joined -match '"done"'
Step "translate" ($hasDelta -and $hasDone) ("elapsed=$($sw.ElapsedMilliseconds)ms lines=$($out.Count)")

# 3. 缓存命中
$sw2 = [Diagnostics.Stopwatch]::StartNew()
$out2 = Post "/api/translate" @{ text = "Hello world, this is a smoke test."; target = ""; model = $Model }
$sw2.Stop()
Step "cache-hit" (($out2 -join "") -match '"cached":true') ("elapsed=$($sw2.ElapsedMilliseconds)ms")

# 4. 太短
$out3 = Post "/api/translate" @{ text = "a"; target = ""; model = $Model }
Step "short-text" (($out3 -join "") -match '"code":"short"') ($out3 -join " ")

# 5. 模型不存在 -> 404 映射
$out4 = Post "/api/translate" @{ text = "Hello there friend"; target = ""; model = "nope/nope" }
Step "model-missing" (($out4 -join "") -match '"code":"model"') ($out4 -join " ")

# 6. 剪贴板写入 + 读回
$before = Get-Clipboard -Raw -ErrorAction SilentlyContinue
$r = Post "/api/clipboard" @{ text = "smoke-clipboard-42" }
$back = Get-Clipboard -Raw -ErrorAction SilentlyContinue
Step "clipboard" ($back -match "smoke-clipboard-42") ("write=" + ($r -join ""))
if ($null -ne $before) { Set-Clipboard -Value $before }

# 7. 设置读写
$s = Invoke-RestMethod "$base/api/settings"
Step "settings-get" ($s.port -eq $Port) ("hotkey=$($s.hotkey) target=$($s.target) restore=$($s.restoreClipboard)")

Write-Host ""
if ($fail -eq 0) { Write-Host "ALL PASS" } else { Write-Host "$fail case(s) failed"; exit 1 }
