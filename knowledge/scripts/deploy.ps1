# EasyShare 知识服务一键部署向导（公司服务器）
# 用法（交互式，缺什么问什么）：
#   powershell -ExecutionPolicy Bypass -File scripts\deploy.ps1
# 无人值守（全参数）示例：
#   powershell -ExecutionPolicy Bypass -File scripts\deploy.ps1 -AdminPassword 'xxx' `
#     -WatchDir 'D:\公司共享盘\知识库入库' -RustfsEndpoint http://127.0.0.1:9000 `
#     -RustfsAccessKey k -RustfsSecretKey s -LlmApiKey sk-... -NonInteractive
# 幂等：可重复运行；已存在的 .venv/.env/账号会询问（或按参数）保留或重建。
param(
    [int]$Port = 8000,
    [string]$AdminUsername = "admin",
    [string]$AdminPassword = "",
    [string]$ColleagueUsernames = "",
    [string]$WatchDir = "",
    [string]$RustfsEndpoint = "",
    [string]$RustfsAccessKey = "",
    [string]$RustfsSecretKey = "",
    [string]$RustfsBucket = "easyshare",
    [string]$LlmBaseUrl = "",
    [string]$LlmApiKey = "",
    [string]$LlmModel = "",
    [string]$EmbeddingBaseUrl = "",
    [string]$EmbeddingApiKey = "",
    [string]$EmbeddingModel = "",
    [switch]$NoMirror,
    [switch]$SkipAutostart,
    [switch]$SkipFirewall,
    [switch]$NonInteractive
)

$ErrorActionPreference = "Stop"
$KnowledgeDir = Split-Path -Parent $PSScriptRoot
Set-Location $KnowledgeDir

# ---------- 输出与交互辅助 ----------

function Write-Step([string]$Message) { Write-Host "`n==> $Message" -ForegroundColor Cyan }
function Write-Ok([string]$Message)   { Write-Host "    [OK] $Message" -ForegroundColor Green }
function Write-Fail([string]$Message) { Write-Host "    [失败] $Message" -ForegroundColor Red }

function Read-Input {
    # 交互式取值：已有值直接用；非交互模式返回空并让调用方决定
    param([string]$Prompt, [string]$Default = "")
    if ($script:NonInteractive) { return $Default }
    $value = Read-Host "$Prompt（回车用默认：$Default）"
    if ([string]::IsNullOrWhiteSpace($value)) { $Default } else { $value }
}

function Read-YesNo {
    param([string]$Prompt, [bool]$Default = $true)
    if ($script:NonInteractive) { return $Default }
    $hint = if ($Default) { "Y/n" } else { "y/N" }
    $value = Read-Host "$Prompt（$hint）"
    if ([string]::IsNullOrWhiteSpace($value)) { return $Default }
    return $value.Trim().ToLowerInvariant() -in @("y", "yes")
}

function New-RandomPassword([int]$Length = 10) {
    $chars = (48..57) + (65..90) + (97..122) | ForEach-Object { [char]$_ }
    return -join ($chars | Get-Random -Count $Length)
}

function Get-LanIp {
    # 排除回环/链路本地/WSL/Hyper-V 虚拟网卡（vEthernet*），否则打印出同事访问不了的地址
    $candidate = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object {
            $_.IPAddress -notlike "127.*" -and $_.IPAddress -notlike "169.254.*" -and
            $_.PrefixOrigin -ne "WellKnown" -and
            $_.InterfaceAlias -notlike "vEthernet*" -and $_.InterfaceAlias -notlike "*WSL*"
        } |
        Select-Object -First 1
    if ($candidate) { $candidate.IPAddress } else { "<本机IP>" }
}

function Test-PortListening([int]$Number) {
    $client = New-Object Net.Sockets.TcpClient
    try {
        $task = $client.ConnectAsync("127.0.0.1", $Number)
        if ($task.Wait(500)) { return $client.Connected }
        return $false
    } catch { return $false } finally { $client.Dispose() }
}

