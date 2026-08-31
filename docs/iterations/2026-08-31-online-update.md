# EasyShare 客户端在线升级（控制面托管）

## 用户问题

- 已安装 EasyShare 的用户（公司同事/外部体验者）拿到的是某个固定版本的安装包；新版本发布后只能人工重新分发安装包，用户没有任何「应用内有新版本 → 一键升级」的通道，像 ZCode/Trae 那样的在线升级体验完全没有。
- 升级源若依赖 GitHub Releases，国内直连不可靠；且产品正走向企业私有部署（发芽路线），升级分发天然应该跟着账号控制面走。

## 目标

- Windows 客户端：设置页「检查更新 → 下载（进度/SHA256 校验）→ 重启并更新 → NSIS 静默安装 → 自动重启到新版本」全自动闭环。
- macOS 客户端：检测到新版本后提醒并引导下载（产物未签名，自动替换留后续迭代）。
- 控制面（platform-drive）：新增版本清单与资产接口——管理员上传安装包（预签名直传 RustFS，控制面不在数据路径上）、发布/回滚，客户端匿名检查/取下载 URL（升级检查不要求登录）。
- 发布脚本：`scripts/publish-release.ps1` 一条命令完成「登录 → 上传 → 发布 → 验证」。

## 非目标

- 差分/增量更新、断点续传（全量安装包约 20MB，整包重下）。
- macOS 自动替换 .app（先决条件是签名/公证链路，未建）。
- Windows 安装包代码签名（Authenticode）与强制校验。
- tag 稳定通道/灰度按比例放量；管理页上传 UI（v1 用脚本发布）。
- GitHub Releases 作为升级源（维持现状作为开发侧分发渠道，不接入客户端升级检查）。

## 设计决策

- **升级源 = RuoYi 控制面**（用户 2026-08-31 拍板）：复用 `config.json` 的 `platformBaseUrl`，不新增配置字段。企业私有部署天然自托管升级；放弃的替代方案：GitHub Releases（国内不可靠）、双通道（范围翻倍）。
- **上传走预签名 PUT 两段式**（与云盘上传同哲学）：`POST /admin/uploads` 建记录返回预签名 URL → 客户端直传 RustFS → `POST /admin/assets/{id}/publish` 校验对象存在且大小一致后置为已发布。安装包不经 Java 进程中转。
- **匿名检查**：`GET /easyshare/app/latest` 与 `GET /easyshare/app/assets/{id}/url` 用 `@SaIgnore` 放开（升级检查先于登录）；若 sa-token 拦截器不认注解，退路是 `easyshare-drive.yml` 的 `security.excludes` 白名单。安装包本就公开，接受匿名可拉（风险记录在案）。
- **下载 URL 现取现用**：预签名 GET 有效期沿用 10 分钟，客户端每次下载前重新解析，不做长缓存。
- **版本比较**：客户端手写 semver 比较（v 前缀/点分数字/-preview.1 后缀），不引依赖；预发布 < 正式版。
- **应用方式**：Windows 下载的是完整 NSIS 安装包；应用时客户端先优雅停 Core，再以 `/S /update` 启动安装包（detached），NSIS 安装区段开头 taskkill 兜底杀残留进程、尾部按 `/update` 自动重启应用。退出链路从 `quitFromTray` 抽出共用 `quitAll`。
- **绿色版/安装版检测**：Windows 读注册表 `HKCU\Software\EasyShare\EasyShare\InstallDir` 与当前 exe 目录比对；绿色版不给「重启并更新」，只引导下载后手动运行。
- **需同步 `architecture.md`**：控制面新增 `/easyshare/app/*` 端点清单；桌面端新增升级流程描述。

## 兼容与迁移

- config.json 无新增字段（复用 `platformBaseUrl`）。
- 数据库新增两张表（`deploy/ruoyi-db/easyshare-app-release.sql`），不影响既有表；控制面不执行该 DDL 时升级检查接口返回「暂无发布」，其余功能不受影响。
- NSIS 改动对人工交互安装向后兼容（多了一段杀进程；`/S` 静默链路是新增能力）。
- 旧客户端（0.1.0 已分发）不感知新接口，无共存问题。

## 测试计划

- 冲刺期豁免：本切片为客户端功能增强，不写新单测（既有 Go/前端回归全绿 + 构建通过 + 冒烟）。
- 冒烟主线：build 出 0.1.0 → 版本改 0.1.1 再 build → publish-release.ps1 传本机 RuoYi → 0.1.0 客户端检查到更新 → 下载（进度/校验）→ 重启并更新 → 静默安装自动重启显示 0.1.1。
- 匿名接口 curl 验证（无 token 拉清单/下载 URL）。

## 发布与回滚

- 发布：`scripts/build.ps1` 产出安装包 → `scripts/publish-release.ps1` 上传控制面。
- 回滚：管理端 `DELETE /easyshare/app/admin/releases/{id}` 删除发布记录（客户端即查不到该版本）；客户端侧升级失败保留旧版本未动（NSIS 覆盖安装前不删旧文件）。
- 日志：desktop.log 记录检查/下载/校验/应用各环节；`update:error` 事件带可读错误。

## 完成记录

