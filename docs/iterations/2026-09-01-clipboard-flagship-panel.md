# 剪切板旗舰插件 + 全局快捷面板（Win+V / ⌘⇧V）

## 用户问题

- 内置剪切板插件 UI 是首版（平列表），与产品「macOS 简约视觉」的审美标准有差距；用户提供了成熟剪切板管理器（按天分组卡片 + 分类侧栏 + 收藏 + 搜索）作为参考。
- 剪切板这类高频工具要像 Windows 系统 Win+V 一样**全局热键即唤即用**，而不是每次打开主窗口找标签页。
- 插件代码要统一进 `plugins/` 插件工程管理，而不是散落在主仓 assets 里。
- 双平台：Windows 与 macOS 都要可用。

## 目标

- **插件本体**：剪切板插件全面重构（`plugins/clipboard/`，版本 2.0.0）——按天分组卡片、收藏（星标，插件 KV 持久化）、分类（文字/图片/链接/文件）、搜索、统计、明暗双主题、实时追加；同一份代码支持 `?panel=1` 紧凑面板形态（搜索 + 键盘导航 ↑↓/Enter/Esc）。
- **快捷面板（宿主新表面）**：全局热键唤起独立小窗（Windows：Win32 + WebView2，复用托盘悬浮窗模式；macOS：NSPanel + WKWebView），加载插件面板页；点击条目 = 复制 + 面板收起 + **自动粘贴到之前的Focus窗口**（Win+V 语义）；失焦/Esc/再按热键收起。
- **插件工程归属**：内置剪切板插件源码迁入 `plugins/clipboard/`（用户指定「代码做到插件仓库里」），主仓 embed 直读该目录；`assets/builtin-plugins/` 退役。
- **macOS 剪切板监听**（批次 3 项）：NSPasteboard changeCount 轮询，文本/图片/文件三类，补齐 darwin 能力。

## 非目标

- 热键自定义设置界面（v1 固定 Win+V，被系统/他方占用时自动回退 Win+Shift+V；mac 固定 ⌘⇧V）。
- 面板设置项（自动粘贴开关等）——v1 恒开自动粘贴，语义即 Win+V。
- 插件在面板中的多实例/多插件面板调度（v1 面板固定服务 `clipboard` 插件，留 `startPanel` 接缝）。
- 商城上架本插件（内置身份不变：不可卸载、随宿主分发；商城装不了内置 ID 是既有保护）。

## 设计决策

- **旗舰插件 = 内置插件的 2.0.0，而非第二个商城插件**。理由：①「剪切板内置永不消失」是插件系统立项时用户定的铁律，内置身份（不可卸载/禁用）只在随宿主分发时成立；②商城路径禁止覆盖内置 ID（`install.go` 显式拒绝），做成商城插件只能换 ID，出现两个剪切板 UI 并存，产品上是噪音；③源码归位 `plugins/clipboard/` 已满足「插件仓统一管理」，分发身份与源码位置是两件事。
- **主仓 embed 直读 `plugins/clipboard`**（`//go:embed all:plugins/clipboard` + `EnsureBuiltinFS(id, fsys)`），不做 assets 拷贝同步——单一事实源，无漂移风险。代价：主仓对 plugins/ 出现一处代码级引用，违反拆分计划「零依赖」前提——已在拆分计划文档补记例外与拆分日处置（拆分时该目录回搬 assets/ 或随内置留在主仓，一次性 cp）。
- **面板 = 宿主新表面，不是插件能力**。全局热键、独立窗口、自动粘贴都是宿主职责；插件只提供「响应 ?panel=1 的紧凑 UI」+ 复制动作。复制动作沿用既有 `clipboard.write` 能力，**面板运行时约定：面板内插件的成功 clipboard.write = 用户选中 = 收起 + 粘贴**，无需新增能力 API。
- **面板静态资源 = 桌面端临时 loopback 监听**（`127.0.0.1:0`，复用 `a.AssetHandler()` mux）。独立 WebView2/WKWebView 够不到 Wails AssetServer 虚拟主机，而插件页引用 `/plugins/_sdk/eshare.js`、`/clipboard-files/*.png` 等绝对路径——同一 mux 换个 listener 全部兼容，插件零改动。端口随启随关、仅回环。
- **SDK 双通道**（`eshare.js`）：iframe（`window.parent` postMessage，现有）之外增加原生通道（WebView2 `chrome.webview.postMessage` / WKWebView `webkit.messageHandlers.espanel`），协议同构 `{__eshare:1,...}`；Go 侧面板窗直接调 `Manager.InvokeFor`（与主窗 iframe 同一鉴权路径）。新增 `eshare.window.dismiss()/onShown()`（面板运行时专用，iframe 模式 no-op，无权限——它是给插件 UI 的窗口控制，非宿主数据）。
- **Win32 细节**：热键在面板窗线程注册（WM_HOTKEY 与窗口同线程，免跨线程）；面板**要抢焦点**（搜索框即打即搜，与悬浮窗「不抢焦点」相反）；粘贴序列 = 收起 → SetForegroundWindow(之前的焦点窗) → keybd_event Ctrl+V（粘贴前校验前台已切回，防粘到自己）；自动粘贴只在「热键唤起前面板外有其他窗口」时执行。macOS 粘贴走 CGEvent（需辅助功能授权，未授权降级为仅复制）。