function Test-IsAdmin {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# ---------- 第 0 步：环境检查 ----------

function Get-PythonCommand {
    # 返回 @("python") 或 @("py","-3")；找不到返回 $null
    try {
        $output = & python --version 2>&1
        if ($LASTEXITCODE -eq 0 -and ("$output" -match "Python 3\.(\d+)") -and [int]$Matches[1] -ge 11) {
            return ,@("python")
        }
    } catch { }
    try {
        $output = & py -3 --version 2>&1
        if ($LASTEXITCODE -eq 0 -and ("$output" -match "Python 3\.(\d+)") -and [int]$Matches[1] -ge 11) {
            return ,@("py", "-3")
        }
    } catch { }
    return $null
}

# ---------- 第 1 步：RustFS 归属 ----------

function Resolve-Rustfs {
    # 返回/写入 $script:Rustfs* 三个变量
    Write-Step "配置对象存储 RustFS"

    if ($script:RustfsEndpoint) {
        Write-Ok "使用参数指定的 RustFS：$($script:RustfsEndpoint)"
        return
    }

    if (Test-PortListening 9000) {
        Write-Host "    检测到本机 9000 端口已有服务在监听（可能已部署 RustFS）。"
        $choice = Read-Input "选择：[1] 复用已有（输入地址与凭据） [2] 本机 Docker 新起一个" "1"
    } else {
        $choice = Read-Input "本机 9000 未监听。选择：[1] 使用别处已部署的 RustFS（输入地址） [2] 本机 Docker 新起一个 [3] 暂不配置（不推荐）" "2"
    }

    switch ($choice) {
        "1" {
            $script:RustfsEndpoint = Read-Input "RustFS 地址" "http://127.0.0.1:9000"
            $script:RustfsAccessKey = Read-Input "AccessKey"
            $script:RustfsSecretKey = Read-Input "SecretKey"
        }
        "2" {
            if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
                Write-Fail "未找到 docker。请先安装 Docker Desktop（https://www.docker.com/products/docker-desktop/）后重跑本脚本，或改选复用已有 RustFS。"
                if ($script:NonInteractive) { exit 1 } else { return }
            }
            if (-not $script:RustfsAccessKey) { $script:RustfsAccessKey = "easyshare-" + (New-RandomPassword 8).ToLowerInvariant() }
            if (-not $script:RustfsSecretKey) { $script:RustfsSecretKey = New-RandomPassword 24 }
            $script:RustfsEndpoint = "http://127.0.0.1:9000"
            # 凭据先写临时 env 文件供 compose 注入（.env 稍后统一生成，内容一致）
            $composeEnv = "RUSTFS_ACCESS_KEY=$($script:RustfsAccessKey)`nRUSTFS_SECRET_KEY=$($script:RustfsSecretKey)"
            [IO.File]::WriteAllText((Join-Path $KnowledgeDir ".compose-env"), $composeEnv)
            Write-Host "    正在启动 RustFS 容器（镜像首次拉取可能需要几分钟）..."
            docker compose --env-file ".compose-env" -f docker-compose.rustfs.yml up -d
            if ($LASTEXITCODE -ne 0) { Write-Fail "RustFS 容器启动失败，请检查 Docker 是否运行"; exit 1 }
            # 等待 9000 就绪
            $ready = $false
            for ($i = 0; $i -lt 60; $i++) {
                if (Test-PortListening 9000) { $ready = $true; break }
                Start-Sleep -Seconds 2
            }
            if ($ready) { Write-Ok "RustFS 已启动（数据目录 knowledge\rustfs-data\，控制台 http://<本机IP>:9001）" }
            else { Write-Fail "RustFS 端口未就绪，请查看：docker logs easyshare-rustfs" }
        }
        default {
            Write-Warning "跳过 RustFS 配置：文档入库/产物存储将不可用（问答无法工作），请部署后手改 .env 再重启。"
            $script:RustfsEndpoint = ""
        }
    }
}