### 已完成项（2026-08-31）

**控制面（platform-drive）**
- `deploy/ruoyi-db/easyshare-app-release.sql`：`es_app_release` + `es_app_release_asset` 两表，version 唯一（重传=覆盖发布），资产 pending→published 两段式
- `domain/EsAppRelease|EsAppReleaseAsset` + Mapper ×2、`domain/AppManifestVo|AppReleaseVo|UploadVo`（record，ID 字符串化防 JS 精度丢失）
- `service/AppReleaseService`：prepareUpload（预签名 PUT 两段式，安装包不经控制面）、publishAsset（校验对象存在且大小一致）、latest（按平台+已发布过滤，release create_time 取最新）、resolveDownloadUrl（10 分钟预签名现取现用）、listAll/deleteRelease（回滚=删记录+删对象）
- `AppReleaseController`：匿名 `/easyshare/app/latest`、`/easyshare/app/assets/{id}/url`；superadmin 的 uploads/publish/releases 增删
- `easyshare-drive.yml` 增加 `security.excludes` 白名单（**RuoYi 的路由级 checkLogin 不认 @SaIgnore**，只能走白名单；已按 platform/ 6.0.0 真实基线逐条核对，列表为整体替换语义）

**客户端**
- `internal/update/`：手写 semver 比较（v 前缀/缺段补零/预发布<正式）、流式下载器（.part 临时文件+边下边算 SHA256+大小校验+原子改名）、`apply_windows.go`（`/S /update` detached 启动）、`installed_windows.go`（注册表 InstallDir 比对判断安装版/绿色版）、非 Windows 桩
- `appupdate.go`：绑定 `AppVersion/CheckUpdate/StartUpdateDownload/ApplyUpdate/OpenUpdatesFolder`；下载进度 200ms 节流 + 速度，经 `update:progress|downloaded|error` 事件推送；启动 24h 节流自动检查（`update-state.json`），命中发 `update:available`
- `app.go`：退出链路重构（`beginQuit`/`quitAll`，托盘退出与升级应用共用；watchdog 竞态由既有 `isQuitting` 检查覆盖）
- NSIS `project.nsi`：安装区段开头 taskkill 双进程、`/update` 参数（GetOptions）安装完自动重启、开机自启 MessageBox 包 `${IfNot} ${Silent}`（静默模式下 MessageBox 返回默认按钮 IDYES 会误开自启）
- 前端：设置页「关于与更新」卡片（版本/检查/进度条/重启并更新/打开下载目录）+ 设置入口红点；`types/core.ts`/`services/core.ts` 绑定级联完成
- `scripts/publish-release.ps1`：登录→登记→预签名直传→发布→清单验证，版本号自动解析

### 环境事实（本次搭建时确认，供下次复用）
- 本机原生 PostgreSQL 服务占用 5432（凭据不明，勿动）→ dev PG 容器改映射 **5433**（compose 已更新，application-dev.yml 同步）
- JDK21 实际在 `D:\Develop\java21`（README 原路径不存在）；系统全局 JAVA_HOME 是 17，启动控制面须显式覆盖
- Docker Hub 直拉超时，postgres:16 经 `docker.m.daocloud.io` 镜像源拉取
- platform/ 工程为 gitee 6.X 分支 clone（revision 6.0.0，79bd1db）；可选模块 ruoyi-snailai-server 因 milvus/es 依赖下载损坏构建失败，不影响 ruoyi-admin fat jar

### 测试结果（2026-08-31，本机全链路验收）
- `go build ./...` + `go vet` + `go test ./...` 全绿；前端 vitest 33 passed；`vue-tsc` 无 TS2305/TS2339；`build.ps1` 两次全量构建（0.1.0/0.1.1）成功
- 发布链路：publish-release.ps1 登录→直传 RustFS→发布→latest 验证 ✓（含同版本覆盖发布语义）
- 无头链路：临时 Go 程序验证 清单解析/资产选择/semver 比较三例/下载 URL 解析/下载+SHA256 校验（84ms/12.5MB）✓
- **UI 全流程（真实安装的 0.1.0 客户端，鼠标驱动）**：启动自动检查状态落盘 → 登录 → 设置页检查更新发现 0.1.1 → 下载（SHA 校验落盘 updates/）→ 重启并更新 → 优雅退出 → NSIS 静默安装 → 自动重启 → 设置页显示 **EasyShare 0.1.1** → 再查「已是最新版本（0.1.1）」**全部通过**
- 匿名接口 curl 无 token 可访问 latest/asset-url ✓；登录鉴权未受白名单影响 ✓

### 已知问题与后续
- 发布说明中文乱码：PS 5.1 Invoke-RestMethod 字符串体默认 ISO-8859-1，已改 UTF-8 字节提交并覆盖重发验证；坑记入 troubleshooting.md
- macOS 路径仅「检测+前往下载」（BrowserOpenURL 预签名 dmg），自动替换待签名链路
- GitHub Releases 渠道未接入客户端升级检查（本次决策仅控制面）；tag 稳定通道/灰度、断点续传、差分、Authenticode 留后续
- 托盘菜单「检查更新…」入口未做（低优先，设置页已有完整入口）
