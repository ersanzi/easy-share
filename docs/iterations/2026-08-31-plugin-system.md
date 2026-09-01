# EasyShare 桌面端插件系统 + 官方自营插件商城

## 用户问题

- 桌面端功能全部硬编码在主程序里（前端 7 个视图枚举 + 静态 Wails 绑定），加一个功能就要发一个新版本，无法按需扩展。
- 用户期望：插件可插拔（按需安装/卸载），由官方运营一个插件商城，客户按需安装；剪切板记录这类高频工具要**内置永不消失**；待办（生成周报）、剪切板记录是首批插件场景。

## 目标

- **插件运行时**：插件 = zip 包（manifest + HTML/JS/CSS），UI 跑在沙箱 iframe（opaque origin），经 postMessage RPC 调用宿主按 manifest `permissions` 授予的能力 API；零新增 Go/前端依赖。
- **内置插件**：剪切板记录——Win32 `AddClipboardFormatListener` 监听文本/图片/文件，本地持久化，插件 UI 随主程序内嵌分发，**不可卸载不可禁用、目录被删重启即恢复**；设置里仅提供「暂停记录」。
- **官方商城**（批次 2）：platform-drive 新增插件登记/发布/匿名清单/预签名下载接口（平移在线升级链路）；前端「插件中心」页（商城浏览/安装/更新 + 已装管理）；`publish-plugin.ps1` 一条命令发布。
- **首个商城插件**（批次 2）：待办/周报——待办 CRUD + 周聚合生成 markdown（复制 + 一键存个人云盘），验证完整分发链路。

## 非目标

- 第三方开发者上传/开放生态（商城官方自营，superadmin 发布即上架；重评估条件：客户提出自写插件需求时启动）。
- 插件包代码签名（发布通道可信：superadmin 凭据 + HTTPS + SHA256 + 预签名 URL；开放第三方时再加 Ed25519）。
- darwin 剪切板监听（本版留 stub，NSPasteboard changeCount 轮询列为批次 3）。
- 插件独立小窗口、开机自启记录、AI 周报、后台任务类能力 API（批次 3 候选）。
- SQLite（剪切板用 JSONL 追加 + 环形截断；SQLite 决策归 ADR-0004 独立迭代）。

## 设计决策

