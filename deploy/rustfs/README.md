# RustFS 本地开发环境

此目录只用于开发和一致性测试，不是生产部署模板。生产启用要求见 [`../../docs/adr/0006-rustfs-self-hosted-object-storage.md`](../../docs/adr/0006-rustfs-self-hosted-object-storage.md)。

## 固定版本

- 镜像：`rustfs/rustfs:1.0.0-beta.10`
- OCI index digest：`sha256:60f4f2f41ce95216f8cac676e69f9d90c0bfec458a3bc7fd7fb9b7c2452ac57a`

Compose 同时固定 tag 与 digest：tag 便于审计版本，digest 防止同名 tag 漂移。升级时应重新检查 RustFS release notes、S3 compatibility matrix，并在独立迭代中更新两者。

## 启动

```powershell
Set-Location deploy/rustfs
Copy-Item .env.example .env
# 编辑 .env，替换开发凭证；不得复用生产凭证

docker compose config
docker compose up -d
docker compose ps
```

服务仅绑定本机：

- S3 API：`http://127.0.0.1:9000`
- Console：`http://127.0.0.1:9001`

在 Console 中创建测试 bucket（例如 `easyshare-test`）。Compose 使用 named volumes 保存数据和日志，`docker compose down` 不会删除它们。

## 运行 EasyShare 一致性测试

以下命令在仓库根目录执行：

```powershell
$env:EASYSHARE_RUSTFS_INTEGRATION = '1'
$env:EASYSHARE_RUSTFS_ENDPOINT = 'http://127.0.0.1:9000'
$env:EASYSHARE_RUSTFS_ACCESS_KEY = '<与 .env 一致>'
$env:EASYSHARE_RUSTFS_SECRET_KEY = '<与 .env 一致>'
$env:EASYSHARE_RUSTFS_BUCKET = 'easyshare-test'

go test ./internal/cloud/objectstore/s3store -run '^TestRustFSIntegration$' -count=1 -v

Remove-Item Env:EASYSHARE_RUSTFS_INTEGRATION
Remove-Item Env:EASYSHARE_RUSTFS_ENDPOINT
Remove-Item Env:EASYSHARE_RUSTFS_ACCESS_KEY
Remove-Item Env:EASYSHARE_RUSTFS_SECRET_KEY
Remove-Item Env:EASYSHARE_RUSTFS_BUCKET
```

测试开关未设置时，集成测试会跳过，普通 `go test ./...` 不依赖 Docker。

同一组环境变量也用于 Python 文档管线的真实闭环测试：

```powershell
knowledge/.venv/Scripts/python.exe -m pytest knowledge/tests/integration -q -m integration
```

Python 测试会写入唯一的 `integration/python/rustfs-it-{uuid}/...` 源对象，并在结束时只删除该对象和对应的三个 `derived/{fileId}/v1/` 派生产物。测试不会自动创建 bucket；请提前在 Console 创建并确认 `HeadBucket` 可访问。

## Docker Desktop 后端排障

如果 Docker Desktop 进程已经存在，但 `docker version` 只有 Client，或返回 `/v1.24/info` Internal Server Error，先检查：

```powershell
wsl.exe -l -v
```

若 `docker-desktop` 为 `Stopped`，可显式唤起 WSL 后端：

```powershell
wsl.exe -d docker-desktop -e sh -lc 'echo ready'
docker version
```

必须等 `docker version` 同时显示 Client 和 Server 后再运行 `docker compose up -d`。不要以 Windows 侧进程存在、命令退出码偶发为 0，或空的 ServerVersion 判断 daemon 已就绪。

## 停止与清理

```powershell
docker compose down
```

只有确认不再需要所有本地测试对象时，才执行以下命令删除 named volumes：

```powershell
docker compose down --volumes
```

## 安全限制

- 当前配置是 loopback + HTTP，仅适用于本机开发；不要把端口绑定改成公网地址后继续使用 HTTP。
- `.env` 已在本目录忽略；日志、终端历史和测试失败信息中也不得输出 secret key。
- 生产必须使用 TLS、独立密钥管理、最小权限账号、备份、监控和恢复演练。
- Docker daemon 不可用时只能执行默认回归并看到集成测试跳过；只有显式运行真实测试成功后，才能描述为“RustFS 已通过集成测试”。
