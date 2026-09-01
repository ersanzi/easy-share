# 插件工程拆解为独立仓库 — 计划

日期：2026-09-01
状态：**待触发**（不立即执行；见「拆与不拆的权衡」，满足任一触发条件即启动）
前置：插件系统已全量落地（运行时/商城/更新机制/独立插件工程，见
[`iterations/2026-08-31-plugin-system.md`](../iterations/2026-08-31-plugin-system.md)）

## 现状与边界

`plugins/` 已是自包含的根目录插件工程（todo 插件 + template 模板 + dev 热调工具 +
README 开发指南），与主程序只通过两个稳定契约耦合：

1. **插件包规范**：manifest.json 字段与权限白名单（`internal/plugin/manifest.go`）
2. **宿主能力 API**：SDK（主仓 `assets/sdk/eshare.js`，宿主内嵌统一分发）

主仓对 `plugins/` 有**一处代码级依赖**（2026-09-01 起）：内置剪切板插件的源码
就在 `plugins/clipboard/`（用户要求插件代码统一进插件工程管理），主仓
`appplugin.go` 经 `//go:embed all:plugins/clipboard` 直读分发，插件版本 2.0.0。
**这是记录在案的唯一例外**：拆分执行时该目录不随 subtree 走，需先搬回主仓
（建议 `assets/builtin-plugins/clipboard/` + `EnsureBuiltin` 多目录路径，或保留
embed 直读并把该目录留在主仓）——第 1 步的 grep 检查会命中 `appplugin.go`，
按本段处置后再继续。

**边界共识（拆后不变）**：主仓 = 宿主 + 内置插件（剪切板等随宿主分发的能力）；
插件仓 = 商城插件（官方自营发布）。

## 拆与不拆的权衡

| | 现在就拆 | 挂触发条件（推荐） |
| --- | --- | --- |
| 收益 | 目录干净、职责先行 | 无收益损耗——拆分动作本身随时可做且成本低（见步骤） |
| 代价 | 双仓跳转、发布凭据两处管理、SDK 文档手动同步立刻生效吃进 | 无（唯一"代价"是 plugins/ 暂在主仓，摩擦接近零） |
| 适用 | 已有外部协作者要进插件仓 | 当前只有 todo 一个插件，主仓内开发 friction 最低 |

**推荐：挂触发条件，暂不执行。** 拆分是单向门但随时可开，提前拆只预付协作成本。

### 触发条件（满足任一即启动本计划）

1. 商城插件数量 ≥ 3 个（todo 之外再有两个，如批次 3 的 AI 周报、新工具类插件）
2. 出现非本团队成员参与插件开发（哪怕只是外包/实习生）
3. 插件发布节奏明显快于主程序（一周多次上架 vs 主程序月级发版），主仓提交流被插件提交刷屏
4. 客户要求插件定制化交付（私有 fork 场景需要独立仓库隔离）

## 拆分步骤（触发后按序执行，预计半天）

### 第 1 步：主仓侧检查（拆前最后一道确认）

- [ ] `git grep -l "plugins/" -- '*.go' '*.ts' '*.vue'` 确认无代码引用（现状为零）
- [ ] `plugins/README.md` 迁移指引与实际一致
- [ ] 商城在架插件与 `plugins/` 目录一一对应（没有只存在于其中一侧的孤儿）

### 第 2 步：历史拆分与新仓库

- [ ] `git subtree split -P plugins -b plugins-split`（保留 plugins/ 全部提交历史）
- [ ] 新仓库（建议名 `easyshare-plugins`，Gitee + GitHub 双仓库，同主仓规范）push 该分支为 `master`
- [ ] 新仓库补 `.gitignore`（无构建产物，基本为空）与 README（从 plugins/README.md 升格，头部加仓库定位说明）

### 第 3 步：插件仓自包含补全

- [ ] 带走 `scripts/publish-plugin.ps1` 副本：`-PluginDir` 已支持绝对路径无需改逻辑，仅更新头注释的仓库根推导说明
- [ ] dev 热调工具：`plugins/dev/main.go` 的 `-root` 参数已支持显式指定 plugins 目录，在新仓库根执行无需改动
- [ ] 发布凭据：脚本默认 admin/admin123 仅限本机 dev；生产发布改用参数传 token（脚本已有 `-Username/-Password`，禁止写死入仓）

### 第 4 步：主仓收尾

- [ ] 删除主仓 `plugins/` 目录
- [ ] `docs/architecture.md` §4c 与迭代文档补"插件源码已迁至独立仓库"指向
- [ ] `docs/progress.md` 登记拆分事件；本计划文档标记"已执行"
- [ ] AGENTS.md 无需改动（未提及 plugins/）

### 第 5 步：验证（DoD）

- [ ] 从插件仓打 todo zip → publish-plugin.ps1 上架新版本 → 客户端更新全链路通
- [ ] 插件仓 `go run ./dev -plugin todo` 热调回路可用（junction + 登记）
- [ ] 主仓删目录后 `go build ./...` + `wails build` + `/verify` 全绿
- [ ] template 模板在新仓 README 的起步指引可跑通

## 拆后协作约定

1. **规范变更单向流**：插件包规范与权限白名单的修改只发生在主仓（宿主侧）；
   变更后主仓提交信息带 `[plugin-spec]` 前缀，插件仓负责人据此同步其 README 权限表。
   插件仓不得自行扩展权限语义（加了也装不进——安装器按主仓白名单校验）。
2. **SDK 兼容策略**：SDK 只在主仓演进（`assets/sdk/eshare.js`，宿主统一 serve，插件
   包内严禁自带副本）。SDK 出现不兼容变更时：升 SDK 语义化版本 + 在插件仓 README
   公告兼容矩阵（宿主版本 ↔ SDK 版本 ↔ 插件最低要求）。manifest 已预留
   `minHostVersion` 字段，必要时启用强制校验（当前仅记录不强制）。
3. **发布流不变**：插件仓开发 → publish-plugin.ps1 上架控制面 → 客户端商城分发。
   控制面（platform-drive）永远在主仓，插件仓只产 zip。
4. **CI（可选项，拆后初期不做）**：插件仓 GitHub Actions 做 manifest 校验 + 打 zip；
   自动上架需 superadmin 凭据（GitHub Secrets），等发布频率上来再评估。

## 风险与重评估

- **风险**：两仓库 README 各自演化失同步 → 缓解：协作约定 1/2 的单向流 + 兼容矩阵。
- **风险**：拆分后忘掉主仓文档里的 plugins/ 引用 → 缓解：第 4 步清单逐项打勾。
- **重评估**：若 6 个月内（2027-03 前）触发条件均未满足，说明插件生态未起量，
  本计划保持"待触发"即可，无需主动制造拆分理由。
