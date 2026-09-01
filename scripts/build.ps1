param(
    # 公司部署构建注入控制面地址：安装包开箱即指向公司服务器，同事免手工改 config.json
    # 例：powershell -ExecutionPolicy Bypass -File scripts/build.ps1 -PlatformUrl http://192.168.1.10:8090
    [string]$PlatformUrl = ""
)

$ErrorActionPreference = 'Stop'

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
