param(
    # 公司部署构建注入控制面地址：安装包开箱即指向公司服务器，同事免手工改 config.json
    # 例：powershell -ExecutionPolicy Bypass -File scripts/build.ps1 -PlatformUrl http://192.168.1.10:8090
    [string]$PlatformUrl = ""
)

$ErrorActionPreference = 'Stop'

# node 闸门脚本输出中文，Windows PowerShell 5.1 默认按 GBK 读控制台，不改就是一片乱码——报错读不懂等于没有闸门
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# 控制面默认地址的 ldflags 注入（internal/config.defaultPlatformBaseURL 由 const 改 var 即为此）
$ldflags = ""
if ($PlatformUrl) {
    $ldflags = "-X easyshare/internal/config.defaultPlatformBaseURL=$PlatformUrl"
    Write-Host "Platform URL baked in: $PlatformUrl" -ForegroundColor Cyan
}

# Ensure NSIS is in PATH for installer build
if (-not (Get-Command makensis -ErrorAction SilentlyContinue)) {
    $nsisPaths = @(
        "${env:ProgramFiles(x86)}\NSIS",
        "${env:ProgramFiles}\NSIS"
    )
    foreach ($p in $nsisPaths) {
        if (Test-Path (Join-Path $p 'makensis.exe')) {
            $env:PATH = "$p;$env:PATH"
            Write-Host "NSIS found at: $p" -ForegroundColor DarkGray
            break
        }
    }
}

# ── 闸门：写在文档里的纪律必须能被机器复算，否则「已收口」只是一句自述 ──
# 顺序按「便宜且会先红」排：文档证据引用 → Go 测试 → 前端（vitest 内含设计 token 守卫，npm run build 内含 vue-tsc 类型检查）
node scripts/diag/check-doc-refs.mjs --self-test
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
node scripts/diag/check-doc-refs.mjs docs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Push-Location frontend
npm test
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
npm run build
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

New-Item -ItemType Directory -Force -Path build/bin | Out-Null
if ($ldflags) { go build -ldflags $ldflags -o build/bin/easyshare-core.exe ./cmd/core }
else { go build -o build/bin/easyshare-core.exe ./cmd/core }
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$wails = Join-Path (go env GOPATH) 'bin\wails.exe'
if ($ldflags) { & $wails build --nsis -ldflags $ldflags }
else { & $wails build --nsis }
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

foreach ($binary in 'build/bin/easyshare.exe', 'build/bin/easyshare-core.exe') {
    if (-not (Test-Path -LiteralPath $binary)) {
        throw "Missing build output: $binary"
    }
}

$installer = Get-ChildItem -Path build/bin -Filter '*-installer.exe' -ErrorAction SilentlyContinue | Select-Object -First 1
if ($installer) {
    Write-Host "Installer: $($installer.FullName)" -ForegroundColor Cyan
} else {
    Write-Host 'Warning: installer not found in build/bin (NSIS may not be installed)' -ForegroundColor Yellow
}

Write-Host 'EasyShare build completed.' -ForegroundColor Green
