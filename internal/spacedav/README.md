# internal/spacedav — 空间 WebDAV 端点

把一个「空间」（个人 / 共享）通过 WebDAV 暴露给 Windows 资源管理器与 macOS Finder。
前身是建在 `objectstore.Store` 上、用编译期静态凭据直连 RustFS 的
`internal/cloud/webdavfs`（KI-2，已随 KI-5 清理删除）。本包改为建在 `internal/drive`
客户端之上，**每个操作都经控制面**，因此客户端不持任何对象存储凭据，
且配额与共享空间授权对资源管理器同样生效。

生产装配在 `spacemount.go`（`drive.New(base)` + `spacedav.New`）。

## 已修复的三个高危缺陷（都值得记住教训）

| 编号 | 现象 | 根因 |
| --- | --- | --- |
| [KI-9](../../docs/known-issues.md) | **双击任何文件都打不开**（GET 一律 500） | `readFile.Seek` 漏掉 `http.ServeContent` 必需的 `Seek(0, SeekStart)`——它的调用顺序是「先 Read(512) 探类型、再回退重放」，与本能的假设相反 |
| [KI-12](../../docs/known-issues.md) | 资源管理器**改名会换掉 fileId** 并留回收站副本 | `Rename` 是「下载+上传+删除」模拟实现；软删改造后第二步语义变了（空间 ×2），且上传到新键必然换 fileId |
| [KI-13](../../docs/known-issues.md) | 拖大文件进来让 Core **吃掉等量内存**（5 GB 文件 = 5 GB 分配） | 写路径用 `bytes.Buffer` 攒整份内容；注释里「需要控制面先支持 multipart」的理由**早已失效** |

三条都是**测试全绿时存在**的：它们不在「已写下的判据」范围内。

## 写路径：落盘暂存（KI-13）

`writeFile` 把 PUT 的内容**暂存到磁盘**，`Close` 时：

1. `Sync()` 刷盘；
2. 交给可选能力 `fileUploader`（即 `*drive.Client.UploadFile`）按**本地路径**上传
   → 走 Upload Session 分片 + 断点续传，字节不经过本包内存；
3. 客户端不支持该能力时退回单请求 PUT，但也是**从磁盘流式读**；
4. **无论成败都删暂存文件**（`defer`）。

### 为什么必须先落到某处

配额在签发预签名 URL 时判定，而判定需要**确切字节数**；流式上传拿不到总长，
只能上报 0，配额就等于失效。落盘才能既拿到真实大小、又不吃掉等量内存。

### 暂存目录（`Options.SpoolDir`）

生产指向数据根 `%LOCALAPPDATA%\EasyShare\upload-spool\`（与 `config.json` 同级），
**刻意不用系统临时目录**：拖进来的可能是几十 GB，而系统盘通常最小；
放数据根，用户至少有「清理数据盘」这一个直觉可循。留空则退回 `os.TempDir()`（测试用）。

### 已知缺陷：孤儿暂存文件

**进程被强杀**（任务管理器结束进程、断电、崩溃）时，`defer` 不会执行，
`upload-spool/` 会残留 `easyshare-upload-*.tmp`。每个残留白占一份磁盘，
而用户看不到提示。

**当前未做清理**。低成本加固：启动时（或首次挂载时）扫一遍该目录，
删除已无对应 in-flight 上传的 `.tmp`。判断依据需要小心——正在上传时也有 `.tmp`，
直接全删会打断进行中的上传，所以要么按 mtime 加阈值（如早于 1 小时视为孤儿），
要么记录进程内的活动句柄再比对。

**未做流式分片**：暂存与最终对象各占一份，峰值是文件大小的**两倍**。
真正的解法是「边收边传」，需要 WebDAV 层拿到 `Content-Length`
（`x/net/webdav` 的 `handlePut` 不透出该值），属后续切片。

## 读路径：`Seek` 的契约（KI-9）

`readFile.Seek` 支持 `http.ServeContent` 实际会用的那几种调用：

```
Read(512)              ← 探测 Content-Type，**先消费数据**
Seek(0, io.SeekStart)  ← "rewind to output whole file"，**回退重放**
Seek(0, io.SeekEnd)    ← 取大小
Seek(ra.start, ...)    ← Range 时定位
```

顺序与直觉相反：**先读、后回退**。`(0, SeekStart)` 的关键动作是
**重开一条从 0 起的流**（远端流不能倒带），这依赖可选能力 `openRangeer`。

## 改名/移动：优先服务端（KI-12）

`Rename` 优先走可选能力 `mover`（`POST /easyshare/drive/move`，
服务端 `CopyObject` + 删源）：

- 字节不出对象存储（旧实现要下载 + 上传一遍）；
- **fileId 不变**——分享链接、任务引用、知识索引绑的都是它；
- 失败时控制面侧有补偿（行挪回 + 清副本），见 `DriveMoveService`。

目录改名仍**明确拒绝**（`os.ErrPermission`）：目录在对象存储里只是前缀，
改名会放大成 N 组复制删除，中途失败留半改状态。需要真正的目录对象模型。

## 防止「静默退化」的守卫

本包有三处「可选能力」，缺了不会报错，只会**悄悄换回坏路径**。各自有守卫：

| 可选能力 | 缺了会怎样 | 守卫形式 |
| --- | --- | --- |
| `openRangeer` | 下载全挂（KI-9 复发） | `contract_test.go` 的 `var _ openRangeer = c`（**函数内**，随测试执行；外部另见 `TestProductionWiringCanSeek` 走真实 HTTP） |
| `mover` | 退回搬字节的改名（KI-12 复发） | `move_contract_test.go` 的**包级** `var _ mover = (*drive.Client)(nil)`（编译期） |
| `fileUploader` | 大文件退回单请求 PUT（KI-13 复发） | `upload_spool_test.go` 的**包级** `var _ fileUploader = (*drive.Client)(nil)`（编译期） |

> **为什么 `openRangeer` 那条不在包级**：它写成了 `contract_test.go` 里的函数局部断言
> （`var _ openRangeer = c`，`c` 是 `drive.New(...)` 的返回值）。功能上等价——
> 不满足则**测试编译/执行失败**——只是形态不一致。真正兜住 KI-9 的是
> `wiring_contract_test.go` 的 `TestProductionWiringCanSeek`：它用生产装配发一次
> 真实 HTTP GET，端到端验证「文件打得开」。（2026-10-16 核对时更正过本文件
> 先前「三处都是包级编译期断言」的说法。）

## 测试

```bash
go test ./internal/spacedav/ -v
```

- `*_contract_test.go`：走**真实 `webdav.Handler`** 的端到端用例
  （PUT/GET/PROPFIND/Range/改名），而非只测 FS 层方法——
  KI-9 正是因为「没有一个用例发过 HTTP 请求」才藏了那么久；
- `wiring_contract_test.go`：用**生产装配方式**（真实 `*drive.Client` +
  httptest 假控制面）跑，钉住运行期类型断言；
- `upload_spool_test.go`：大文件内存断言 + 暂存清理（含失败路径）。
