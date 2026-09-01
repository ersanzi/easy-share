# EasyShare 控制面投递脚本（开发机 → Linux 服务器）。
# 控制面 fat jar 只能在开发机构建（platform/ RuoYi 源码树 gitignore），本脚本负责：
#   构建 → scp jar + 建表 SQL → 远程换包重启 → 探活。快速迭代时约 1 分钟完成。
#
# 前置（一次性）：
#   1) 服务器已跑过 deploy/server-linux/deploy.sh（compose + rustfs.env 就位）；
#   2) 开发机能免密 ssh 到服务器：ssh-keygen -t ed25519 后把公钥追加进服务器 ~/.ssh/authorized_keys
#      （Windows: type $env:USERPROFILE\.ssh\id_ed25519.pub | ssh user@host "cat >> ~/.ssh/authorized_keys"）
#
# 用法（仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File scripts\ship-control-plane.ps1 -SshTarget root@192.168.1.10
#   快速重发（不重新构建）：加 -SkipBuild
param(
    [Parameter(Mandatory = $true)][string]$SshTarget,   # user@host
    [string]$ServerDir = "/opt/easyshare",
    [string]$JavaHome = "D:\Develop\java21",            # RuoYi 6.0 需要 JDK 21
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$jarLocal = Join-Path $repoRoot "platform\ruoyi-admin\target\ruoyi-admin.jar"
$cpRemote = "$ServerDir/control-plane"

function Write-Step([string]$m) { Write-Host "`n==> $m" -ForegroundColor Cyan }
function Write-Ok([string]$m)   { Write-Host "    [OK] $m" -ForegroundColor Green }
function Write-Fail([string]$m) { Write-Host "    [失败] $m" -ForegroundColor Red }

# ── 0) 远端前置 ──
Write-Step "检查服务器部署状态（$SshTarget）"
$composeReady = ssh $SshTarget "test -f $ServerDir/compose.yaml && echo yes || echo no"
if ($composeReady -ne "yes") { Write-Fail "服务器上没有 $ServerDir/compose.yaml —— 先跑 deploy/server-linux/deploy.sh"; exit 1 }
ssh $SshTarget "mkdir -p $cpRemote/sql" | Out-Null
Write-Ok "compose 与 control-plane 目录就绪"

# ── 1) 构建 fat jar ──
if (-not $SkipBuild) {
    Write-Step "构建控制面 fat jar（JDK 21）"
    $env:JAVA_HOME = $JavaHome
    Push-Location (Join-Path $repoRoot "platform")
    try {
        # 优先 mvnw.cmd（Windows）；平台差异由 Maven wrapper 吸收。snailai 可选模块的
        # milvus/es 依赖会下载损坏，-pl 聚焦 ruoyi-admin 也会牵连父 POM，故仍全量构建并忽略其失败模块
        & .\mvnw.cmd clean package -DskipTests
        if ($LASTEXITCODE -ne 0) { Write-Fail "Maven 构建失败"; exit 1 }
    } finally { Pop-Location }
} else {
    Write-Step "跳过构建（-SkipBuild）"
}
if (-not (Test-Path $jarLocal)) { Write-Fail "找不到 $jarLocal（先去掉 -SkipBuild 完整跑一次）"; exit 1 }
Write-Ok "jar 就绪：$([Math]::Round((Get-Item $jarLocal).Length / 1MB, 1)) MB"

# ── 2) 传输 SQL 与 jar ──
Write-Step "传输建表 SQL 与 jar"
$sqlSources = @()
$pgSqlDir = Join-Path $repoRoot "platform\script\sql\postgres"
foreach ($f in @("postgres_ry_vue.sql", "postgres_ry_workflow.sql", "postgres_ry_job.sql", "postgres_ry_ai.sql")) {
    $p = Join-Path $pgSqlDir $f
    if (-not (Test-Path $p)) { Write-Fail "缺 RuoYi 基础 SQL：$p（platform/ 工程不完整）"; exit 1 }
    $sqlSources += $p
}
foreach ($f in @("easyshare-space.sql", "easyshare-app-release.sql", "easyshare-plugin.sql")) {
    $p = Join-Path $repoRoot "deploy\ruoyi-db\$f"
    if (Test-Path $p) { $sqlSources += $p } else { Write-Host "    跳过不存在的 $f" -ForegroundColor DarkGray }
}
foreach ($p in $sqlSources) { scp -q $p "${SshTarget}:$cpRemote/sql/" }
scp -q $jarLocal "${SshTarget}:$cpRemote/ruoyi-admin.jar.new"
if ($LASTEXITCODE -ne 0) { Write-Fail "scp 传输失败（检查免密 ssh 与磁盘空间）"; exit 1 }
Write-Ok "已传输 $($sqlSources.Count + 1) 个文件"

# ── 3) 建表（幂等：库里有 sys_user 就跳过基础 SQL） ──
Write-Step "检查并初始化数据库表"
$hasSchema = ssh $SshTarget "docker exec easyshare-ruoyi-pg psql -U ruoyi -d ryvue -tAc 'select 1 from sys_user limit 1' >/dev/null 2>&1 && echo yes || echo no"
if ($hasSchema -eq "yes") {
    Write-Ok "基础表已存在，跳过建表（增量 SQL 请手动：docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue < 文件）"
} else {
    foreach ($f in @("postgres_ry_vue.sql", "postgres_ry_workflow.sql", "postgres_ry_job.sql", "postgres_ry_ai.sql",
                     "easyshare-space.sql", "easyshare-app-release.sql", "easyshare-plugin.sql")) {
        $remoteSql = "$cpRemote/sql/$f"
        ssh $SshTarget "test -f $remoteSql && docker exec -i easyshare-ruoyi-pg psql -U ruoyi -d ryvue -v ON_ERROR_STOP=1 < $remoteSql || echo skip"
        if ($LASTEXITCODE -ne 0) { Write-Fail "灌入 $f 失败"; exit 1 }
        Write-Ok "$f"
    }
    Write-Ok "建表完成（默认账号 admin/admin123，上线后尽快在客户端管理页改密）"
}

# ── 4) 换包重启 ──
Write-Step "替换 jar 并重启控制面容器"
ssh $SshTarget "cd $ServerDir && if [ -f $cpRemote/ruoyi-admin.jar ]; then cp $cpRemote/ruoyi-admin.jar $cpRemote/ruoyi-admin.jar.bak; fi && mv $cpRemote/ruoyi-admin.jar.new $cpRemote/ruoyi-admin.jar && docker compose up -d --no-deps ruoyi"
if ($LASTEXITCODE -ne 0) { Write-Fail "重启失败（看日志：ssh $SshTarget docker logs easyshare-ruoyi --tail 50）"; exit 1 }
Write-Ok "容器已重启（旧包备份在 ruoyi-admin.jar.bak）"

# ── 5) 探活 ──
Write-Step "探活 8090（Spring Boot 启动约 30-60 秒）"
$healthy = $false
for ($i = 0; $i -lt 45; $i++) {
    Start-Sleep -Seconds 2
    $code = ssh $SshTarget "curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:8090/auth/tenant/list 2>/dev/null || echo 000"
    if ($code -eq "200") { $healthy = $true; break }
    if ($code -eq "404") { Write-Warning "8090 已应答但 /auth/tenant/list 404（可能路由不同），改按端口判断"; $healthy = $true; break }
}
if (-not $healthy) {
    Write-Fail "8090 未在 90 秒内就绪，最近日志："
    ssh $SshTarget "docker logs easyshare-ruoyi --tail 30"
    exit 1
}
Write-Ok "控制面已上线：http://<服务器IP>:8090"
Write-Host ""
Write-Host "==== 投递完成 ====" -ForegroundColor Green
Write-Host " 回滚：ssh $SshTarget 然后 cp $cpRemote/ruoyi-admin.jar.bak $cpRemote/ruoyi-admin.jar && cd $ServerDir && docker compose up -d --no-deps ruoyi"
Write-Host " 日志：ssh $SshTarget docker logs -f easyshare-ruoyi"
