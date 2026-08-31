# EasyShare 客户端发布脚本：登录控制面 → 预签名直传安装包 → 发布 → 验证清单。
#
# 用法（在仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File scripts/publish-release.ps1
#   powershell -ExecutionPolicy Bypass -File scripts/publish-release.ps1 -Notes "修复XX" -Version 0.1.1
#
# 前置：控制面（RuoYi + platform-drive）已运行，RustFS 可达；安装包已由 scripts/build.ps1 产出。
# 版本号默认从 internal/version/version.go 解析；修改版本须同步 wails.json 与 frontend/package.json。

param(
    [string]$PlatformUrl = "http://localhost:8090",
    [string]$Username = "admin",
    [string]$Password = "admin123",
    [string]$InstallerPath = "build\bin\EasyShare-amd64-installer.exe",
    [string]$Version = "",
    [string]$Notes = "",
    [string]$Platform = "windows",
    [string]$Kind = "installer"
)

$ErrorActionPreference = "Stop"

# RuoYi PC 端 clientId（同 internal/account/client.go）
$ClientId = "e5cd7e4891bf95d1d19206ce24a7b32e"

# ── 1) 定位安装包 ──
$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not [System.IO.Path]::IsPathRooted($InstallerPath)) {
    $InstallerPath = Join-Path $repoRoot $InstallerPath
}
if (-not (Test-Path $InstallerPath)) {
    throw "安装包不存在：$InstallerPath（先跑 scripts/build.ps1）"
}
$item = Get-Item $InstallerPath

# ── 2) 版本号：参数优先，否则从 internal/version/version.go 解析 ──
if ([string]::IsNullOrWhiteSpace($Version)) {
    $versionFile = Join-Path $repoRoot "internal\version\version.go"
    if (-not (Test-Path $versionFile)) { throw "找不到 $versionFile" }
    $matched = Select-String -Path $versionFile -Pattern 'Version\s*=\s*"([^"]+)"'
    if (-not $matched) { throw "无法从 version.go 解析版本号" }
    $Version = $matched.Matches[0].Groups[1].Value
}

# ── 3) 摘要与大小 ──
$sha256 = (Get-FileHash -Path $InstallerPath -Algorithm SHA256).Hash.ToLower()
$sizeBytes = $item.Length

Write-Host "发布 $([System.IO.Path]::GetFileName($InstallerPath))"
Write-Host "  版本   : $Version"
Write-Host "  平台   : $Platform / $Kind"
Write-Host "  大小   : $sizeBytes 字节"
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

# ── 5) 上传准备：建记录 + 预签名 PUT ──
# 注意：PS 5.1 的 Invoke-RestMethod 对字符串 Body 默认按 ISO-8859-1 编码，
# 中文 Notes 会变问号——必须显式转 UTF-8 字节再发。
$uploadBody = @{
    version   = $Version
    platform  = $Platform
    kind      = $Kind
    filename  = $item.Name
    sizeBytes = $sizeBytes
    sha256    = $sha256
    notes     = $Notes
} | ConvertTo-Json
$prepared = Invoke-RestMethod -Method Post -Uri "$PlatformUrl/easyshare/app/admin/uploads" `
    -ContentType "application/json; charset=utf-8" -Headers $authHeaders `
    -Body ([System.Text.Encoding]::UTF8.GetBytes($uploadBody))
if ($prepared.code -ne 200) { throw "上传准备失败：$($prepared.msg)" }
$assetId = $prepared.data.assetId
$uploadUrl = $prepared.data.uploadUrl
Write-Host "资产已登记（assetId=$assetId），开始直传 RustFS..."

# ── 6) 直传（预签名 PUT，字节不经控制面）──
$null = Invoke-WebRequest -Method Put -Uri $uploadUrl -InFile $InstallerPath `
    -ContentType "application/octet-stream" -UseBasicParsing
Write-Host "直传完成"

# ── 7) 发布（控制面校验对象存在且大小一致）──
$published = Invoke-RestMethod -Method Post -Uri "$PlatformUrl/easyshare/app/admin/assets/$assetId/publish" `
    -Headers $authHeaders
if ($published.code -ne 200) { throw "发布失败：$($published.msg)" }
Write-Host "已发布"

# ── 8) 验证清单（匿名接口，模拟客户端视角）──
$manifest = Invoke-RestMethod -Method Get -Uri "$PlatformUrl/easyshare/app/latest?platform=$Platform"
if ($manifest.code -ne 200 -or $null -eq $manifest.data) { throw "清单验证失败：latest 未返回数据" }
if ($manifest.data.version -ne $Version) { throw "清单版本不符：期望 $Version，实际 $($manifest.data.version)" }
Write-Host "清单验证通过：latest = $($manifest.data.version)，资产 $($manifest.data.assets.Count) 个"
Write-Host "`n发布完成。客户端「设置 → 检查更新」即可看到新版本。"