function Initialize-Bucket {
    if (-not $script:RustfsEndpoint -or -not $script:RustfsAccessKey) { return }
    Write-Step "初始化对象存储桶"
    $code = @"
import sys
import boto3
from botocore.config import Config

client = boto3.client(
    "s3",
    endpoint_url=sys.argv[1],
    aws_access_key_id=sys.argv[2],
    aws_secret_access_key=sys.argv[3],
    config=Config(connect_timeout=5, read_timeout=10, retries={"max_attempts": 1}),
    region_name="us-east-1",
)
bucket = sys.argv[4]
try:
    client.head_bucket(Bucket=bucket)
    print("exists")
except Exception:
    client.create_bucket(Bucket=bucket)
    print("created")
"@
    $tmp = Join-Path $env:TEMP "es-deploy-bucket.py"
    [IO.File]::WriteAllText($tmp, $code)
    try {
        $result = & .venv\Scripts\python.exe $tmp $script:RustfsEndpoint $script:RustfsAccessKey $script:RustfsSecretKey $script:RustfsBucket 2>&1
        if ($LASTEXITCODE -eq 0) { Write-Ok "桶 $($script:RustfsBucket)：$result" }
        else { Write-Fail "桶初始化失败：$result（检查 RustFS 地址与凭据）" }
    } finally { Remove-Item $tmp -ErrorAction SilentlyContinue }
}

# ---------- 第 2 步：Python 依赖 ----------

function Install-Deps {
    Write-Step "准备 Python 虚拟环境与依赖"
    $python = Get-PythonCommand
    if (-not $python) {
        Write-Fail "未找到 Python 3.11+。请安装后重跑本脚本："
        Write-Host "    winget install --id Python.Python.3.12 -e --scope user"
        Write-Host "    或下载：https://www.python.org/downloads/ （勾选 Add to PATH）"
        exit 1
    }
    $pyExe, $pyArg = $python

    if (Test-Path ".venv\Scripts\python.exe") {
        Write-Ok "虚拟环境已存在（跳过创建）"
    } else {
        & $pyExe ($pyArg | Where-Object { $_ }) -m venv .venv
        if ($LASTEXITCODE -ne 0) { Write-Fail "创建 .venv 失败"; exit 1 }
        Write-Ok "虚拟环境已创建"
    }

    $mirrorArgs = @()
    if (-not $NoMirror) { $mirrorArgs = @("-i", "https://pypi.tuna.tsinghua.edu.cn/simple") }
    Write-Host "    安装依赖（公司网络默认走清华镜像，加 -NoMirror 关闭）..."
    & .venv\Scripts\python.exe -m pip install --quiet --disable-pip-version-check $mirrorArgs -r requirements.txt
    if ($LASTEXITCODE -ne 0) { Write-Fail "依赖安装失败（网络？）；可重跑或手动：.venv\Scripts\pip install -r requirements.txt"; exit 1 }
    Write-Ok "依赖安装完成"
}

# ---------- 第 3 步：生成 .env ----------

