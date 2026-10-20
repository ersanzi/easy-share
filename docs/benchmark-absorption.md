# 对标吸收清单 — WPS × 钉钉，学到什么 / 落地到哪 / 还有什么没用上

> 这份表是回答「让你学 WPS 和钉钉，你学的是点啥」的唯一交付物。
> 它取代两份取证文档末尾的「差距清单」（`docs/intelligence/2026-10-15-dingpan-file-storage-reverse.md` §9、
> `docs/intelligence/2026-10-15-wps-cfapi-placeholder-reverse.md` §10）——那两张表是 2026-10-15 的快照，
> 到 10-18 已有 14 行过期，并且**在本轮把结论带偏过两次**（误判「预签名不支持 Range」「占位符没接进产品」）。
> 教训写进了 `docs/iterations/2026-10-18-递归删除非空文件夹.md`。
>
> **每条证据都是 `文件 · 符号`，不用行号**（行号会腐烂）。整张表连同 `docs/` 其余文档由
> `node scripts/diag/check-doc-refs.mjs docs` 校验：
> 文件不存在或符号查不到，直接报错。
>
> 状态口径：**已落地** = 有实现且有生产调用方；**部分** = 实现了但只覆盖子集或未接产品；
> **未做** = 全仓搜不到；**判定不做** = 看过证据后主动否掉，理由必须写出来。
> 最后更新：2026-10-18

## 一、网盘（钉盘同步引擎 + WPS 云盘桌面形态）

