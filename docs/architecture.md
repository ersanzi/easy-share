# EasyShare 当前架构

> 更新基线：2026-07-19，Windows MVP。

## 1. 进程模型

EasyShare 由两个 Windows 进程组成：

```text
┌─────────────────────────────────────────────────────────────┐
│ easyshare.exe                                               │
│ Wails 宿主 + Vue 3 UI                                      │
│ app.go：Core 进程管理、HTTP 客户端、文件选择、桌面日志       │
└───────────────────────┬─────────────────────────────────────┘
                        │ 127.0.0.1:19079 / Bearer Token
┌───────────────────────▼─────────────────────────────────────┐
│ easyshare-core.exe                                          │
│ Core API、UDP 发现、TCP 传输、WebDAV、网络驱动器映射         │
└───────┬────────────────────┬───────────────────────┬────────┘
        │ UDP 9527           │ TCP 9528              │ 127.0.0.1:19080
        │ 设备发现           │ 文件传输              │ WebDAV Digest
        ▼                    ▼                       ▼
   局域网设备             对端 EasyShare       Windows WebClient / Z:
```

关闭桌面窗口默认只结束 `easyshare.exe`，Core 可以继续运行。界面中的“退出全部服务”才会关闭整个系统。

## 2. 主要代码入口

| 路径 | 职责 |
| --- | --- |
| `main.go` | 创建 Wails 窗口，注册 Startup/Shutdown 和 Go 绑定 |
| `app.go` | 前端桥接、Core 探测/启动、Core API 调用、桌面日志 |
| `cmd/core/main.go` | Core 组装、后台服务启动、信号和退出管理 |
| `internal/api` | Core HTTP API、状态、事件流和资源清理 |
| `internal/config` | 配置默认值、验证、原子保存 |
| `internal/desktop` | Core HTTP 客户端、健康校验和子进程启动 |
| `internal/discovery` | UDP 设备广播与在线列表 |
| `internal/transfer` | TCP 流式发送、接收和安全落盘 |
| `internal/drive` | WebDAV Digest 认证、服务和 Windows `net use` 映射 |
| `internal/logging` | 日志目录、追加写入和 5 MiB 轮转 |
| `internal/task` | 内存中的传输任务状态 |
| internal/cloud/objectstore | 尚未接入 Core 的对象存储边界、内存 fake 与 RustFS S3 adapter |
| `frontend/src/services/core.ts` | Wails 绑定的前端适配层 |
| `frontend/src/composables/useEasyShare.ts` | 前端状态、轮询、操作和退出状态机 |
| `frontend/src/components` | 状态、设备、传输、驱动器 UI |

`frontend/wailsjs` 是 Wails 自动生成代码，不应手工修改。

## 3. 默认端口和地址

| 功能 | 默认地址/端口 | 暴露范围 |
| --- | --- | --- |
| Core API | `127.0.0.1:19079` | 仅本机 |
| WebDAV | `127.0.0.1:19080` | 仅本机 |
| 设备发现 | UDP `9527` | 局域网 |
| 文件传输 | TCP `9528` | 局域网 |

端口由 `%LOCALAPPDATA%\EasyShare\config.json` 配置。Core API Host 必须是 loopback 地址。

## 4. Core API

`/health` 使用随机 nonce、Device ID 和 HMAC proof 来确认端口上的进程确实是当前配置对应的 EasyShare Core。其余 API 需要：

