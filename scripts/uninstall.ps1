# 卸载 helper：停进程、删自启、删安装目录
#
#   powershell -ExecutionPolicy Bypass -File scripts\uninstall.ps1
#
$ErrorActionPreference = "Stop"
$dest = Join-Path $env:LOCALAPPDATA "magpie-translate"
$vbs = Join-Path ([Environment]::GetFolderPath("Startup")) "start-magpie-translate.vbs"

Get-Process magpie-translate-helper -ErrorAction SilentlyContinue | Stop-Process -Force
if (Test-Path $vbs) {
    Remove-Item $vbs -Force
    Write-Host "==> 已删除自启 $vbs"
}
if (Test-Path $dest) {
    Remove-Item $dest -Recurse -Force
    Write-Host "==> 已删除 $dest"
}
Write-Host "==> 完成。magpie 里的插件用 magpie plugin remove magpie-select-translate 移除"