| # | 学到的点 | 证据出处 | 代码证据 | 状态 | 差在哪 |
| --- | --- | --- | --- | --- | --- |
| 1 | 文件身份与路径解耦，改名移动不换身份 | 钉盘 §9 行1；WPS 云盘对标 §4.1 | `platform-drive/src/main/java/org/dromara/easyshare/drive/domain/EsFile.java` · `fileId`；`internal/drive/client.go` · `DeleteByFileID` | 已落地 | 写路径已按 fileId；`es_file` 唯一索引仍含路径 |
| 2 | 远端版本号 + 前值做 CAS 提交 | 钉盘 §3 | `platform-drive/src/main/java/org/dromara/easyshare/drive/service/DriveFileService.java` · `assertVersion` | 已落地 | 条件提交走请求体字段，不是 HTTP `If-Match` |
| 3 | 空间版本 + 变更流水做增量列举 | 钉盘 §4 | `platform-drive/src/main/java/org/dromara/easyshare/drive/service/DriveChangeLogService.java` · `record`；`internal/drive/changes_apply.go` · `ApplyChanges`；`internal/cfmount/cfmount_windows.go` · `ListState` | 已落地 | 两个消费方（挂载盘 + 占位符）都吃到；裁剪未按空间分别保留 |
| 4 | 云端文件本地只留空壳，点开才下载 | 钉盘 §5；WPS §2、§9 | `internal/cfapi/mount_windows.go` · `MountSpace`；`internal/cfmount/cfmount_windows.go` · `Entries` | 已落地 | 只读形态，双向同步另立项 |
| 5 | 按需取数只下被读区间 | WPS §3.4；钉盘 §5 | `internal/httprange/range.go` · `NewFetcher`；`internal/cfmount/cfmount_windows.go` · `Open` | 已落地 | 活栈实测 3 MB 对象首/中/尾三段均 206 |
| 6 | 「始终保留在这台电脑上」「释放空间」 | WPS §0b 第四步、§3.5 | `internal/cfapi/state_ops_windows.go` · `SetPinState`；`cfmount_app_windows.go` · `CloudFreeUpSpace` | 已落地 | 资源管理器自带菜单项是否出现未点验 |
| 7 | 取数进度要回报给系统，否则大文件像卡死 | WPS §6 | `internal/cfapi/progress_windows.go` · `reportProviderProgress`；`internal/cfapi/mount_windows.go` · `readProgressively` | 已落地 | 单次区间 < 8 MiB 不回报（内核不弹框） |
| 8 | 冲突规则写死在程序里，不弹窗问用户 | 钉盘 §6 | `app.go` · `conflictObjectKey`；`app.go` · `sameContentOnRemote` | 已落地 | 四条规则齐；无 `conflictStrategy` 协议字段 |
| 9 | 内容哈希 + 秒传 | 钉盘 §7；WPS §10（`fsha1`） | `platform-drive/src/main/java/org/dromara/easyshare/drive/service/DriveFileService.java` · `findByContentHash`；`deploy/ruoyi-db/easyshare-content-hash.sql` | 已落地 | 只有整文件 MD5，无分片级哈希与服务端代算 |
| 10 | 删除是可还原状态，不是消失 | WPS 云盘对标 §4.2；钉盘 §9 行7 | `platform-drive/src/main/java/org/dromara/easyshare/drive/service/DriveFileService.java` · `markRecycled`；`platform-drive/src/main/java/org/dromara/easyshare/drive/service/RecycleJanitor.java` · `RETENTION_DAYS` | 已落地 | 远端软删齐；本地不进系统回收站 |
| 11 | 删目录就是删整棵子树 | 钉盘 §9 行9；WPS §10 | `platform-drive/src/main/java/org/dromara/easyshare/drive/service/DriveFileService.java` · `recycleSubtree`；`platform-drive/src/main/java/org/dromara/easyshare/drive/DriveStorage.java` · `deleteDirectoryTree` | 已落地 | 逐行提交，无单事务边界 |
| 12 | 服务端改名/移动，字节不出对象存储 | 钉盘 §9 行9 | `platform-drive/src/main/java/org/dromara/easyshare/drive/service/DriveMoveService.java` · `move` | 已落地 | 目录改名与跨空间移动不支持 |
| 13 | 目录是云端实体，所有端一致 | 钉盘 §9 行10 | `platform-drive/src/main/java/org/dromara/easyshare/drive/DriveStorage.java` · `createDirectory`；`internal/spacedav/spacedav.go` · `Mkdir` | 部分 | 两个入口的标记现在一致；服务端仍无目录行（无 `parent_id`/目录 fileId，目录不进回收站） |
| 14 | 失败要留下可诊断的三件套 | WPS 云盘对标 §4.6 | `internal/driveerr/driveerr.go` · `Classify` | 已落地 | 业务码 + 人读文本 + 系统码齐 |
| 15 | 读盘要按进程节流，否则被杀软和索引器打爆 | WPS §3.5 | `internal/cfapi/limiter_windows.go` · `Delay` | 已落地 | 只限取数次数，无并发/带宽闸门 |
| 16 | 三层状态位表达文件状态 | WPS §3.4 | `internal/cfapi/cfapi_windows.go` · `UpdateFlagMarkInSync` | 部分 | in-sync + pin 两层；custom state 见第 22 行 |
| 17 | 占位符身份要稳定绑定云端文件 | WPS §5（`fs_metadata_table`） | `internal/cfapi/mount_windows.go` · `EncodeIdentity` | 部分 | 无本地元数据表；脏数据退回路径哈希，不跨会话稳定 |
| 18 | 状态对不上要能自愈 | WPS §3.5；钉盘 §8 | `internal/cfapi/tree_windows.go` · `removeGhosts` | 部分 | 只有幽灵清理，无周期性一致性审计与重试队列 |
| 19 | 配额与策略要落到本地看得懂 | 钉盘 §9 行11 | `platform-drive/src/main/java/org/dromara/easyshare/drive/service/CapacityService.java` | 部分 | 有配额软校验与前端提示；无单文件上限、无 NTFS ACL 下发 |
| 20 | 编辑锁防止两人同时改 | 钉盘 §9 行8 | — | 未做 | 全仓零命中（`edit_lock`/`tryGetEditLock`/`lock_expire_time`） |
| 21 | 元数据扩展用 KV 承载，不改表 | WPS §10 行13 | — | 未做 | 加列要改表；`extraInfo` 只作为取证引文存在 |
| 22 | 自定义状态位承载业务状态 | WPS §3.4、§625 取值清单 | — | 判定不做 | 本机 SDK 头 `cfapi.h` 无任何 CustomState 接口，WPS 走的是未文档化入口；且「哪个状态画哪个图标」的映射取证时自认未拿到。没有映射表就照抄未文档接口属于猜 |
| 23 | 本地元数据表双向绑定 NTFS 身份 | WPS §5 | — | 判定不做 | 只读挂载不会发生本地改名/移动，这张表是给双向同步用的；身份翻转经核实是原地更新（`internal/cfapi/tree_windows.go` · `updateExisting`），不需要表 |
| 24 | 双向同步（本地改动传回云端） | 钉盘 §2；WPS §3.5 | — | 未做（切片 5b 另立项） | 现在整片置只读位，写入被明确拒绝而不是静默丢失（实测见 `internal/cfmount/README.md`） |

