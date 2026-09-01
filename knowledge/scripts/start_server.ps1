# EasyShare 知识服务启动脚本（公司内部部署用）
# 单 worker 运行：SQLite 状态（任务/查询日志/账号）不允许多 worker。
# 用法：powershell -ExecutionPolicy Bypass -File scripts\start_server.ps1 [-Port 8000]
param(
    [int]$Port = 8000
)
$ErrorActionPreference = "Stop"

# 切到 knowledge 目录（脚本在 knowledge/scripts/ 下）
$knowledgeDir = Split-Path -Parent $PSScriptRoot
Set-Location $knowledgeDir

if (-not (Test-Path ".venv\Scripts\python.exe")) {
    Write-Error "未找到虚拟环境 .venv。请先按 knowledge/README.md「快速开始」完成安装，或跑 scripts\deploy.ps1 一键部署。"
    exit 1
}
if (-not (Test-Path ".env")) {
    Write-Warning "未找到 .env 配置文件，将使用默认配置。建议：Copy-Item .env.example .env 并填写。"
}

Write-Host "启动 EasyShare 知识服务：http://0.0.0.0:$Port"
Write-Host "同事访问地址：http://<本机IP>:$Port/lab    停止：Ctrl+C"

& .venv\Scripts\python.exe -m uvicorn app.main:app --host 0.0.0.0 --port $Port --workers 1
