# 全局快捷搜索 — Everything 式第二面板表面（用户点名功能）

- 日期：2026-09-06（部署缺口评估拍板三项之一；同日第四片）
- 状态：**已完成**（go build/test 全绿 + 知识服务 144 绿；真机热键/搜索冒烟随下轮打包）

## 1. 交互形态

`Alt+Space` 全局呼出搜索小窗（480×560，贴光标弹出）→ 输入即搜（220ms 防抖）→
结果分组（**知识**片段 / **文件**条目）→ `↑↓` 选择、`Enter` 打开或复制、`Esc` 收起。
底部显示实际生效热键。热键回退链：Alt+Space → Ctrl+Alt+Space → Win+Alt+Space
（Alt+Space 是 Windows 窗口菜单键——Everything/uTools 的既定惯例，呼出价值大于
菜单键；被占自动回退并展示，不静默）。

动作语义：文件条目 Enter = 签发下载链接交系统浏览器打开；知识片段 Enter =
片段正文写入系统剪切板（回原窗口直接粘贴）。

## 2. 架构：面板多表面化

快捷面板基建（独立线程 Win32+WebView2 窗口、全局热键、loopback 静态服务、
`__eshare` 信封 RPC）从"剪切板专用"泛化为**多表面**：

- `clipPanel.kind`（clip/search）+ `panelSurfaceSpec`：窗口标题/尺寸/URL/热键链
  差异全部收拢在 spec；panelInstances 按 kind 寻址，stopPanel/stopSearchPanel
  互不误伤；两表面独立存活守卫。
- 搜索表面不随插件启停（剪切板面板随插件在场；搜索是宿主自有表面，启动即注册）。
- **host.\* 信封分支**（panel_surface.go）：`host.search / host.open / host.copy`
  是面板表面专用宿主能力，不经插件权限体系——面板窗口本就是宿主表面
  （loopback + 本机边界），与剪切板面板同级信任；页面是 go:embed 自包含 HTML
  （`/search` 路由，`search_page.html`），裸 postMessage 桥不走 SDK 插件身份。
- **搜索 RPC 异步化**：host.search 在 goroutine 执行、经独立的 `searchEmit`
  Eval 通道回投——知识检索+网盘列举是慢 IO，同步会卡死面板线程消息循环
  （检索期间窗口无法隐藏/重绘）。检索期间被收起则静默丢弃。
- **搜索路由走 Core 网关与登录态**：知识路 `KnowledgeSearch`（`/query` 新增
  `mode=search` 仅检索跳过生成——快搜即时响应，也不烧 LLM token）；文件路
  `drive.Objects`（个人+共享）按名过滤（观察期数据量客户端过滤足够，量大后
  服务端化）。任一路失败不影响另一路（宁可少一半结果不整页报错）。

## 3. 改动清单

| 位置 | 内容 |
| --- | --- |
| `search_surface.go` + `search_page.html`（新） | 搜索表面页（go:embed）+ host.search 异步聚合 + host.open/copy 动作 |
| `panel_windows.go` | 多表面化：kind/spec/存活守卫分离/URL 与热键链按 kind/evalScript；startSearchPanel（Win 实现） |
| `panel_darwin.go` / `panel_other.go` | darwin 暂 no-op（NSPanel 待真机批次），其余平台 no-op |
| `panel_surface.go` | searchURL 装配、ensurePanelServer 共用、host.* 信封分支 |
| `app.go` / `appplugin.go` | /search 路由注册、searchURL 字段、启动即拉起搜索面板 |
| `internal/knowledge/client.go` + `internal/api/knowledge.go` + `internal/desktop/client.go` | `mode=search` 全链路（仅检索，20s 短超时） |
| `knowledge/app/api/schemas.py + routes.py` | QueryRequest.mode=search：跳过生成与生成日志 |

## 4. 验证与遗留

- go build/test 全绿；知识服务 144 全绿（mode 为加法改动）。
- 真机冒烟（热键呼出/输入即搜/Enter 动作/Esc）随下轮打包一并做；
  darwin 侧搜索面板随 NSPanel 真机批次。
- 文件搜索当前是客户端全量过滤，文档过万后需控制面加名称索引检索（记入 P1 候选）。