function Write-KnowledgeEnv {
    Write-Step "生成配置 .env"
    if (Test-Path ".env") {
        $keep = Read-YesNo "已存在 .env，保留现有配置？（选否将按本次输入重写）" $true
        if ($keep) { Write-Ok "保留现有 .env"; return }
    }

    if (-not $script:LlmApiKey) {
        $script:LlmApiKey = Read-Input "LLM API Key（OpenAI 兼容；留空=纯检索模式，只返回片段不生成回答）" ""
        if ($script:LlmApiKey) {
            if (-not $script:LlmBaseUrl) { $script:LlmBaseUrl = Read-Input "LLM Base URL" "" }
            if (-not $script:LlmModel)   { $script:LlmModel = Read-Input "LLM 模型名" "" }
        }
    }
    if (-not $script:EmbeddingApiKey) {
        $script:EmbeddingApiKey = Read-Input "Embedding API Key（留空=无语义检索，仅关键词；强烈建议配置）" ""
        if ($script:EmbeddingApiKey) {
            if (-not $script:EmbeddingBaseUrl) { $script:EmbeddingBaseUrl = Read-Input "Embedding Base URL" "" }
            if (-not $script:EmbeddingModel)   { $script:EmbeddingModel = Read-Input "Embedding 模型名" "" }
        }
    }
    if (-not $script:WatchDir) {
        $script:WatchDir = Read-Input "知识库入库目录（共享盘路径；同事往这里放文件自动入库）" (Join-Path $KnowledgeDir "watch-inbox")
    }
    $watchAbsolute = [IO.Path]::GetFullPath($script:WatchDir)
    if (-not (Test-Path $watchAbsolute)) {
        New-Item -ItemType Directory -Path $watchAbsolute -Force | Out-Null
        Write-Ok "已创建入库目录：$watchAbsolute"
    }

    $lines = @(
        "# 由 deploy.ps1 生成于 $(Get-Date -Format 'yyyy-MM-dd HH:mm')",
        "HOST=0.0.0.0",
        "PORT=$Port",
        "LOCAL_LAB_ENABLED=true",
        "",
        "RUSTFS_ENDPOINT=$($script:RustfsEndpoint)",
        "RUSTFS_ACCESS_KEY=$($script:RustfsAccessKey)",
        "RUSTFS_SECRET_KEY=$($script:RustfsSecretKey)",
        "RUSTFS_BUCKET=$($script:RustfsBucket)",
        "",
        "LLM_BASE_URL=$($script:LlmBaseUrl)",
        "LLM_API_KEY=$($script:LlmApiKey)",
        "LLM_MODEL=$($script:LlmModel)",
        "",
        "EMBEDDING_BASE_URL=$($script:EmbeddingBaseUrl)",
        "EMBEDDING_API_KEY=$($script:EmbeddingApiKey)",
        "EMBEDDING_MODEL=$($script:EmbeddingModel)",
        "EMBEDDING_DIM=1024",
        "",
        "OCR_ENABLED=true",
        "OCR_LANG=ch",
        "OCR_MIN_TEXT_CHARS=20",
        "",
        "AUTH_ENABLED=true",
        "AUTH_DB_PATH=./data/auth.db",
        "AUTH_TOKEN_EXPIRY_HOURS=168",
        "",
        "WATCH_DIRS=$watchAbsolute",
        "WATCH_INTERVAL_SECONDS=30",
        "WATCH_STABLE_SECONDS=5"
    )
    [IO.File]::WriteAllLines((Join-Path $KnowledgeDir ".env"), $lines)
    Write-Ok ".env 已生成（AUTH 已开启；入库目录 $watchAbsolute）"
}

# ---------- 第 4 步：启动与探活 ----------

function Start-AndVerify {
    Write-Step "启动服务并验证"
    if (Test-PortListening $Port) {
        # 端口被占：先确认监听者是不是 EasyShare 自己，别拿别人的服务继续部署
        $isOurs = $false
        try {
            $health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/health" -TimeoutSec 3
            $isOurs = ($health.PSObject.Properties.Name -contains "embedder")
        } catch { }
        if (-not $isOurs) {
            $owner = (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1).OwningProcess
            $name = (Get-Process -Id $owner -ErrorAction SilentlyContinue).ProcessName
            Write-Fail "端口 $Port 被其他程序占用（PID $owner / $name），不是 EasyShare。请释放该端口，或用 -Port 参数换端口重跑。"
            exit 1
        }
        $restart = Read-YesNo "端口 $Port 已有 EasyShare 服务在监听。停掉并重启？（选否=复用现有实例）" $false
        if (-not $restart) { Write-Ok "复用现有服务"; return $null }
        $connections = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
        $connections | Select-Object -ExpandProperty OwningProcess -Unique | ForEach-Object {
            Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue
        }
        Start-Sleep -Seconds 2
    }
    New-Item -ItemType Directory -Path (Join-Path $KnowledgeDir "data") -Force | Out-Null
    $outLog = Join-Path $KnowledgeDir "data\server.out.log"
    $errLog = Join-Path $KnowledgeDir "data\server.err.log"
    $proc = Start-Process -FilePath (Join-Path $KnowledgeDir ".venv\Scripts\python.exe") `
        -ArgumentList "-m", "uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "$Port", "--workers", "1" `
        -WorkingDirectory $KnowledgeDir -RedirectStandardOutput $outLog -RedirectStandardError $errLog -PassThru -WindowStyle Hidden
    $healthy = $false
    for ($i = 0; $i -lt 40; $i++) {
        Start-Sleep -Seconds 1
        try {
            $health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/health" -TimeoutSec 3
            if ($health.status -eq "ok") { $healthy = $true; break }
        } catch { continue }
    }
    if ($healthy) {
        Write-Ok "服务已启动并通过健康检查（日志：data\server.err.log）"
        return $proc
    }
    Write-Fail "服务未在 40 秒内通过健康检查。最近日志："
    Get-Content $errLog -Tail 20 -ErrorAction SilentlyContinue | ForEach-Object { Write-Host "    $_" }
    exit 1
}