## 二、界面（WPS KD 设计体系 + 钉钉逐像素实测）

| # | 学到的点 | 证据出处 | 代码证据 | 状态 | 差在哪 |
| --- | --- | --- | --- | --- | --- |
| 25 | 颜色是算出来的：色阶 → 语义 → 组件三层 | WPS 界面逆向 §2.1 | `frontend/src/style.css` · `--kd-blue-6` | 部分 | 蓝与中性建成阶，其余语义色只有基色 + 新补的文字/浅底两档，未成 10 阶 |
| 26 | 中性色带 220° 蓝相，越浅越去饱和 | §1 规律2 | `frontend/src/style.css` · `--kd-text-4`；`frontend/src/utils/designTokensLint.ts` · `NEUTRAL_ALLOWLIST` | 已落地 | 残留 3 处近中性（装饰图标灰与深色模式底），已进白名单并有测试挡住新增 |
| 27 | 品牌蓝降饱和到 S=76% | §2.2、§5.1 | `frontend/src/style.css` · `--blue` | 已落地 | 旧蓝（`#007aff` 一族）全仓零命中 |
| 28 | 透明叠加用深色基 + alpha | §1 规律3 | `frontend/src/style.css` · `--line-rgb` | 已落地 | — |
| 29 | 状态取相邻阶，文字要过 AA | §2.4、§8.3 | `frontend/src/style.css` · `--green-text`；`frontend/src/utils/designTokensLint.ts` · `TEXT_ON_TINT_PAIRS` | 已落地 | 四组文字/浅底档实测 4.89~6.51 全过 AA |
| 30 | 侧栏选中态用浅蓝底，不用实心蓝块 | §4、§9.2 | `frontend/src/style.css` · `--blue-selected` | 已落地 | — |
| 31 | 文件类型彩色图标 | §9.2 | `frontend/src/components/CloudPanel.vue` · `fileKind` | 已落地 | 九类，认不出回落 generic |
| 32 | 列表行高 64px、名称 14px | §3.1 | `frontend/src/style.css` · `.cloud-row` | 部分 | 64px 靠 `13+38+13` 撑出来，没有显式行高 token，改图标高度就塌 |
| 33 | 外壳不叠大渐变与毛玻璃（Windows） | §5.2 病灶4 | `frontend/src/style.css` · `--bg-chrome` | 已落地 | macOS 按 AGENTS 要求保留毛玻璃，走 `--bg-glass-*` 单独一档 |
| 34 | 组件里不写字面色值 | AGENTS 硬约束 | `frontend/src/__tests__/designTokens.test.ts` · `lintDesignTokens` | 部分 | 字面值 217 → 130；剩余含导出物皮肤 37 处与蓝/青两个待决色系 |
| 35 | 三段式布局（窄 rail + 面板） | §3.1、§6 P2；落地记录 §22 | `frontend/src/App.vue` · `sectionMeta`；`frontend/src/style.css` · `.nav-rail` | 已落地 | 56px 轨 + 240px 面板；**此前误记为「判定不做」**——§9.3 否掉的是侧栏 130px，不是三段式。网盘域面板与工具条入口尚不对等 |
| 36 | 不抄团队空间、设备管理面板、固定文件夹同步根 | WPS 云盘对标 §7 | — | 判定不做 | 单组织私有部署用不上；同步根已由 CfApi 占位符方案覆盖 |