## 测试计划

- 冲刺期豁免新单测（回归绿 + 构建过 + 冒烟）。
- 冒烟：写剪贴板 → 插件中心页历史出现（full 形态视觉）→ Win+V 面板弹出 → 搜索/↑↓/Enter → 粘贴落进之前的前台应用 → Esc/失焦收起；macOS 侧编译由 CI 把关，运行时行为列真机验收。

## 完成记录

### 已实现（2026-09-01）

**插件本体（`plugins/clipboard/`，v2.0.0，内置身份不变）**

- 全面重构 UI：侧栏（全部/收藏/分类 文字·图片·链接·文件 + 各分类计数 + 本地容量条）+ 按天分组卡片流（今天/昨天/M月D日）+ 搜索（250ms 防抖）+ 暂停/恢复/清空 + 实时新条目插入；深浅双主题跟随系统，macOS 简约视觉
- 收藏：星标存插件私有 KV（`storage` 权限），收藏视图/计数联动；链接是文本条目的客户端再分类（`^https?://\S+$`，与宿主 `clipboard.stats` 同规则）
- `?panel=1` 紧凑面板形态：搜索框（自动聚焦）+ 类型 chips + 速取列表 + 键盘导航（↑↓ 选择 / Enter 复制 / Esc 关闭）+ 图片缩略图行
- manifest 权限新增 `storage`；版本 1.0.0 → 2.0.0

**宿主侧（主仓）**

- 内置插件源码迁入插件工程：`//go:embed all:plugins/clipboard` + `plugin.EnsureBuiltinFS(id, fsys)`（`EnsureBuiltin` 重构为逐目录复用同一逻辑）；`assets/builtin-plugins/` 删除——单一事实源，无同步拷贝。拆分计划补例外记录（见 plans 文档）
- `internal/clipboard`：`AddOnChange` 多订阅（原 SetOnChange 单槽被面板占用会顶掉 Wails 事件）、`Stats()` 分类计数、新能力 `clipboard.stats`
- `assets/sdk/eshare.js` 双通道：iframe（window.parent postMessage，原路径不变）+ 原生面板（WebView2 `chrome.webview.postMessage`，**必须发 JSON 字符串**——go-webview2 的 `TryGetWebMessageAsString` 不收对象，发对象直接 E_INVALIDARG；WKWebView script handler）；新增 `eshare.clipboard.stats`、`eshare.window.dismiss()/onShown()`
- 快捷面板：`panel_surface.go`（消息协议解析/事件脚本/loopback 静态服务——复用 `a.AssetHandler()` mux 起 `127.0.0.1:0`，插件页绝对路径全部兼容）+ `panel_windows.go`（Win32+WebView2，独立线程同悬浮窗模型；热键在面板线程注册；Win+V → Win+Shift+V → Ctrl+Shift+V → Alt+V 回退链）+ `panel_darwin.go/.m/.h`（NSPanel+WKWebView，Carbon RegisterEventHotKey ⌘⇧V，dispatch 主队列模式同托盘）+ `panel_other.go`（no-op）
- 自动粘贴：面板内成功 `clipboard.write` = 选中 → 收起 → 80ms 后 `SetForegroundWindow` 切回唤起前窗口 → **焦点确认切回才注入 Ctrl+V**（切不回宁可不粘）；目标为本进程窗口时跳过
- macOS 剪切板监听（批次 3 项）：`listener_darwin.go` + `clipboard_darwin.m`——NSPasteboard changeCount 800ms 轮询、文件>图片>文本优先级与 Windows 对齐、TIFF→PNG、回写三类型；`listener_other.go` 收窄为非 win/darwin

