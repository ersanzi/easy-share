# 注册开机自启（当前用户登录时启动知识服务）
# 用法：powershell -ExecutionPolicy Bypass -File scripts\install_autostart.ps1 [-Port 8000]
# 卸载：schtasks /Delete /TN "EasyShare Knowledge" /F
param(
    [int]$Port = 8000
)
$ErrorActionPreference = "Stop"

$startScript = Join-Path $PSScriptRoot "start_server.ps1"
if (-not (Test-Path $startScript)) {
    Write-Error "未找到 start_server.ps1"
    exit 1
}

$taskName = "EasyShare Knowledge"
$command = "powershell -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$startScript`" -Port $Port"
schtasks /Create /TN $taskName /TR $command /SC ONLOGON /RL LIMITED /F | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Error "计划任务创建失败（退出码 $LASTEXITCODE）"
    exit 1
}
Write-Host "已注册开机自启：$taskName（当前用户登录时自动启动，端口 $Port）"
Write-Host "立即启动一次：schtasks /Run /TN `"$taskName`""
Write-Host "提示：若用专用服务器部署，建议在系统设置中为该账号启用自动登录；或改用 SYSTEM 级计划任务。"