# ---------- 第 5 步：账号 ----------

function Initialize-Accounts {
    Write-Step "初始化账号"
    if (-not $script:AdminPassword) {
        $script:AdminPassword = Read-Input "管理员（$AdminUsername）口令"
        if (-not $script:AdminPassword -and $script:NonInteractive) { Write-Fail "非交互模式必须传 -AdminPassword"; exit 1 }
    }
    $base = "http://127.0.0.1:$Port"
    $script:AdminToken = $null
    try {
        Invoke-RestMethod -Method Post -Uri "$base/auth/bootstrap" -ContentType "application/json" `
            -Body (@{ username = $AdminUsername; password = $script:AdminPassword } | ConvertTo-Json) | Out-Null
        Write-Ok "管理员已创建：$AdminUsername"
    } catch {
        $status = $_.Exception.Response.StatusCode.value__
        if ($status -eq 409) {
            # 已有用户库：用输入口令登录验证（幂等重跑）
            try {
                $login = Invoke-RestMethod -Method Post -Uri "$base/auth/login" -ContentType "application/json" `
                    -Body (@{ username = $AdminUsername; password = $script:AdminPassword } | ConvertTo-Json)
                Write-Ok "管理员已存在，口令验证通过"
            } catch {
                Write-Fail "管理员已存在且口令不匹配。请用既有管理员口令重跑，或跳过账号步骤后手工处理。"
                return
            }
        } else { throw }
    }
    if (-not $script:AdminToken) {
        $login = Invoke-RestMethod -Method Post -Uri "$base/auth/login" -ContentType "application/json" `
            -Body (@{ username = $AdminUsername; password = $script:AdminPassword } | ConvertTo-Json)
        $script:AdminToken = $login.token
    }

    $script:NewAccounts = @()
    $names = ($script:ColleagueUsernames -split "[,，;；\s]+" | Where-Object { $_.Trim() })
    if (-not $names -and -not $script:NonInteractive) {
        $raw = Read-Input "批量创建同事账号（用户名逗号分隔；留空跳过，之后可加）" ""
        $names = ($raw -split "[,，;；\s]+" | Where-Object { $_.Trim() })
    }
    $headers = @{ Authorization = "Bearer $($script:AdminToken)" }
    foreach ($name in $names) {
        $password = New-RandomPassword
        try {
            Invoke-RestMethod -Method Post -Uri "$base/auth/users" -Headers $headers -ContentType "application/json" `
                -Body (@{ username = $name.Trim(); password = $password } | ConvertTo-Json) | Out-Null
            $script:NewAccounts += [pscustomobject]@{ Username = $name.Trim(); Password = $password }
            Write-Ok "同事账号已创建：$($name.Trim()) / 初始口令 $password"
        } catch {
            Write-Fail "创建账号 $($name.Trim()) 失败：$($_.Exception.Message)（可能已存在）"
        }
    }
}

# ---------- 第 6 步：防火墙 ----------

