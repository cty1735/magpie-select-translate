# 编译并安装 helper（落地到 %LOCALAPPDATA%\magpie-translate，写启动文件夹自启）
#
#   powershell -ExecutionPolicy Bypass -File scripts\install.ps1
#
param(
    [int]$Port = 3477,
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$dest = Join-Path $env:LOCALAPPDATA "magpie-translate"
New-Item -ItemType Directory -Force $dest | Out-Null

Write-Host "==> go build"
Push-Location $root
try {
    go build -ldflags "-H windowsgui -s -w" -o (Join-Path $dest "helper.exe") ./cmd/helper
} finally {
    Pop-Location
}

$settings = Join-Path $dest "settings.json"
if (-not (Test-Path $settings)) {
    $json = @"
{
  "port": $Port,
  "target": "auto",
  "hotkey": "Alt+D",
  "trigger": "hotkey",
  "restoreClipboard": true
}
"@
    [IO.File]::WriteAllText($settings, $json, (New-Object System.Text.UTF8Encoding($false)))
}

# 自启：.vbs 必须 CRLF，否则 WSH 报语法错误
$startup = [Environment]::GetFolderPath("Startup")
$vbs = Join-Path $startup "start-magpie-translate.vbs"
$exe = Join-Path $dest "helper.exe"
$content = 'Set sh = CreateObject("WScript.Shell")' + "`r`n" +
           'sh.Run """' + $exe + '""", 0, False' + "`r`n"
[IO.File]::WriteAllText($vbs, $content, (New-Object System.Text.UTF8Encoding($false)))
Write-Host "==> 自启已写入 $vbs"

if (-not $NoStart) {
    Get-Process magpie-translate-helper -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Milliseconds 300
    Start-Process $exe -WindowStyle Hidden
    Start-Sleep -Seconds 2
    try {
        $ping = Invoke-RestMethod "http://127.0.0.1:$Port/api/ping" -TimeoutSec 3
        Write-Host "==> helper 已运行：$($ping | ConvertTo-Json -Compress)"
    } catch {
        Write-Warning "helper 启动后 /api/ping 无响应，看 $dest\helper.log"
    }
}

Write-Host "==> 日志：$dest\helper.log"
Write-Host "==> 到 magpie 里加载插件：magpie plugin add `"$root\plugin`""