```http
Authorization: Bearer <apiToken>
```

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/health?nonce=...` | Core 身份和健康检查 |
| `GET` | `/api/status` | Core、发现、接收、WebDAV、映射状态 |
| `GET` | `/api/peers` | 附近设备 |
| `GET` | `/api/tasks` | 传输任务 |
| `GET` | `/api/events` | WebSocket 事件流 |
| `POST` | `/api/transfers` | 发起文件发送 |
| `POST` | `/api/transfers/{id}/accept` | 接受接收任务 |
| `POST` | `/api/transfers/{id}/reject` | 拒绝接收任务 |
| `POST` | `/api/drive/start` | 启动 WebDAV |
| `POST` | `/api/drive/stop` | 停止 WebDAV 和相关映射 |
| `POST` | `/api/drive/map` | 启动 WebDAV 后映射驱动器 |
| `POST` | `/api/drive/unmap` | 取消 EasyShare 自己的映射 |
| `POST` | `/api/shutdown` | 按顺序退出全部 Core 服务 |

## 5. 关键生命周期

### 启动

1. 桌面端打开 `desktop.log`。
2. 加载或首次生成 `config.json`。
3. 调用带 HMAC proof 的 `/health` 检查现有 Core。
4. 如果身份匹配则复用；否则启动同目录下的 `easyshare-core.exe`。
5. Core 再次检查兼容实例，避免手动或并发启动产生端口冲突。
6. 桌面端建立 Core API Client，前端读取首次快照；若尚未连接网络盘，则自动发起一次映射。自动尝试失败后只展示错误，不随状态轮询重复映射。

### 网络驱动器映射

1. Core 确保 WebDAV 已监听 `127.0.0.1:19080`。
2. WebDAV 使用 Digest Authentication。这样可以兼容 Windows 默认 `BasicAuthLevel=1`，无需修改机器级注册表。
3. HTTP URL 转换为 Windows WebClient UNC：

   ```text
   http://127.0.0.1:19080
   -> \\127.0.0.1@19080\DavWWWRoot
   ```

4. 查询目标盘符；若已指向当前 EasyShare WebDAV，则幂等复用并校准 Core 状态。
5. 已有非 EasyShare 映射时拒绝覆盖；空闲时执行 `net use <盘符> <UNC> <密码> /user:<用户名> /persistent:no`。
6. 取消映射前再次校验远端地址，只删除 EasyShare 拥有的映射。

### 退出全部服务

前端先停止轮询，然后调用 `ShutdownAll`。Core 资源清理顺序不可随意改变：

1. 取消网络驱动器映射
2. 停止 WebDAV
3. 取消 Core 后台 context（发现、接收等）
4. 关闭 Core HTTP Server 并退出进程
5. 前端进入“服务已安全退出”状态，不再请求 Core

直接关闭桌面窗口不会执行上述 Core 全退出流程。

## 6. 状态和持久化

- 配置持久化在 `%LOCALAPPDATA%\EasyShare\config.json`，使用临时文件加原子替换写入。
- peers 和 tasks 当前是内存状态，Core 退出后丢失。
- WebDAV 共享文件和已接收文件直接存储在配置指定的本地目录。
- DriveMapped 是 Core 的运行状态；桌面端下一次自动映射时可以认领远端地址完全匹配的 EasyShare 残留映射，但不会认领或清理无法确认归属的盘符。

## 7. 对象存储基础层（尚未接入运行路径）

- `internal/cloud/objectstore` 定义 Multipart、预签名上传/下载、Head 和删除所需的 provider-neutral 接口。
- `internal/cloud/objectstore/s3store` 使用 AWS SDK for Go v2，固定 path-style addressing，可连接 RustFS；默认拒绝 HTTP endpoint，只有显式开发配置允许明文 HTTP。
- `internal/cloud/objectstore/memory` 是状态机单元测试 fake，不是生产存储实现。
- 本层当前没有被 `cmd/core`、桌面端或 WebDAV 调用，也没有改变现有文件落盘路径。
- 固定版本的本地 RustFS 环境和 opt-in 一致性测试见 [`../deploy/rustfs/README.md`](../deploy/rustfs/README.md)。生产启用仍受 [ADR-0006](adr/0006-rustfs-self-hosted-object-storage.md) 门禁约束。

## 8. 安全边界

- Core API 和 WebDAV 只监听 loopback。
- Core API 使用随机 Token；健康检查同时验证 Device ID 和 HMAC proof。
- WebDAV 使用 Digest 而不是 HTTP Basic，避免修改 Windows 的 `BasicAuthLevel`。
- 映射命令的密码不会写入日志，但配置文件本身包含密钥。
- UDP 发现和 TCP 文件传输当前面向可信局域网，没有端到端加密或设备配对。