function Enable-Firewall {
    if ($SkipFirewall) { return }
    Write-Step "防火墙放行端口 $Port（同事访问必需）"
    $ruleName = "EasyShare Knowledge ($Port)"
    $existing = netsh advfirewall firewall show rule name="$ruleName" 2>$null
    if ($existing -match $ruleName) { Write-Ok "规则已存在，跳过"; return }
    if (-not (Test-IsAdmin)) {
        Write-Warning "当前不是管理员权限，无法自动放行。请用管理员 PowerShell 执行一次："
        Write-Host "    netsh advfirewall firewall add rule name=`"$ruleName`" dir=in action=allow protocol=TCP localport=$Port"
        return
    }
    netsh advfirewall firewall add rule name="$ruleName" dir=in action=allow protocol=TCP localport=$Port | Out-Null
    if ($LASTEXITCODE -eq 0) { Write-Ok "已放行" } else { Write-Fail "放行失败，请手动执行上面的 netsh 命令" }
}

# ---------- 第 7 步：自启 ----------

function Register-Autostart([object]$Proc) {
    if ($SkipAutostart) { return }
    Write-Step "注册开机自启"
    $want = Read-YesNo "注册开机自启（当前用户登录时自动启动服务）？" $true
    if (-not $want) {
        Write-Host "    跳过。手动启动：powershell -ExecutionPolicy Bypass -File scripts\start_server.ps1 -Port $Port"
        return
    }
    & (Join-Path $PSScriptRoot "install_autostart.ps1") -Port $Port
    # 计划任务起的服务与本脚本启动的测试进程二选一：停掉测试进程，交给计划任务
    if ($Proc -and -not $Proc.HasExited) {
        Stop-Process -Id $Proc.Id -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 2
        schtasks /Run /TN "EasyShare Knowledge" | Out-Null
        Start-Sleep -Seconds 3
        if (Test-PortListening $Port) { Write-Ok "已由计划任务接管运行" }
        else { Write-Warning "计划任务启动较慢，请稍后用 http://<本机IP>:$Port/health 确认" }
    }
}

# ---------- 第 8 步：使用指引 ----------

function Write-UsageGuide {
    Write-Step "生成「同事使用指引」"
    $lanIp = Get-LanIp
    $labUrl = "http://${lanIp}:$Port/lab"
    $watch = if ($script:WatchDir) { [IO.Path]::GetFullPath($script:WatchDir) } else { "（未配置，见 .env WATCH_DIRS）" }
    $lines = @(
        "# 公司知识库使用指引（发给同事的一页纸）",
        "",
        "## 怎么提问",
        "浏览器打开 $labUrl ，用你的账号登录，底部「知识问答」输入问题。",
        "回答带引用来源，点引用可以看原文出处。",
        "",
        "## 怎么贡献文件",
        "把文件放进共享目录：$watch",
        "支持 Word / PDF / Excel / PPT / TXT / Markdown / 图片，约 1 分钟后自动入库可被检索；",
        "文件更新后重新放入即可，答案会引用最新版本。",
        "",
        "## 注意",
        "- 从入库文件夹删除文件不会从知识库删除（需要管理员处理）；",
        "- 敏感文件先问 IT 再放（个人文档别人搜不到，共享文档所有人可搜）；",
        "- 装了 WPS 插件的电脑：在 WPS 文字里选中一段话，点「知识」页签的「查知识」。",
        "",
        "## 本次部署的账号（分发完建议删除本文件）",
        "管理员：$AdminUsername"
    )
    foreach ($account in $script:NewAccounts) {
        $lines += "同事：$($account.Username) / 初始口令 $($account.Password)"
    }
    $guidePath = Join-Path $KnowledgeDir "同事使用指引.txt"
    [IO.File]::WriteAllLines($guidePath, $lines)

    Write-Host ""
    Write-Host "==================== 部署完成 ====================" -ForegroundColor Green
    Write-Host " 同事访问地址：$labUrl"
    Write-Host " 入库目录　　：$watch"
    Write-Host " 管理台 　　：http://${lanIp}:9001 （RustFS 控制台，仅 Docker 新起时）"
    Write-Host " 指引文件　　：$guidePath"
    Write-Host " 质量驾驶舱：http://${lanIp}:$Port/lab/cockpit"
    Write-Host "===================================================" -ForegroundColor Green
}

# ---------- 主流程 ----------

Write-Host "EasyShare 知识服务一键部署（目录：$KnowledgeDir）" -ForegroundColor Cyan
Resolve-Rustfs
Install-Deps
Initialize-Bucket
Write-KnowledgeEnv
$proc = Start-AndVerify
Initialize-Accounts
Enable-Firewall
Register-Autostart $proc
Write-UsageGuide
