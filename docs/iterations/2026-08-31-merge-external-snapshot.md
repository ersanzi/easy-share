# 2026-08-31 合入外部协作者批次（RuoYi 账号控制面 + 托盘悬浮窗）

## 主题

外部协作者以压缩包回传的工作（`example/私人云盘项目/easy-share-可修改`，快照基点 `f5524f9`，
2026-08-28~30 的工作，**以未提交工作区形式**回传）整理合入 dev 并推送双仓库。压缩包内另含
基线副本（`easy-share-不可动，当做比较`，仅构建副产物差异）与 plus-ui 嵌套仓库，均不入库。

## 批次内容（详见各迭代记录与 ADR-0007）

| 主题 | 关键落点 |
| --- | --- |
| 账号控制面 P0：RuoYi-Vue-Plus 6.0 环境 | `deploy/ruoyi-db/`（PG16 :5432 + Redis :6380 + 登录验证） |
| P1：桌面登录门禁 + 登录态贯通 | `LoginView.vue`、`internal/account/`、账号 chip |
| P2：按用户隔离的存储授权 | `platform-drive/`（预签名 URL）、`internal/drive/`、删 `internal/cloud/defaults.go`，KI-2 关闭 |
| P3：管理面板 + 空间/配额/池上限 | `AdminPanel.vue`、`es_space`/`es_space_member`、`CapacityService` |
| 托盘悬停浮窗切片 1+2 | 原生 `Shell_NotifyIcon` + Win32 浮窗内嵌 WebView2，移除 systray |
| 支撑模块 | `internal/winui/`、`internal/spacedav/`、`spacemount.go`、`docs/known-issues.md` |

## 合并方式

1. 在快照基点 `f5524f9` 建 `merge/external-account-plane` 分支，落地外部工作区为单提交
   `b1de4dc`（97 文件，+12650/−249；排除 `logs/`、`platform-drive/target/` 构建产物与嵌套 git 仓库）。
2. `git merge` 进 dev（dev 领先基点 23 个知识平台提交）。冲突 5 处：`preview_test.go`
   （双方各自修了 KI-4，取外部版本）、`App.vue`/`core.ts`/`style.css`（并集：知识页 + 登录/管理页共存）、
   `package.json.md5`（取本方）、`progress.md`（双方结构重组，手工并集并修正外部侧的过时表述）。
3. `app.go`、`internal/api/server.go`、`cmd/core/main.go`、wailsjs 绑定、`types/core.ts` 全部自动合并成功。

## 密钥扫描

推送公网仓库前全量扫描新增文件：无 AKIA/sk-/ghp_ 模式；`easyshare-drive.yml` 密钥全部
`${RUSTFS_ACCESS_KEY}` 环境变量注入，真值在 gitignore 的 `deploy/rustfs/.env`。旧 dev 凭据
随 `defaults.go` 删除一并出库。

## 验证（DoD：回归绿 + 构建过）

- `go build ./...` 通过；`go test ./...` 18 包全绿
- `vue-tsc --noEmit` 通过；`vitest run` 33/33（含合入的 AdminPanel 12 条）
- `knowledge` pytest `-m "not integration"`：120 passed, 1 skipped
- `wails build` 通过；**重生成绑定与手工合并零差异**（App 结构体 60 方法 − 1 个生命周期钩子 = 59 绑定，逐一对上）
- 排障记录：合并 style.css 时误删 `@media` 块闭合大括号导致 vite 构建失败（tailwind 报
  "Missing closing }"），补回即可；这也是"手工解冲突后必须跑一次完整构建"的原因

## 文档同步

- `docs/architecture.md`：进程模型补控制面（8090/8091/5432/6380）、代码入口补 6 个新模块、
  §7/§8/§10 移除 defaults.go/systray 过时表述、§8 重写为预签名 URL 链路 + KI-5 遗留说明
- `AGENTS.md`：产品定位"编译期常量"改为"控制面 + 预签名 URL"；架构速查补 Java 控制面；坑表补 platform-drive 构建前提
- `docs/progress.md`：阶段 4 转"进行中"，登记外部批次条目与已知阻塞

## 遗留

- 真机验收：登录 → 上传 → 换账号看列表（外部侧仅验证编译与类型）
- KI-5：Core 侧 `/api/cloud/*` 死路由约 900 行待删（前置：P4 挂载方案定夺 webdavfs 去留）
- 「此电脑 → EasyShare 网盘」入口因 KI-2 修复暂不可用，恢复归 P4
- `docs/plans/2026-08-30-space-pool-and-organize.md` 整理算法部分待实施
