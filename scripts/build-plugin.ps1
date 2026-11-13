# 打出可发布的插件包：编译 helper → 放进 plugin/bin → npm pack
#
#   powershell -ExecutionPolicy Bypass -File scripts\build-plugin.ps1
#
# 产物：plugin\bin\helper.exe 和仓库根的 magpie-select-translate-<ver>.tgz
# 发布：npm publish .\magpie-select-translate-0.1.0.tgz
param(
    [string]$GoOS = "windows",
    [string]$GoArch = "amd64"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$plugin = Join-Path $root "plugin"
$bin = Join-Path $plugin "bin"
New-Item -ItemType Directory -Force $bin | Out-Null

Write-Host "==> 编译 helper ($GoOS/$GoArch)"
Push-Location $root
try {
    $env:GOOS = $GoOS
    $env:GOARCH = $GoArch
    $env:CGO_ENABLED = "0"
    $out = Join-Path $bin "helper.exe"
    go build -trimpath -ldflags "-H windowsgui -s -w" -o $out ./cmd/helper
} finally {
    Pop-Location
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

$size = [math]::Round((Get-Item (Join-Path $bin "helper.exe")).Length / 1MB, 1)
Write-Host "==> helper.exe $size MB"

# 卸载脚本跟着包走：用户 %LOCALAPPDATA%\magpie-translate\uninstall.ps1 就能清干净
Copy-Item -Force (Join-Path $PSScriptRoot "uninstall.ps1") (Join-Path $bin "uninstall.ps1")

Write-Host "==> npm pack"
# Bun 的 JSON.parse 和 magpie 的 json.Unmarshal 都拒收 BOM：打包前强制无 BOM
$pkgJson = Join-Path $plugin "package.json"
[IO.File]::WriteAllText($pkgJson, [IO.File]::ReadAllText($pkgJson), (New-Object System.Text.UTF8Encoding($false)))
Push-Location $plugin
try {
    $tgz = npm pack --silent 2>$null | Select-Object -Last 1
    if ($tgz) {
        Move-Item -Force (Join-Path $plugin $tgz) (Join-Path $root $tgz)
        Write-Host "==> 产物 $root\$tgz"
        Write-Host "==> 包内容："
        tar -tzf (Join-Path $root $tgz) | Select-Object -First 20
    }
} finally {
    Pop-Location
}

Write-Host ""
Write-Host "下一步："
Write-Host "  1) 本地试：magpie plugin add `"$plugin`""
Write-Host "  2) 发布：npm publish <root>\<tgz>  （先 npm login，包名要改成你拥有的 scope）"
Write-Host "  3) 上架到 magpie 的 Discover：向 magpie-community/plugins 提 PR，加一条 registry.json 记录"
Write-Host "     详见 docs/PUBLISH.md"