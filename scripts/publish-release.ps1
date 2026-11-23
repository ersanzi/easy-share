# EasyShare 控制面发布兼容入口；CI 与手工发布共用 Node 发布器和同一份校验。
# 凭据推荐通过 EASYSHARE_RELEASE_USERNAME/PASSWORD 注入，不写入命令或仓库。
param(
    [string]$PlatformUrl = $env:EASYSHARE_RELEASE_URL,
    [string]$Username = $env:EASYSHARE_RELEASE_USERNAME,
    [string]$Password = $env:EASYSHARE_RELEASE_PASSWORD,
    [string]$InstallerPath = 'build\bin\EasyShare-amd64-installer.exe',
    [string]$Version = '',
    [string]$Notes = '',
    [string]$Platform = 'windows',
    [string]$Kind = 'installer'
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not $PlatformUrl) { $PlatformUrl = $env:PLATFORM_URL }
if (-not $PlatformUrl) { throw '请配置发布控制面地址（PlatformUrl 或 EASYSHARE_RELEASE_URL），不回退 localhost' }
if (-not $Username -or -not $Password) { throw '请通过环境变量配置发布账号，禁止使用默认 admin 口令' }
if (-not (Get-Command node -ErrorAction SilentlyContinue)) { throw '发布需要 Node.js 22+' }
if (-not [System.IO.Path]::IsPathRooted($InstallerPath)) { $InstallerPath = Join-Path $repoRoot $InstallerPath }

Push-Location $repoRoot
$oldUsername = $env:EASYSHARE_RELEASE_USERNAME
$oldPassword = $env:EASYSHARE_RELEASE_PASSWORD
try {
    # 保留旧参数入口，但绝不把口令继续传给子进程命令行。
    $env:EASYSHARE_RELEASE_USERNAME = $Username
    $env:EASYSHARE_RELEASE_PASSWORD = $Password
    $check = @('scripts/check-release-version.mjs')
    if ($Version) { $check += @('--tag', "v$Version") }
    & node @check
    if ($LASTEXITCODE -ne 0) { throw '产品版本不一致，发布已中止' }
    if (-not $Version) {
        $matched = Select-String -Path 'internal/version/version.go' -Pattern '^const\s+Version\s*=\s*"([^"]+)"'
        $Version = $matched.Matches[0].Groups[1].Value
    }
    $arguments = @('scripts/publish-release.mjs', '--platform-url', $PlatformUrl,
        '--installer-path', $InstallerPath, '--version', $Version, '--platform', $Platform, '--kind', $Kind)
    if ($Notes) { $arguments += @('--notes', $Notes) }
    & node @arguments
    if ($LASTEXITCODE -ne 0) { throw '发布失败，请查看上方已脱敏的阶段诊断' }
} finally {
    $env:EASYSHARE_RELEASE_USERNAME = $oldUsername
    $env:EASYSHARE_RELEASE_PASSWORD = $oldPassword
    Pop-Location
}
