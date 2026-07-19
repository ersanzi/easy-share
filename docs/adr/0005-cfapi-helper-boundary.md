# ADR-0005：CfAPI Helper 与 Go Core 边界

- 状态：提议
- 日期：2026-07-19
- 决策者：EasyShare maintainers
- 关联：[`../product-vision.md`](../product-vision.md)、[`../architecture.md`](../architecture.md)

## 背景

成熟 Windows 云盘入口需要 Cloud Files API（CfAPI）、Sync Root、占位文件、水合/脱水和必要的资源管理器命令。CfAPI 与 Shell 集成属于 Win32/COM 生命周期，任何加载到 `explorer.exe` 或处于系统回调关键路径的代码都必须快速、稳定且不依赖网络。

EasyShare 当前同步和网络能力位于 Go Core，UI 位于 Wails。让 Shell 代码直接操作云端数据库，或把整个 Go/Wails 运行时加载进 Explorer，会扩大崩溃影响和协议耦合。

## 决策

CfAPI 阶段采用三类职责：

1. **Go Core**：文件身份、同步决策、任务、缓存、本地 SQLite、云端和局域网访问；
2. **独立 Windows Helper，首选 C++/Win32**：注册和连接 Sync Root，处理 CfAPI 回调，将水合、取消和状态请求转交 Core；
3. **最小 Shell 集成**：只提供必要的上下文菜单或状态入口，不执行网络请求，不直接访问 SQLite。

现有 Wails UI 到 Core 的 loopback HTTP API继续保留。Helper 到 Core 优先使用带当前用户 ACL 的 Windows Named Pipe 和版本化消息；原型阶段可比较 JSON 与 Protobuf，但协议必须具备：

- handshake 和协议版本；
- 请求 ID 与幂等 operation ID；
- 明确超时和取消；
- Core 身份校验；
- 最大消息尺寸；
- 向后兼容窗口和能力协商；
- 不传递长期云端凭据。

Helper 不直接写本地数据库。水合数据由 Core 写入受控 staging/cache，Helper 只通过约定的句柄、文件或流完成 CfAPI 操作；最终方式由 Spike 的性能和取消语义决定。

### 失败边界

- Core 不可用时，Helper 快速返回可重试错误，不在 Explorer 回调中自动启动复杂服务并无限等待；
- 网络不可用时，由 Core 根据本地缓存和策略决定结果；
- Helper 崩溃不得破坏已提交缓存或云端状态；
- Explorer 重启后可以重新连接 Sync Root；
- 升级和卸载必须先停止新水合、处理在途回调，再注销或保留 Sync Root；
- 任何 Shell in-process 代码都不得加载 Wails、Vue、网络 SDK 或完整 Go Core。

## 不变量

1. 云端凭据只由 Core 的安全存储和网络层使用。
2. Core 是同步状态和本地 SQLite 的唯一所有者。
3. Shell/CfAPI 回调路径有超时、取消和进程失联处理。
4. 资源管理器线程不直接等待公网请求。
5. IPC 消息按版本解析，未知字段可安全忽略，未知必需能力明确拒绝。
6. 文件采用前仍由 Core 根据 file ID、version ID 和内容哈希验证。
7. 安装、升级、回滚和卸载属于 CfAPI 功能的交付范围，不是事后补充。

## 备选方案

### Go 直接实现全部 CfAPI 和 COM 集成

不作为首选。可以用于小型可行性实验，但 Win32 回调、COM 生命周期、线程和 Explorer 故障隔离风险更高。

### C#/.NET Helper

保留为候选。开发效率较高，但仍需验证运行时部署、原生回调、启动成本、打包和 Shell 进程内边界。若 Spike 明显优于 C++，应通过新 ADR 修改决定。

### 将 CfAPI 逻辑加入 Wails 主进程

拒绝。关闭窗口不应停止同步入口，WebView 生命周期也不适合作为系统文件提供者。

### Helper 与 Core 共享 SQLite

拒绝。会把数据库锁和迁移带入系统回调路径，并破坏单写所有权。

### 所有 IPC 都继续使用 loopback HTTP

UI 场景保留，Helper 场景不作为首选。Named Pipe 更容易施加当前用户 ACL 和进程边界；最终仍需通过 Spike 比较调试性、吞吐和部署复杂度。

## 影响

正面影响：

- Windows 平台细节与同步业务解耦；
- Explorer 故障影响更小；
- Core 可独立测试，未来其他平台也能复用同步逻辑；
- IPC 可明确支持版本兼容。

成本与风险：

- 增加 C++ 工具链、第三个产物和跨语言协议；
- 安装器、签名、升级和崩溃报告更复杂；
- CfAPI 回调取消、文件占用和占位状态需要大量真实 Windows 测试。

## 开放问题

- 最低 Windows 版本、NTFS 要求和 ARM64 范围；
- Helper 是常驻独立进程、按需进程还是与 Sync Provider 托管模型结合；
- IPC 采用 JSON 或 Protobuf；
- Core 到 CfAPI 的数据交付采用文件句柄、临时文件还是分块流；
- NSIS 是否足够，何时切换 WiX/MSI；
- 上下文菜单采用何种 Windows 扩展模型；
- Core 版本不匹配时的升级和降级行为。

## 验证

正式实现前必须完成独立 CfAPI Spike，至少验证：

1. 当前用户注册、连接和注销 Sync Root；
2. 创建目录和文件占位符；
3. 双击、预览和应用打开触发水合；
4. 水合进度、取消、失败和重试；
5. 释放空间及“始终保留在此设备”；
6. 断网访问已缓存内容；
7. Core、Helper、Explorer 分别崩溃和重启；
8. Office/编辑器占用、重命名、删除和并发访问；
9. 十万级占位符的枚举和 Explorer 响应时间；
10. 安装、覆盖升级、回滚和卸载残留清理；
11. IPC 版本不匹配和恶意本机客户端；
12. 签名后的发布产物在支持的 Windows 版本上工作。

Spike 通过只代表平台可行，不自动承诺阶段 1 必须交付 CfAPI。
