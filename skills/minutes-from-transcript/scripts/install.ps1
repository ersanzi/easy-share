# 一键安装（Windows PowerShell）
# 用法：powershell -ExecutionPolicy Bypass -File install.ps1
#       powershell -ExecutionPolicy Bypass -File install.ps1 -UseVenv   （建 .venv，避免污染系统环境）
param([switch]$UseVenv)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

Write-Host "== 1/4 检查 Python ==" -ForegroundColor Cyan
$python = $null
foreach ($candidate in @("python", "python3", "py")) {
    $found = Get-Command $candidate -ErrorAction SilentlyContinue
    if ($found) { $python = $candidate; break }
}
if (-not $python) {
    Write-Host "未找到 Python。请先安装 3.10+：" -ForegroundColor Red
    Write-Host "  winget install Python.Python.3.12   (装完重开终端)" -ForegroundColor Yellow
    exit 1
}
& $python -c "import sys; sys.exit(0 if sys.version_info >= (3,10) else 1)"
if ($LASTEXITCODE -ne 0) {
    Write-Host "需要 Python 3.10+，当前版本不满足。请升级后重试。" -ForegroundColor Red
    exit 1
}
& $python --version

if ($UseVenv) {
    Write-Host "== 2/4 创建虚拟环境 .venv ==" -ForegroundColor Cyan
    & $python -m venv .venv
    $python = Join-Path $PSScriptRoot ".venv\Scripts\python.exe"
} else {
    Write-Host "== 2/4 直接安装到当前环境 ==" -ForegroundColor Cyan
}

Write-Host "== 3/4 安装 minutecraft ==" -ForegroundColor Cyan
& $python -m pip install --quiet --upgrade pip
& $python -m pip install -e .

Write-Host "== 4/4 验证 ==" -ForegroundColor Cyan
& $python -m minutecraft --version

Write-Host ""
Write-Host "安装完成。下一步：配置 LLM 环境变量后运行" -ForegroundColor Green
Write-Host '  $env:MINUTECRAFT_LLM_BASE_URL="..."; $env:MINUTECRAFT_LLM_API_KEY="..."; $env:MINUTECRAFT_LLM_MODEL="..."'
Write-Host "  minutecraft run <转写文件> --out .\out --json"
