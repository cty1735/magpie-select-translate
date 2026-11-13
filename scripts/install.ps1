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
$exe = Join-Path $dest "helper.exe"
New-Item -ItemType Directory -Force $dest | Out-Null

# 先停掉正在跑的 helper：进程可执行文件被占用时 go build 写不进去（会静默留下旧 exe），
# 而且没停掉的话新进程抢不到端口，只留一行 "port busy" 就退出——看起来"装好了"其实还在跑老版本。
# 注意进程名是 helper（exe 名），不是 magpie-translate-helper；按路径比一遍再杀，别误伤同名进程。
Get-Process -Name helper -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -eq $exe } |
    ForEach-Object { Write-Host "==> 停掉旧 helper pid=$($_.Id)"; Stop-Process -Id $_.Id -Force }
Start-Sleep -Milliseconds 300

Write-Host "==> go build"
Push-Location $root
try {
    go build -ldflags "-H windowsgui -s -w" -o $exe ./cmd/helper
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
$content = 'Set sh = CreateObject("WScript.Shell")' + "`r`n" +
           'sh.Run """' + $exe + '""", 0, False' + "`r`n"
[IO.File]::WriteAllText($vbs, $content, (New-Object System.Text.UTF8Encoding($false)))
Write-Host "==> 自启已写入 $vbs"

if (-not $NoStart) {
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
