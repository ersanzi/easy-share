# EasyShare 插件开发安装辅助（薄壳）：包装 plugins/dev 的 Go 工具。
# 建立/移除开发目录联接（junction）并登记 plugins.json——改文件即时生效。
#
# 用法（仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File plugins\dev.ps1 -Plugin todo          # 建立/刷新映射
#   powershell -ExecutionPolicy Bypass -File plugins\dev.ps1 -Plugin todo -Remove  # 移除映射
#
# 实现是 Go（plugins/dev/main.go，可直接 go run ./plugins/dev -plugin todo）：
# Windows PowerShell 5.1 在本目录以 -File 执行较大脚本时 param 块会被解析丢弃
# （同字节文件在 Temp 目录执行正常、最小脚本正常，与内容语义无关；疑为宿主解析
# 怪病或安全软件按路径介入脚本流），故逻辑不落在 .ps1 里。

param(
    [Parameter(Mandatory = $true)]
    [string]$Plugin,
    [switch]$Remove
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot

$goArgs = @("run", "./plugins/dev", "-plugin", $Plugin)
if ($Remove) { $goArgs += "-remove" }
Push-Location $repoRoot
try {
    & go @goArgs
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