## 三、会议听记（钉钉 AI 听记前端逆向）

| # | 学到的点 | 证据出处 | 代码证据 | 状态 | 差在哪 |
| --- | --- | --- | --- | --- | --- |
| 37 | 区块级独立状态，某块失败只重跑那块 | 听记逆向 §4 选择1 | `knowledge/app/meetings/store.py` · `set_block` | 已落地 | 多一个 `skipped` 态；分块仍是整页轮询刷新 |
| 38 | 待办是一等实体（负责人/期限/勾选） | §7 E15/E16 | `knowledge/app/meetings/store.py` · `add_todo` | 已落地 | 负责人与期限是自由文本，无日历同步与提醒 |
| 39 | 关键词独立抽取、可单独补跑 | §7 E14 | `knowledge/app/meetings/service.py` · `_run_keywords_block` | 已落地 | — |
| 40 | 改名即沉淀声纹，跨会自动认人 | §7 E17 | `knowledge/app/meetings/voiceprint.py` · `MATCH_THRESHOLD` | 已落地 | 阈值 0.67 是量出来的；无授权同意文案。2026-10-20 补：发言人**合并**（`knowledge/app/meetings/service.py` · `merge_speakers`），重聚类后旧序号上的人工标注不再沿用（`apply_voiceprints` 以声纹为准） |
| 41 | 个性化热词提升专名识别 | §7 E18 | `knowledge/app/meetings/hotwords.py` · `validate` | 已落地 | 口径照抄钉钉（字数/条数上限）；2026-10-20 起实时推流也吃热词（`knowledge/app/meetings/stream.py` · `_recognize`） |
| 42 | 实时上屏 + 会中要点 | §7 E7/E8/E21 | `knowledge/app/meetings/stream.py` | 部分 | 只做预览，终态以离线重跑为准；会中要点是一维列表，不是可编辑摘要树。2026-10-20 补：句末/逗号级断行（`knowledge/app/meetings/livenow.py` · `should_break`）、会中快检（`engine/minutecraft/summarizer.py` · `live_check`）、推流自愈与轮询兜底（`frontend/src/services/meetingSession.ts` · `pumpStream`）。**实时无发言人**：流式模型不含说话人分离 |
| 43 | 听记数据要能被外部 AI 工具读 | §7 E23（钉钉本机 MCP 六件套） | `internal/meetingsmcp/meetingsmcp.go` · `list_meetings` | 已落地 | 现有 7 个只读工具；单域授权，组织域与写回未做 |
| 44 | 导出到在线文档、DIY 模板、换模型重跑、视频导入、分片续传 | §4、§7 E11/E12/E19/E22 | — | 未做 | 只有内置模板与音频导入；大文件仍一次性 multipart |

## 四、怎么复核这张表

```bash
node scripts/diag/check-doc-refs.mjs --self-test                     # 闸门自己会报错（注入坏引用）
node scripts/diag/check-doc-refs.mjs docs                             # 全 docs/ 证据可解析（文件在、符号在）
cd frontend && npx vitest run                                         # 含设计 token 守卫测试
cd frontend && npx vue-tsc --noEmit                                   # 类型与 Wails 绑定同步
node scripts/diag/color-literals.mjs                                  # 色值纪律现状（诊断报告，不是闸门）
node scripts/diag/recursive-delete-check.mjs                          # 活栈：递归删与回收站
node scripts/diag/presign-range-probe.mjs                             # 活栈：分段下载
```

前四条已经进 CI：`quality-gates.yml` 在推 `dev` / 提 PR 时自动跑（文档引用自检 + `docs` 全量 + `npm test` + `vue-tsc`）。
后三条没进 CI：色值脚本是给人看的报告，两条活栈脚本要真实控制面和 RustFS，本机才具备。

**状态会变，行号会烂，符号不会**——所以本表只写符号，并由脚本保证引用有效。
新增行时请照此格式，否则 `check-doc-refs` 会红。
