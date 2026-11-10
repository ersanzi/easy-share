# Windows 个人云盘：云端主存实验通路

> 本文描述CFAPI个人云盘及其服务器前置，不是正式发布或完整双向同步承诺。当前进度和本次提交验证见 [迭代记录](iterations/2026-11-08-windows-cloud-primary.md)。

## 用户可见变化

- 从Windows“此电脑”打开个人云盘，根目录可新建文件/目录、粘贴、拖入和打开编辑；保存后异步写回服务器。
- 服务器是正文主存。Windows同步根是接入机制和透明缓存，未打开文件不主动下载正文；显式复制出云盘才形成用户自己管理的普通本地副本。
- 上传内容经独立服务器回读确认后，默认空闲5分钟自动回收正文；保留同一占位文件身份，重开时按需读取。
- 托盘/悬浮窗使用同一登录与个人空间。悬浮窗“打开”进入同一个Shell入口，真实文件拖入走云端上传任务，不投递到局域网收件箱。
- 正常退出保留持久同步根及状态；重启恢复账号、前端登录显示和“此电脑”入口。

## 不支持或刻意保守的行为

- **Windows共享CFAPI尚未完成权限执行适配**，不发布入口，不以只读外观代替真正的拒写。主窗口的共享操作和非Windows既有WebDAV通路不因此删除。
- 写回只处理新增、编辑和目录创建；**不把资源管理器删除/改名推断为远端删除/移动**，不从远端清单缺项删除本机内容。
- 已有水合文件/未知普通目录和冲突保持保护，不保证另一客户端更新会自动替换本机已缓存正文。
- 相同持久目录在旧 `/mkdir`、`includeDirs` 与明确递归删除接口中保持兼容；主窗口目录删除按稳定fileId（可用时）和显式递归标记操作，不等于CFAPI本机删除/改名自动同步。
- 可写CFAPI目前保守使用全量清单，不宣称已有生产增量同步性能。
- 占用、pending、冲突、未确认正文、pin和不完整缓存不会自动回收；状态损坏时拒绝重新认领基线，不能删pending来“修复”。

## 服务端前置和兼容

必须配套部署 `platform-drive` 和目录迁移。旧服务端缺能力接口时，客户端在建根/发布入口前明确拒绝，不回退为另一条看似成功的本机路径。

| 接口/数据 | 用途 |
| --- | --- |
| `GET /easyshare/drive/capabilities` | 已认证能力协商：`protocolVersion: 1`、`persistentDirectories: true` |
| `POST /easyshare/drive/directories` | 创建有稳定fileId的数据库目录，返回 `directory: true`；同名目录幂等，文件/目录冲突拒绝 |
| `/objects`、`/changes` | 清单及变更带目录类型；文件仍以对象存储正文为存在性依据，空目录由数据库维护 |
| `deploy/ruoyi-db/easyshare-directory.sql` | 在file/recycle/incremental迁移之后添加目录字段及树结构约束 |
| 预签名上传 | 保留expectedVersion条件；缺失秒传源、空文件和中断上传重试不能伪造成功或无条件覆盖 |

兼容最新版已有的 `/mkdir`、`includeDirs` 和明确递归删除接口，不能只把旧服务端文件覆盖到新上游；新旧目录共存、去重、类型冲突与子树操作必须一起验证。迁移与部署顺序见 [控制面开发环境](../deploy/ruoyi-db/README.md)。

**迁移顺序**：备份数据库及运行程序 → 维护窗口内按既有顺序执行基础业务迁移，再执行目录迁移 → 部署配套控制面 → 认证查询能力与隔离目录测试 → 才接入新客户端。本仓此次提交不自动操作任何服务器。

回退优先回退程序、保留新增列及数据；不要为回退删除directory列/触发器或覆盖旧pending。数据库目录与旧marker的兼容不能通过丢掉实体、回收站或稳定ID实现。

## 本机数据和恢复保护

