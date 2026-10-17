# 异步取数：未完成的实验（已从构建中移出）

## 为什么放在这里

`internal/cfapi` 的**同步**取数已可用且有真机验收（见上级 README）。
本目录是**异步取数**的未完成尝试，`*.go.txt` 后缀使其不参与构建。

## 动机（明确且成立）

同步模型把整个取数过程占在系统回调线程上。小文件无感，但真实云端拉 GB 级文件时，
回调线程被占住的时长可能超过系统容忍上限 → I/O 超时失败。而大文件恰恰是占位符
最大的受益场景。WPS 走的是异步（`cfApiFetchDataCallback` 只造 JobContext 并用
Qt 队列派发，立刻返回，取证 §2.1b (4)）。

## 已经跑通的部分

**异步+慢取数（3 秒）能成功读取**：

```
TestAsyncFetchSlow PASS
    慢取数（3s）下读取成功：受理于 03:13:29.822，总耗时 3.019s，内容匹配
```

即「回调立刻返回、后台取数、再回填」这条主链路是通的。

## 卡住的地方（**下一个接手的人从这里开始**）

两处失败，都指向同一个根因：

| 用例 | 现象 | 诊断 |
| --- | --- | --- |
| 多块回填 | 读失败 `0x8007017C`（ERROR_CLOUD_FILE_INVALID_REQUEST） | 第一块 `CfExecute` 就被拒 |
| 错误上报 | 读取**永久挂起** | 错误也没能回填进去 |

**根因**：CfApi 规定——**回调返回之后**再调 `CfExecute(TRANSFER_DATA)`，
必须先持有该文件的**受保护句柄**（`CfOpenFileWithOplock` 取得）。
这解释了为什么同步模型能跑（在回调内，回调栈本身算作持有）、异步模型不能跑。

## 走过的两条错路（省得重走）

1. **直接用回调里的 key 回填**（不取句柄）→ `0x8007017C`。
2. **在回调里 `CfOpenFileWithOplock` 取句柄、回调返回后再用** →
   **读取永久挂起**。因为 oplock 会阻塞那个正要触发 FETCH_DATA 的读取本身，
   形成死锁（读取等 oplock 释放，oplock 等读取完成）。

## 一个被**证伪**的假说（留着省得重走）

看到 `TestAsyncFetchSlow`（延迟 3 秒）成功、而「立刻回填」的用例失败时，
我提出假说：**「回调返回到 CfExecute 的间隔太短」导致失败**。

为此写了时序探针（单块回填，分别延迟 0 / 50ms / 500ms）：

```
--- FAIL: TestProbeDelay
    --- FAIL: TestProbeDelay/0s   (15.02s) 挂起
    --- FAIL: TestProbeDelay/50ms (15.03s) 挂起
    --- FAIL: TestProbeDelay/500ms(15.01s) 挂起
```

**三个延迟全部挂起——假说被证伪。** 500ms 也失败，说明不是「太快」的问题。

那 `TestAsyncFetchSlow` 为什么成功？现在只能给出更保守的解释：
它与探针的差别不只是延迟，还有**数据量与时序细节**（Slow 是单块小数据、
探针也是；但 Slow 的成功可能带有偶然性——**该用例只跑过一次，没有重复验证**）。
**结论：目前对异步失败原因的把握不足**，不要采信上述任何解释，
从「哪些条件下能稳定成功」重新做实验设计。

## 一个必须知道的运行期危害：**挂起的测试进程杀不掉**

跑超时用例时出现过：用例超时后 `go test` 报了 panic，但**测试进程仍驻留**
（`cfapi.test.exe`，9.8 MB 常驻），并且：

- `Stop-Process -Force`、`taskkill /F`、`TerminateProcess` **全部失败**
  （`TerminateProcess` 返回 Win32 错误 5 = 拒绝访问）；
- 它占着 `%TEMP%\TestAsyncFetchSlow*\001\slow.txt`，导致临时目录删不掉；
- `tasklist` 显示进程在，但 PIDs 与句柄状态自相矛盾（`Get-Process` 时而查得到时而查不到）。

**这不是清理不便，是设计信号**：进程卡在**内核态的 CfApi 调用**里
（`syscall.readFile` 的栈帧可证，见超时 panic 的 goroutine dump），
这种状态下的线程连强制终止都被拒——说明它正等在一个我们无法从用户态打断的
等待上。

**对实现的两个直接要求**：
1. **绝不能把不可控时长的等待放进 CfApi 回调链**。同步模型的前提是
   「取数一定会很快返回」，而真实云端不保证这一点；
2. **测试必须带超时并且超时后能自清**。当前 `TestAsyncFetch*` 用例在设计上
   做不到（卡在内核调用时 defer/cleanup 都不会跑），所以它们**不应进入常规 CI**——
   这也是把它们隔离到本目录的原因之一。

> 复现痕迹：`tasklist /FI "IMAGENAME eq cfapi.test.exe"`。

## 下一步该试什么

**先补实验**：把「什么条件下异步能稳定成功」测清楚（重复跑、控制变量），
不要基于单次成功的用例推断机制。

然后才试：

1. **回调内** `CfOpenFileWithOplock` 取受保护句柄；
2. **回调内**再 `CfReferenceProtectedHandle(handle)`（返回 BOOLEAN，语义是
   「声明还有一个引用要用，别在我用之前关掉」）——这一步是错路 2 缺的；
3. 后台线程 `CfGetWin32HandleFromProtectedHandle(handle)` 取真正的句柄做 I/O；
4. 完成后 `CfReleaseProtectedHandle(handle)` + `CfCloseHandle(handle)`；
5. **重点验证死锁是否复现**：如果仍死锁，说明 oplock 与「触发本次读取的句柄」
   存在冲突，需要换一种取句柄的时机（例如在**建占位符时**就先备好句柄，
   而不是等到 FETCH_DATA 才取）。

## 更稳的备选（推荐先做这个）

**保持同步取数，但把粒度改小**：按 `RequiredLength` 分片，让每次回调只负责
一小块（例如 4 MiB，对齐钉盘取证 §3.1.1 实测的 `partSizeOfClient`）。
这样单次回调占用时间被压到可预期范围，不引入跨线程时序问题。

这条路**不改变模型、只改变粒度**，风险低得多，而且顺带把「大文件一次性读进内存」
这个老问题也解决了（本仓 WebDAV 路径的已知缺点）。

## 结论

**在明确「oplock + CfReferenceProtectedHandle」的正确用法之前，不要合入本目录。**
同步版本已在真机验证可用，是当前应当发布的状态；本目录只在确认异步方案可行后
才值得继续投入。
