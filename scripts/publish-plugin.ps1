# EasyShare 插件发布脚本（商城上架）：登录控制面 → 预签名直传插件 zip → 发布 → 验证清单。
#
# 用法（在仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File scripts/publish-plugin.ps1 -PluginDir plugins-src\todo
#   powershell -ExecutionPolicy Bypass -File scripts/publish-plugin.ps1 -PluginDir plugins-src\todo -Version 1.0.1 -Notes "修复XX"
#
# 前置：控制面（RuoYi + platform-drive）已运行且已执行 easyshare-plugin.sql；RustFS 可达。
# 流程：读取插件目录 manifest.json → 打 zip（manifest.json 必须在包根）→ 上传两段式 → 发布。
# 插件目录结构见 docs/iterations/2026-08-31-plugin-system.md（manifest.json + index.html + 资源）。

param(
    [Parameter(Mandatory = $true)]
    [string]$PluginDir,
    [string]$PlatformUrl = "http://localhost:8090",
    [string]$Username = "admin",
    [string]$Password = "admin123",
    [string]$Version = "",
    [string]$Notes = ""
)

$ErrorActionPreference = "Stop"

# RuoYi PC 端 clientId（同 internal/account/client.go；32 位，勿手抄漏段）
$ClientId = "e5cd7e4891bf95d1d19206ce24a7b32e"

# ── 1) 定位插件目录与 manifest ──
$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not [System.IO.Path]::IsPathRooted($PluginDir)) {
    $PluginDir = Join-Path $repoRoot $PluginDir
}
if (-not (Test-Path $PluginDir)) { throw "插件目录不存在：$PluginDir" }
$manifestPath = Join-Path $PluginDir "manifest.json"
if (-not (Test-Path $manifestPath)) { throw "缺少 manifest.json：$manifestPath" }
$manifest = Get-Content $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
$pluginId = $manifest.id
$pluginName = $manifest.name
if ([string]::IsNullOrWhiteSpace($pluginId)) { throw "manifest.json 缺少 id" }
if ([string]::IsNullOrWhiteSpace($pluginName)) { throw "manifest.json 缺少 name" }
if ([string]::IsNullOrWhiteSpace($Version)) { $Version = $manifest.version }
if ([string]::IsNullOrWhiteSpace($Version)) { throw "未提供 -Version 且 manifest.json 缺少 version" }

# ── 2) 打 zip（包根 = 插件目录内容，manifest.json 在根）──
$zipName = "$pluginId-$Version.zip"
$zipPath = Join-Path ([System.IO.Path]::GetTempPath()) $zipName
if (Test-Path $zipPath) { Remove-Item $zipPath -Force }
Compress-Archive -Path (Join-Path $PluginDir "*") -DestinationPath $zipPath -CompressionLevel Optimal
$item = Get-Item $zipPath

# ── 3) 摘要与大小 ──
$sha256 = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()
$sizeBytes = $item.Length

Write-Host "发布插件 $pluginId v$Version"
Write-Host "  名称   : $pluginName"
Write-Host "  包     : $zipName（$sizeBytes 字节）"
Write-Host "  SHA256 : $sha256"
Write-Host "  控制面 : $PlatformUrl"

# ── 4) 登录 ──
$loginBody = @{ clientId = $ClientId; grantType = "password"; username = $Username; password = $Password } | ConvertTo-Json
$login = Invoke-RestMethod -Method Post -Uri "$PlatformUrl/auth/login" `
    -ContentType "application/json" -Headers @{ clientid = $ClientId } -Body $loginBody
if ($login.code -ne 200) { throw "登录失败：$($login.msg)" }
$token = $login.data.access_token
if ([string]::IsNullOrWhiteSpace($token)) { throw "登录成功但未返回 access_token" }
$authHeaders = @{ clientid = $ClientId; Authorization = "Bearer $token" }
Write-Host "登录成功（$Username）"

# ── 5) 上传准备：登记 + 预签名 PUT ──
# PS 5.1 字符串 Body 默认 ISO-8859-1，中文必须显式 UTF-8 字节（同 publish-release.ps1）。
$uploadBody = @{
    pluginId    = $pluginId
    name        = $pluginName
    description = $manifest.description
    icon        = $manifest.icon
    author      = $manifest.author
    version     = $Version
    filename    = $zipName
    sizeBytes   = $sizeBytes
    sha256      = $sha256
    notes       = $Notes
} | ConvertTo-Json
$prepared = Invoke-RestMethod -Method Post -Uri "$PlatformUrl/easyshare/plugins/admin/uploads" `
    -ContentType "application/json; charset=utf-8" -Headers $authHeaders `
    -Body ([System.Text.Encoding]::UTF8.GetBytes($uploadBody))
if ($prepared.code -ne 200) { throw "上传准备失败：$($prepared.msg)" }
$assetId = $prepared.data.assetId
$uploadUrl = $prepared.data.uploadUrl
Write-Host "资产已登记（assetId=$assetId），开始直传 RustFS..."

# ── 6) 直传（预签名 PUT，字节不经控制面）──
$null = Invoke-WebRequest -Method Put -Uri $uploadUrl -InFile $zipPath `
    -ContentType "application/zip" -UseBasicParsing
Write-Host "直传完成"

# ── 7) 发布（控制面校验对象存在且大小一致）──
$published = Invoke-RestMethod -Method Post -Uri "$PlatformUrl/easyshare/plugins/admin/assets/$assetId/publish" `
    -Headers $authHeaders
if ($published.code -ne 200) { throw "发布失败：$($published.msg)" }
Write-Host "已发布"

# ── 8) 验证清单（匿名接口，模拟客户端视角）──
$latest = Invoke-RestMethod -Method Get -Uri "$PlatformUrl/easyshare/plugins/$pluginId/latest"
if ($latest.code -ne 200 -or $null -eq $latest.data) { throw "清单验证失败：latest 未返回数据" }
if ($latest.data.version -ne $Version) { throw "清单版本不符：期望 $Version，实际 $($latest.data.version)" }
Write-Host "清单验证通过：$pluginId latest = $($latest.data.version)"
Remove-Item $zipPath -Force -ErrorAction SilentlyContinue
Write-Host "`n上架完成。客户端「插件中心」即可安装。"