根路径为 `%LOCALAPPDATA%\EasyShare\cloud-drive\<稳定空间身份>`；写回状态在旁边的 `cfapi-state`，不是云根子目录。身份包含服务器、账号稳定ID及空间，不包含登录令牌。

- `WritebackEngine.Reconcile` 先扫描/写回，再在同一互斥区执行受保护树同步和可选回收。
- 提交前保存不可变快照与pending，版本冲突不覆盖用户内容；首次扫描不能先重建远端树。
- 已有普通目录只有在明确授权、ACL、NTFS身份和远端目录来源都匹配时才解除该目录保护；授权JSON不是密码学签名，同账号写入者属于信任边界。
- 正常断连不是注销。只有确认归属的隔离测试根在成功清理后显式注销；生产旧损坏项需要独立恢复方案。
- 悬浮窗WebView2外部drop必须开启；附加File对象取真实路径，上传状态通过窗口消息回到COM创建线程。

## 构建与普通回归

```powershell
npm ci --prefix frontend
npm --prefix frontend run build
go test -p 1 ./...
go vet . ./internal/cfapi ./internal/cfmount ./internal/drive ./internal/namespace
npm --prefix frontend test
```

`main.go`嵌入 `frontend/dist`，干净检出必须先生成前端资源。Core和桌面壳均需构建；不要在运行EXE的目录直接覆盖构建。Go/Wails版本按go.mod，不额外升级依赖。

## 可复现的隔离活栈验收

这些测试会真实写服务器，但只使用随机前缀、独立provider/namespace和本检出下 `.rd-test` 新目录。推荐独立测试账号；不得把任意生产云根当测试落点。默认全关闭。

```powershell
$env:EASYSHARE_LIVE_BASE='https://<测试控制面>'
$env:EASYSHARE_LIVE_USER_A='<测试账号>'
$env:EASYSHARE_LIVE_PASS_A='<通过本机安全方式注入的口令>'
$env:EASYSHARE_LIVE_CFAPI='1'
$env:EASYSHARE_LIVE_DRIVE='1'
$env:EASYSHARE_LIVE_CACHE='1'
go test . -run '^TestLiveNativeCFAPICacheIsolated$' -count=1 -v -timeout=15m

# 真实鼠标：需保持已解锁交互桌面；测试期间不要争用鼠标/键盘
$env:EASYSHARE_LIVE_CACHE_GUI='1'
go test . -run '^TestLiveCacheThisPCIsolated$' -count=1 -v -timeout=15m
```

安全运行器也可通过 `EASYSHARE_LIVE_TOKEN` 注入现有测试会话；测试用SessionInfo验证它，不自行读取AppData凭据，不在命令/日志写令牌。父测试重启子进程沿用内存环境中的会话，避免反复登录触发服务器会话限制。运行后清除本进程测试环境变量，不将凭据写进脚本或仓库。

`.rd-test`及祖先必须不是重解析/生产同步根。GUI辅助脚本校验：本检出路径、随机run目录、精确root、随机CLSID、PID/窗口命中和菜单归属。失败保留数据/证据，不用递归通配删除；恢复清理须逐项核对nonce、fileId、版本、哈希和未上传状态。

## 真机验收清单

1. 从“此电脑”真实双击个人云盘磁贴，而不是只打开背后目录。
2. 在**根目录**右键新建空文本，编辑保存、复制粘贴、实际拖入，并独立从服务器全文回读。
3. 验证未上传/冲突/占用正文不回收；已确认正文空闲后自动回收、重开可读；复制出网盘副本独立。
4. 隐藏图标区托盘打开/正常退出；悬浮窗固定、当前空间状态、打开同一入口、真实Explorer拖入产生cloud_upload任务。
5. 正常重启，不重输有效账号，确认主窗状态与入口一致，再从“此电脑”点击回读。
6. 同时核对原文件身份/版本/哈希保留；只清理本次已证明归属的短名称测试项。

当前公开仓库只保存可复用测试、协议和脱敏结论；PID、私有授权、登录状态、用户文件、实际机器路径和原始证据留本机，不是缺失的产品源码。