- **插件形态 = Web 插件包 + 沙箱 iframe**（已否决：Vue 动态组件——插件拿全量宿主上下文无权限边界；goja/quickjs 进 Core——重依赖且不解决 UI；独立进程插件——进程管理成本高）。iframe `sandbox="allow-scripts"`（无 `allow-same-origin`），postMessage RPC，宿主严格校验 `event.source`。
- **静态资源 serve = Wails AssetServer fallback Handler**：v2.13 `assetserver.Options.Handler` 官方语义「Assets FS 返回 os.ErrNotExist 的 GET 转发 Handler」（已读源码确认），`/plugins/{id}/...` 映射 `%LOCALAPPDATA%\EasyShare\plugins\{id}\`。iframe 相对路径同源加载，无跨域。备选（Core serve 19079）未启用。
- **插件 API 走动态通道避开 Wails 绑定级联**：唯一新增静态绑定 `PluginInvoke(pluginId, api, argsJSON) string`；Go 侧能力注册表按 manifest permissions 过滤。首发能力：`storage.*`（按 pluginId 隔离 KV）、`clipboard.history/delete/clear/write`、`clipboard.events`、`notification.show`、`drive.upload`。
- **剪切板监听放桌面端进程**（托盘常驻应用同生命周期；随桌面端退出而停——「开机自启记录」是独立特性）：message-only 窗口 + 消息循环复用 `internal/winui` 基建。记录来源进程名（GetForegroundWindow）；排除密码管理器标记格式（`CanIncludeInClipboardHistory` / `ExcludeClipboardContentFromMonitorProcessing` / 剪贴板查看器忽略格式——Windows 官方约定）；内容 hash 去重。
- **存储**：元数据追加 `clipboard-history.jsonl`（环形截断默认 1000 条，原子写惯例）；图片/文件快照存 `files/{sha256}.png`（默认 200MB LRU，设置可调）。
- **商城服务端 = 在线升级链路平移**：`es_plugin` / `es_plugin_release` / `es_plugin_asset` 三表（version 唯一、sha256/size 登记、status 两段式、对象键 `plugins/{id}/{version}/`，插件无 platform 维度）；匿名清单/URL + superadmin uploads/publish；`easyshare-drive.yml` excludes 白名单（Spring 列表整体替换语义，对照基线）。
- **安装协议**：下载 → SHA256 → 解压临时目录（zip-slip 校验）→ 解析 manifest → 原子 rename 进 `plugins/{id}/` → `plugins.json` 登记；包上限 50MB。卸载=删目录+登记；禁用=登记标志；更新=校验后目录原子替换。

## 兼容与迁移

- 前端 `View` 类型从字符串枚举扩为 `string`（内置视图 + `plugin:{id}` 动态视图），内置视图行为不变。
- `plugins/` 目录与 `plugins.json` 均为新增，首次启动自动创建并释放内置剪切板插件；不存在旧数据迁移。
- 控制面新表不影响既有表；不执行 DDL 时商城接口报错但客户端其余功能不受影响（商城页显示「服务不可用」）。
- Core（easyshare-core.exe）零改动——插件运行时整体在桌面端进程，边界清晰。

## 测试计划

- 冲刺期豁免：本切片不写新单测（既有回归全绿 + 构建通过 + 冒烟）。
- 批次 1 冒烟：复制文本/图片/文件 → 剪切板历史即时出现 → 重启不丢 → 密码管理器标记格式不入记录；手动 zip 安装/卸载/禁用插件；未授权 API 调用被拒；删 `plugins/clipboard` 目录重启自动恢复。
- 批次 2 冒烟：publish-plugin.ps1 发布 → 商城页可见 → 安装 → 待办可用 → 周报生成/复制/上传云盘 → 发新版 → 客户端提示更新 → 卸载干净；匿名接口 curl 验证。

## 发布与回滚

- 客户端：随下一版本安装包分发；剪切板插件数据（JSONL + files/）不受卸载/回滚影响。
- 商城：`DELETE /easyshare/plugins/admin/releases/{id}` 下架版本；客户端已装插件继续可用（本地自足），仅停止更新推送。

## 完成记录

### 已完成（2026-08-31）

**批次 1：插件运行时 + 内置剪切板插件**

- `internal/plugin/`（新包，6 文件）：manifest 定义与校验（ID/权限白名单/entry 防逃逸）、zip 安装器（SHA256、zip-slip 防护、总量/文件数/单文件三重上限、staging+原子换入）、`plugins.json` 登记表（builtin 身份只随宿主、目录对账）、Wails AssetServer fallback 静态 serve（禁用/未装一律 404、manifest 与 staging 不可访问）、能力注册表（API→权限鉴权）、插件私有 KV（`plugins-data/{id}.json`，单值 256KB/总量 10MB/500 键上限）
- `internal/clipboard/`（新包）：Windows 监听（message-only 窗口 + `AddClipboardFormatListener`，线程模型同托盘）、文本/图片(DIBV5/CF_DIB→PNG，24/32bpp，bottom-up 翻转，alpha 全零兜底)/文件(CF_HDROP)三类内容、SHA256 前 8 字节去重、来源进程名（GetForegroundWindow→QueryFullProcessImageName）、密码管理器排除格式（ExcludeClipboardContentFromMonitorProcessing / ClipboardViewerIgnore / CanIncludeInClipboardHistory=0）、回写三类型（文本/DIB 构造/DROPFILES 构造）+ selfWrite 防回环、JSONL 追加 + 环形截断（默认 1000 条）+ 图片 LRU（默认 200MB）、`/clipboard-files/` 图片静态路由（仅放行 16 位 hex.png）；非 Windows stub（Start 返回 ErrUnsupportedPlatform）
- 桌面端集成 `appplugin.go`：唯一静态绑定组（PluginList/PluginInvoke/PluginInstallFromPath/PluginSetDisabled/PluginUninstall + 批次2 的 PluginMarketList/PluginInstallFromMarket）；能力注册（clipboard.* ×5、notification.show、drive.upload、storage 走 InvokeFor 分派）；`clipboard:changed` Wails 事件；App.AssetHandler mux（NewApp 建表、initPluginSystem 注册路由，规避 main 构造期时序）
- 资产 `assets/`：公共 SDK `sdk/eshare.js`（postMessage Promise RPC + 事件订阅）；内置剪切板插件 `builtin-plugins/clipboard/`（列表/搜索/类型过滤/点击重新复制/单条删除/清空/暂停记录/实时新条目插入，emoji 徽章 + 来源应用）
- 前端：`usePlugins.ts`（插件状态 + 桥：严格 `event.source` 校验已注册 iframe、clipboard:changed 按权限转发）、`PluginHost.vue`（sandbox iframe + 挂卸登记）、App.vue（View 扩为 `BuiltinView | plugin:${string}`、插件动态导航含内置角标、插件页 flex 布局）、SettingsPanel 插件管理卡（启停开关/卸载/从 zip 安装）、`PluginMarket.vue`（商城+已装双 tab）

**批次 2：商城 + 待办周报插件**

- `deploy/ruoyi-db/easyshare-plugin.sql`：es_plugin / es_plugin_release / es_plugin_release_asset 三表（plugin_id 字符串主键、(plugin_id,version) 唯一=覆盖发布、release_id 唯一资产、两段式 status）
- `PluginController` + `PluginService` + 域/Mapper ×3（平移 AppRelease 模式）：匿名商城列表/单插件 latest/资产 URL；superadmin uploads（upsert 登记信息随发布刷新）/publish（对象存在+大小校验）/releases 列表/删除下架（已装客户端不受影响）
- `easyshare-drive.yml` excludes 增 3 条匿名路径（Ant * 不跨段，admin 多段路径不命中；@SaCheckRole 注解独立于路由级白名单双重保护）
- `scripts/publish-plugin.ps1`：读插件目录 manifest → Compress-Archive → 登录 → uploads → 直传 → publish → 匿名 latest 验证（UTF-8 带 BOM；PS5.1 中文显式 UTF8 字节）
- 客户端商城消费：`internal/plugin/market.go`（匿名 HTTP 客户端 + 内存下载带大小校验）、PluginMarketList（按本地版本回填 updateAvailable，semver 复用 internal/update）、PluginInstallFromMarket（下载→SHA256→InstallBytes）
- 待办周报插件 `plugins-src/todo/`（首个商城插件）：待办 CRUD（storage 持久化）+ 本周聚合周报（完成/进行中/下周计划）+ 复制（clipboard.write）+ 存云盘（drive.upload，走统一云上传任务通道）

### 验证结果（2026-08-31）

- `go build ./...` + `go test ./...` 18 包全绿；`vue-tsc` 无 TS 错误（绑定级联完成）；`wails build` 成功（批次 1、批次 2 后各一次）
- 插件全链路冒烟（`scripts/diag/plugin-smoke`）：安装 zip → 登记 → storage 读写 → 未授权能力拒绝（todo 无 clipboard.read）→ 未知能力拒绝 → 内置插件不可卸载/不可禁用 → 卸载后调用被拒 → 静态 serve 安全（正常 200/穿越 404/未装 404/SDK 200）全部通过
- **商城端到端（dev 控制面本机全链路）**：`easyshare-plugin.sql` 建表 → 重编 platform-drive + ruoyi-admin（`mvnw install` 后 `-pl ruoyi-admin clean package` 强制重打 fat jar）→ 重启控制面 → 匿名 `/easyshare/plugins` 200 → `publish-plugin.ps1` 发布 todo（登录→登记→预签名直传 RustFS→publish 校验→latest 验证）→ curl 匿名列表返回 todo v1.0.0 → 预签名 URL 下载 SHA256 与登记一致 → `scripts/diag/market-smoke` 走 Go MarketClient（列表→下载→校验→安装→登记）全部通过
- 排障记录：① SQL 曾在 release 表误引用 status 列建索引（已修正）；② publish-plugin.ps1 手抄 clientId 漏了 4 字符（`...06a7b32e`，正确 `...06ce24a7b32e`），服务端 500 且 sys_login_info 无失败记录——审计无记录+0ms 返回=校验前异常，clientId 已修并加注释；③ Maven `-T 1C` 并行构建后 `-rf` 恢复会因依赖未 install 失败，用全量 `install` 再 `-pl clean package`；④ ruoyi-admin fat jar 判定 up-to-date 不重打时，必须 `clean package`
- 待真机验收（需 GUI）：剪切板实时记录（复制文本/图片/文件→历史出现→重启不丢→密码管理器格式不入）、插件中心商城页安装 todo、周报复制/上传云盘、删 `plugins/clipboard` 目录重启自动恢复、iframe 加载与 postMessage 桥（AssetServer fallback 在 wails build 下的人工确认）

### 更新机制补全（2026-08-31 追加，用户提出"已装插件的后续升级"）

批次 2 交付时更新链路已通（覆盖式安装 + 商城「更新」按钮 + 版本比对），但有三个缺口，当日补齐：

1. **主动更新提醒**：启动后延迟 15s 检查商城（单个匿名 GET，不落盘节流）→ 发现新版 `EventsEmit("plugin:updates-available")` → 侧边栏「插件中心」红点（复用主程序升级红点样式）；进入插件中心即清除。
2. **权限变更确认（安全补强）**：商城安装改两段式——`PluginPreviewFromMarket`（下载+校验+解压但**不落成**，返回 `PreviewResult{isUpdate, newPermissions}`，首装=全部声明权限、更新=相对本地新增）→ 前端 confirm 展示中文权限名 → `PluginInstallFromMarket(assetId, sha, size, acceptedPermissions)`；`InstallWithConsent` 强制校验「包内新增权限 ⊆ 用户同意集合」，防插件借升级静默扩权。本地 zip 安装（高级路径）保留直装不加确认（用户主动选的文件，风险自担）。预览与安装各自下载解压一次（包小，无状态实现最简）。
3. **更新后热重载**：PluginHost 的 iframe `:key` 含版本号——目录已原子替换 + 响应 no-store，key 变化触发 iframe 自动重载，用户无需切页。

验证：权限同意冒烟（篡改包追加 clipboard.read：未同意被拒 / 同意后放行，`scripts/diag/plugin-smoke` 6.5 节）；真实更新流（todo 1.0.0 预装 → 商城发布 1.0.1 → 预览识别更新 → 权限 diff 为空 → 升级生效，`scripts/diag/market-smoke`）；Go 18 包回归 + vue-tsc + wails build 全绿。todo 插件已随本次升版 1.0.1 真实上架。

### 后续（批次 3 候选）

darwin 剪切板（NSPasteboard changeCount 轮询）、插件独立小窗口（tray_hover WebView2 模式）、开机自启记录、AI 周报（接知识服务网关）、更多能力 API（本地数据表/后台任务/消息中心）、插件遥测与付费授权（开放生态前的重评估项）。
