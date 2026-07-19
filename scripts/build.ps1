$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Push-Location frontend
npm test
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
npm run build
if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
Pop-Location

New-Item -ItemType Directory -Force -Path build/bin | Out-Null
go build -o build/bin/easyshare-core.exe ./cmd/core
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$wails = Join-Path (go env GOPATH) 'bin\wails.exe'
& $wails build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

foreach ($binary in 'build/bin/easyshare.exe', 'build/bin/easyshare-core.exe') {
    if (-not (Test-Path -LiteralPath $binary)) {
        throw "Missing build output: $binary"
    }
}

Write-Host 'EasyShare build completed.' -ForegroundColor Green
