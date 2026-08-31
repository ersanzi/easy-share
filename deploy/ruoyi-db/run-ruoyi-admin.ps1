# 启动 EasyShare 控制面（RuoYi-Vue-Plus 6.0）本地开发实例。
# 前置：deploy/ruoyi-db 的 PG+Redis 已 docker compose up -d，platform 已 ./mvnw package。
# dev 运行参数说明见 deploy/ruoyi-db/README.md。

$ErrorActionPreference = 'Stop'

# 仓库根（本脚本位于 deploy/ruoyi-db/）
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$jar = Join-Path $repoRoot 'platform\ruoyi-admin\target\ruoyi-admin.jar'

if (-not (Test-Path $jar)) {
    Write-Error "未找到 $jar —— 请先在 platform/ 下执行 ./mvnw clean package -DskipTests"
}

# JDK 21（RuoYi 6.0 要求）
if (-not $env:JAVA_HOME) {
    $env:JAVA_HOME = 'C:\Program Files\Microsoft\jdk-21.0.8.9-hotspot'
}
$java = Join-Path $env:JAVA_HOME 'bin\java.exe'

# RustFS 凭据注入：真值只在 deploy/rustfs/.env（已 gitignore），
# 控制面读环境变量，绝不写进客户端二进制（ADR-0007 不变量 1）。
$envFile = Join-Path $repoRoot 'deploy\rustfs\.env'
if (-not (Test-Path $envFile)) {
    Write-Error "未找到 $envFile —— 请先 cp deploy/rustfs/.env.example deploy/rustfs/.env 并填入 RustFS 凭据"
}
Get-Content $envFile | ForEach-Object {
    if ($_ -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$') {
        $value = $Matches[2].Trim().Trim('"').Trim("'")
        [Environment]::SetEnvironmentVariable($Matches[1], $value, 'Process')
    }
}

# 存储授权模块配置（纳管在仓内，密钥用 ${ENV} 占位）
$driveConfig = (Join-Path $PSScriptRoot 'easyshare-drive.yml').Replace('\', '/')

Write-Host "启动 RuoYi 控制面：http://localhost:8090  (Ctrl+C 停止)"
& $java -jar $jar `
    --server.port=8090 `
    --spring.data.redis.port=6380 `
    --spring.data.redis.password=ruoyi123 `
    --captcha.enable=false `
    --api-decrypt.enabled=false `
    "--spring.config.additional-location=file:///$driveConfig"
