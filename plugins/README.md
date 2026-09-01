# EasyShare 插件工程

官方插件的源码与开发工作流。**本目录自包含**：不依赖主仓库的 Go/前端代码，只依赖
两个稳定契约——插件包规范（manifest.json + 静态 Web 资源）与宿主能力 API
（SDK 经 `/plugins/_sdk/eshare.js` 统一分发）。将来可直接把本目录拆为独立仓库
（`git subtree split -P plugins` 或整目录搬走），主程序仓库零感知。

## 目录结构

```
plugins/
├── README.md          本文档：规范、调试、发布
├── dev.ps1            开发安装辅助（junction 热调回路）
├── template/          新插件最小模板（复制改名即起步）
├── todo/              待办与周报（首个商城插件）
└── <your-plugin>/     每个插件一个子目录，目录名建议 = 插件 ID
```

## 插件包规范

一个插件 = 一个子目录，打成 zip 后上架商城，客户端解压到
`%LOCALAPPDATA%\EasyShare\plugins\{id}\` 运行：

```
<plugin-id>/
├── manifest.json    # 必须在包根
├── index.html       # 入口（manifest.entry 可改）
├── app.js
└── style.css
```

### manifest.json 字段

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | ✅ | 全局唯一：小写字母开头，仅小写字母/数字/连字符，2~32 位；内置插件 ID（如 `clipboard`）不可占用 |
| `name` | ✅ | 显示名 |
| `version` | ✅ | 语义化版本（如 1.0.0）；商城按发布时间取最新，客户端按 semver 判更新 |
| `description` |  | 一句话说明（商城卡片） |
| `icon` |  | emoji（如 ✅）或包内图标相对路径（如 icon.png），空则默认 🧩 |
| `entry` |  | 入口 HTML 文件名，默认 index.html |
| `author` |  | 作者 |
| `permissions` |  | 权限声明数组，见下表；安装/升级时逐项向用户确认 |

### 权限清单（声明了才能调对应能力 API）

| 权限 | 中文（确认框展示） | 解锁的 SDK 能力 |
| --- | --- | --- |
| `storage` | 本地数据存储 | `eshare.storage.*`（按插件隔离的 KV，卸载即清除） |
| `clipboard.read` | 读取剪切板历史 | `eshare.clipboard.history/remove/clear/settings` |
| `clipboard.write` | 写入剪切板 | `eshare.clipboard.copyText/copyImage/copyFiles` |
| `clipboard.events` | 剪切板变化通知 | `eshare.clipboard.onChanged(cb)` |
| `notification` | 系统通知 | `eshare.notify(title, body)` |
| `drive.upload` | 上传到个人云盘 | `eshare.uploadToDrive({filename, content})`（走统一上传任务，进度在活动中心） |

### 运行环境与限制

- UI 跑在 `<iframe sandbox="allow-scripts">`（opaque origin）：**没有** DOM storage、
  不能 fetch 宿主 API、不能弹窗——与宿主唯一通道是 SDK 的 postMessage RPC。
- `<img src="/clipboard-files/{hash}.png">` 这类**同源子资源**可正常加载。
- 静态资源响应 `no-store`，改包重装后无需担心缓存。
- 包大小上限 50MB；解压总量 120MB / 2000 文件 / 单文件 64MB（防压缩炸弹）。

## SDK 速查

插件 HTML 内引用（宿主统一分发，勿自带副本——版本随宿主）：

```html
<script src="/plugins/_sdk/eshare.js"></script>
```

```js
await eshare.storage.set('todos', [...])        // KV：string key，任意 JSON 值
const list = await eshare.storage.get('todos')
await eshare.clipboard.copyText('周报内容...')    // 权限 clipboard.write
const entries = await eshare.clipboard.history({ limit: 60, offset: 0, kind: '', query: '' })
eshare.clipboard.onChanged(entry => { ... })     // 权限 clipboard.events
await eshare.notify('周报已生成', '已复制到剪切板')
await eshare.uploadToDrive({ filename: '周报.md', content: '...' })
// 所有调用返回 Promise；未授权/未知能力 reject（Error.message 为中文原因）
```

## 开发调试回路

桌面端从安装目录加载插件（`%LOCALAPPDATA%\EasyShare\plugins\{id}\`），开发时用
**目录联接（junction）**把开发目录映射过去，改文件即时生效（响应 no-store，
插件页切走再切回即重载，无需重启应用）：

```powershell
# 安装开发映射（以 todo 为例；也可以直接 go run ./plugins/dev -plugin todo）
powershell -ExecutionPolicy Bypass -File plugins\dev.ps1 -Plugin todo
# 移除映射
powershell -ExecutionPolicy Bypass -File plugins\dev.ps1 -Plugin todo -Remove
```

映射建立时会同步把插件登记进 `plugins.json`（宿主按登记表决定可见性与权限快照）。
已从商城正式安装的同名插件会被开发映射顶替——先在插件中心卸载。

> 实现说明：dev 工具的逻辑在 `plugins/dev/main.go`（Go），`dev.ps1` 只是薄壳。
> 原因：Windows PowerShell 5.1 在本仓库 plugins\ 目录以 `-File` 执行较大脚本时，
> param 块会被解析丢弃（同字节文件在 Temp 目录正常、最小脚本正常，与内容语义
> 无关，疑为宿主解析怪病或安全软件按路径介入脚本流），故逻辑不落在 .ps1 里。

纯浏览器调试（不接宿主能力，只调 UI 布局）可直接开本地静态服务器看 index.html
（SDK 调用会失败，属预期）。

## 发布上架

控制面（`deploy/ruoyi-db/` 的 RuoYi + RustFS）运行中时，从仓库根执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/publish-plugin.ps1 -PluginDir plugins\todo `
    -Notes "本次更新说明"
```

流程：读 manifest → 打 zip → 登录 → 预签名直传 RustFS → 发布校验 → 匿名验证。
同版本重传 = 覆盖发布；版本号递增 = 新版成为 latest，客户端启动检查发现后亮
「插件中心」红点，用户确认权限变更后完成升级。

## 新插件起步

```powershell
Copy-Item -Recurse plugins\template plugins\my-plugin
# 改 manifest.json 的 id/name/version/permissions，改 index.html 文案
powershell -ExecutionPolicy Bypass -File plugins\dev.ps1 -Plugin my-plugin
```

## 迁移为独立仓库时

- 本目录整体自包含，直接搬走即可；主仓库只通过**发布产物**（zip）与插件交互。
- `scripts/publish-plugin.ps1` 随主仓（它依赖控制面凭据约定），迁移后可复制一份带走。
- SDK 源在主仓 `assets/sdk/eshare.js`（宿主内嵌分发）；独立仓库如需 SDK 文档，
  以本 README 的速查为准，勿在插件包内携带 SDK 副本（避免宿主/插件版本漂移）。