### 验证结果（2026-09-01，Windows 真机）

- `go build ./...` + `go test ./...` 全绿；`wails build` 通过（无前端改动，无绑定级联）
- **面板端到端（真机 GUI 冒烟全过）**：热键唤起（本机 Win+V 被占自动回退 Ctrl+Shift+V，日志可查）→ 面板贴光标弹出、搜索框自动聚焦 → 实时历史（来源 Weixin.exe）→ Enter 复制 → 面板收起 → 焦点切回记事本 → 自动粘贴落字（截图+日志验证）；自粘贴守卫（prev 为本进程窗口时跳过）验证生效
- 完整形态：loopback 服务页面（无宿主环境）布局渲染正确（无头 Edge 截图）；数据链路与面板共用同一套 RPC（面板内 history/stats 实测有数据）
- 排障记录：① go-webview2 `MessageReceived` 的 `TryGetWebMessageAsString` 只收字符串消息，SDK 原生通道发对象会报「参数不正确」（已改 `JSON.stringify`）；② WebView2 嵌入阶段父窗口零尺寸会报参数错误（建窗即给非零尺寸，DPI 缩放后挪屏外待命）；③ 粘贴目标在定时器回调用完前不得清零（首版 bug，粘贴永远不触发）

### 当日追加：剪切板改为普通可卸载插件（2026-09-01，用户反馈）

用户看到「内置功能，不可卸载」后明确：**插件的核心价值就是可安装可卸载**，剪切板不应例外——推翻了「内置永不消失」的旧约定（那是插件系统立项时的过桥决策）。同屏还抓到一个真 bug：有 6 条记录时空态仍然显示。

- **改造**：`SeedPlugin`（`internal/plugin/install.go`）取代内置分发——首启把 embed 里的插件落成**普通插件**（登记 builtin=false），之后更新走商城（含权限确认）；`plugins-seeded.json` 记录种子标记，**卸载后不复活**（删掉标记里的 ID 可重新种子）。历史 builtin 登记自动降级迁移，迁移那一次若 embed 版本更新则原地重写（兑现老版本「随宿主分发即升级」的承诺，此后交还商城）
- **随插件生死**：录制服务与快捷面板不再无条件启动——`syncClipboardSurface`（`panel_surface.go`）按插件在场+启用状态对齐：卸载/禁用 → 停录制、销毁面板、释放热键；安装/启用 → 反向恢复。剪切板服务 Start/Stop 重构为可重入（lifeMu+running，stopCh 在重启时复位）；面板 startPanel 幂等（panelAlive 原子标记）、stopPanel 三平台实现（win: PostMessage 销毁线程；darwin: easyshare_panel_stop 注销热键收窗）
- **空态 bug**：`.empty { display:flex }` 压过 `hidden` 属性的 UA 默认 `display:none`，导致有记录时空态仍显示——补 `.empty[hidden] { display:none }`
- **发布**：clipboard 2.0.1 已上架商城（publish-plugin.ps1，匿名清单验证通过）——「卸载 → 插件中心重装」全链路可走
- 踩坑记录：Go map 值类型——`e = m.entries[id]` 改副本不写回，降级静默不落盘（首验 registry 仍 builtin=true 即此因）

### 待真机验收（macOS + 交互细节）

- macOS：⌘⇧V 面板、NSPasteboard 记录（文本/图片/文件）、自动粘贴的辅助功能授权流（编译验证靠 CI，运行行为需 mac 真机）
- Windows：图片/文件条目的面板展示与回贴、深色主题、多显示器/高 DPI 下贴光标定位、系统 Win+V 空闲机器上的「顶替系统面板」体验
- 主窗口内完整形态（插件中心页）的登录态视觉验收
