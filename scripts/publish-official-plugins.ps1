# EasyShare 官方插件批量上架（保证商城默认有货）。
#
# 官方插件 = plugins/ 下含 manifest.json 的目录（template 模板除外）。新增插件只要
# 在 plugins/ 建目录即自动纳入上架清单，无需改本脚本——商城货架由我们维护，新用户
# 打开「插件中心」不会看到空列表，也不会撞见发布脚本这类内部细节。
#
# 用法（在仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File scripts/publish-official-plugins.ps1
#   指向公司服务器：加 -PlatformUrl http://<IP>:<端口> -Username admin -Password <密码>
#   只看会上架哪些、不真发：加 -DryRun
#
# 前置：控制面（RuoYi + platform-drive）已运行且已执行 easyshare-plugin.sql；RustFS 可达。
# 幂等：同版本重传 = 覆盖发布，可安全重复执行；留空 -Notes 不会抹掉既有版本的更新说明。
# 单个插件带版本说明正式发布，仍用 scripts/publish-plugin.ps1 -PluginDir ... -Notes "..."。

param(
    [string]$PlatformUrl = "http://localhost:8090",
    [string]$Username = "admin",
    [string]$Password = "admin123",
    [string]$Notes = "",
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$pluginsRoot = Join-Path $repoRoot "plugins"
$singlePublisher = Join-Path $PSScriptRoot "publish-plugin.ps1"

if (-not (Test-Path $pluginsRoot)) { throw "插件目录不存在：$pluginsRoot" }
if (-not (Test-Path $singlePublisher)) { throw "缺少单插件发布脚本：$singlePublisher" }

# ── 1) 发现官方插件（身份以 manifest.id 为准，目录名只是习惯）──
# template 是新建插件的复制源（id=template），不是可发布的产品，排除。
$official = @()
foreach ($dir in (Get-ChildItem $pluginsRoot -Directory | Where-Object { $_.Name -ne "template" } | Sort-Object Name)) {
    $manifestPath = Join-Path $dir.FullName "manifest.json"
    if (-not (Test-Path $manifestPath)) { continue }
    $manifest = Get-Content $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ([string]::IsNullOrWhiteSpace($manifest.id) -or [string]::IsNullOrWhiteSpace($manifest.version)) {
        throw "$($dir.Name) 的 manifest.json 缺少 id 或 version"
    }
    $official += [pscustomobject]@{ Id = $manifest.id; Version = $manifest.version; Dir = $dir.FullName }
}

if (-not $official) { throw "$pluginsRoot 下没有含 manifest.json 的插件目录" }

Write-Host "发现 $($official.Count) 个官方插件：$((($official | ForEach-Object { "$($_.Id) v$($_.Version)" }) -join ', '))"
Write-Host "控制面：$PlatformUrl"
if ($DryRun) { Write-Host "（-DryRun：只列出，不上传）"; exit 0 }

# ── 2) 逐个上架；单个失败不中断其余，最后汇总退出码 ──
$failed = @()
foreach ($p in $official) {
    Write-Host "`n==== 上架 $($p.Id) v$($p.Version) ====" -ForegroundColor Cyan
    try {
        & $singlePublisher -PluginDir $p.Dir -PlatformUrl $PlatformUrl `
            -Username $Username -Password $Password -Notes $Notes
    } catch {
        Write-Host "  失败：$($_.Exception.Message)" -ForegroundColor Red
        $failed += $p.Id
    }
}

# ── 3) 匿名核对货架（客户端视角：每个官方插件都要在列且带已发布 zip 资产）──
Write-Host "`n==== 商城核对 ====" -ForegroundColor Cyan
# PS 5.1 的 Invoke-RestMethod 在服务端不声明 charset 时按 ISO-8859-1 解码，中文插件名
# 会显示成乱码；这里显式取字节按 UTF-8 解码，保证日志可读（同 publish-plugin.ps1 的编码坑）。
try {
    $marketRaw = (Invoke-WebRequest -Method Get -Uri "$PlatformUrl/easyshare/plugins" -UseBasicParsing).RawContentStream.ToArray()
    $marketInfo = [System.Text.Encoding]::UTF8.GetString($marketRaw) | ConvertFrom-Json
} catch {
    Write-Host "  [失败] 商城清单拉取失败：$($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
$market = @($marketInfo.data)
foreach ($p in $official) {
    $entry = $market | Where-Object { $_.id -eq $p.Id }
    if (-not $entry) {
        Write-Host "  [缺失] $($p.Id) 不在商城清单" -ForegroundColor Red
        $failed += $p.Id
    } elseif (-not $entry.asset) {
        Write-Host "  [缺失] $($p.Id) 在架但无已发布 zip 资产" -ForegroundColor Red
        $failed += $p.Id
    } else {
        Write-Host "  [OK] $($entry.name) v$($entry.version)（$($entry.asset.sizeBytes) 字节）" -ForegroundColor Green
    }
}

if ($failed) {
    Write-Host "`n上架未完成：$((($failed | Select-Object -Unique) -join ', '))" -ForegroundColor Red
    Write-Host "重试（单插件）：powershell -ExecutionPolicy Bypass -File scripts/publish-plugin.ps1 -PluginDir plugins\<id> -PlatformUrl $PlatformUrl"
    exit 1
}
Write-Host "`n全部官方插件已在架（$($official.Count) 个）：$((($official | ForEach-Object { $_.Id }) -join ', '))"
# 显式收尾：被 ship-control-plane.ps1 以 & 调用时靠 $LASTEXITCODE 判定成败，
# 脚本正常结束时不写 exit 会留下上一条原生命令的旧值（误报成败）。
exit 0
