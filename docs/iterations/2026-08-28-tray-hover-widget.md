# 2026-08-28 托盘悬停浮窗（切片 1：悬停链路）

## 用户问题

用户希望获得与本机 WPS 云盘一致的常驻入口体验：鼠标悬停到通知区域图标即弹出一个小浮窗，不必先打开主窗口。当前 EasyShare 的托盘只支持右键菜单，悬停时仅显示系统 tooltip，没有任何富交互面。

更长远的诉求是把这个浮窗做成「文件投递口」：可固定在桌面、可拖入文件、并对投入的文件做自动整理。本迭代只交付其中的第一段——把悬停到弹窗到打开主程序这条链路跑通，为后续固定与拖放留出结构。

## 目标与非目标

### 目标

1. Windows 通知区域图标支持**悬停事件**，悬停即弹出浮窗，移开即收起。
2. 浮窗内容为单条标题栏：左上角项目图标 → 间隔 → 「私人云盘」文字 → 右上角设置图标 → 头像（占位）。
3. 点击设置图标等效于双击 `easyshare.exe`：显示主窗口（进程已在运行时复用现有窗口，不重复启动）。
4. 浮窗定位跟随通知区域图标的实际屏幕位置，支持任务栏在不同边缘和多显示器/高 DPI 场景。
5. 保留现有托盘右键菜单的全部能力（打开主窗口、服务状态、退出）与 `app.go` 既有生命周期契约。
6. 窗口与消息层结构按「后续要能固定、要能接收文件拖放」设计，避免切片 2/3 推翻重做。

### 非目标

- **不做**浮窗固定/钉住（切片 2）。
- **不做**文件拖入浮窗与上传（切片 3）。
- **不做**自动整理算法（切片 4）。
- **不做**登录、账号与真实头像——头像是纯占位图形，不请求任何用户数据。
- **不做** macOS 对等实现。macOS 的 `NSStatusItem` 悬停语义与 Windows 不同，另起迭代。
- **不改**前端 Vite 构建配置，不引入多 HTML 入口，不引入 vue-router。
- **不改**主窗口的尺寸、定位与显示逻辑。

### 验收标准

1. 悬停通知区域图标 → 浮窗在图标附近弹出，无可见闪烁，不抢焦点。
2. 鼠标移开图标且未进入浮窗 → 浮窗自动收起。
3. 鼠标从图标移入浮窗 → 浮窗保持显示，可点击其中按钮。
4. 点击浮窗设置图标 → 主窗口显示并取得前台；主窗口此前处于隐藏或最小化时同样生效。
5. 右键图标 → 菜单仍含「打开主窗口 / 服务状态：运行中|已停止 / 退出 EasyShare」，各项行为与本迭代前一致。
6. 左键单击图标 → 显示主窗口。
7. 「退出 EasyShare」→ 通知区域图标立即消失，浮窗销毁，Core 与桌面进程按既有顺序退出，无残留进程。
8. 任务栏置于屏幕上/左/右边缘时，浮窗仍贴合图标且完全落在屏幕可见区域内。
9. 150% 缩放下浮窗尺寸与文字比例正常，不模糊、不裁切。
10. `go test ./...`、`npm --prefix frontend test`、`scripts/build.ps1` 全部通过；`tray_platform_test.go` 的两条架构守卫测试仍通过。

## 技术决策

### 1. 用原生 `Shell_NotifyIcon` 替换 `getlantern/systray`（仅 Windows）

`getlantern/systray` **不具备**悬停能力，且无法通过配置获得：其 `systray_windows.go` 的 `wndProc` 只处理 `WM_RBUTTONUP` 与 `WM_LBUTTONUP`，两者都直接调 `showMenu()`；库内唯一与悬停相关的 API 是 `SetTooltip`，走系统标准 tooltip，不提供回调。

Windows 为此场景提供了专门机制：把通知图标声明为 `NOTIFYICON_VERSION_4`（`Shell_NotifyIcon` 的 `NIM_SETVERSION`）后，鼠标悬停会向回调窗口投递 `NIN_POPUPOPEN`，移开时投递 `NIN_POPUPCLOSE`，语义正是「用富浮窗替代文本 tooltip」。同时 `Shell_NotifyIconGetRect` 可取得图标的实际屏幕矩形，用于浮窗定位——这比自行推算任务栏位置可靠，且天然适配多显示器与任务栏停靠边缘。

因此 Windows 侧自建通知图标：一个 message-only 窗口 + 自有 `WndProc`，承担图标注册、悬停事件、左键激活和右键菜单（`TrackPopupMenu`）。代价是需要自行实现原本由库提供的菜单，收益是获得悬停能力并移除一个直接依赖。

项目已有手写 Win32 的先例与风格（`LazyDLL` + `NewProc` + `.Call`）：`internal/fsutil/fsutil_windows.go:11-15`（kernel32）、`internal/namespace/namespace_windows.go:27`（shell32）。本迭代首次引入 `user32.dll`，遵循同一风格。

### 2. 保持 `startTray(app *App)` 签名不变

`main.go:37` 是 `startTray` 的唯一调用点。新实现沿用相同签名，并继续使用 `app.trayOnce` 去重、`app.trayStatusCh` 接收状态、回调 `app.trayReady()` / `app.showWindow()` / `app.quitFromTray()`。因此 `main.go` 与 `app.go` **零改动**，替换范围收敛在 `tray_windows.go` 及新增文件内。

### 3. 浮窗用独立 Win32 窗口 + 嵌入 WebView2，不复用主窗口

Wails v2 是单窗口架构：`frontend/wailsjs/runtime/runtime.d.ts` 中所有 `Window*` 函数都作用于唯一窗口，运行时**不存在**创建窗口的 API。可选路径有三条：

| 方案 | 取舍 |
| --- | --- |
| 复用主窗口变形 | 代码最少，但浮窗与主窗口互斥；主窗口开着时悬停会把它抽走，且「点设置打开主窗口」退化为窗口变形动画 |
| 原生 GDI 绘制 | 最轻，但圆角、阴影、字体排版全部手写，样式迭代成本高 |
| **独立窗口 + WebView2** | 样式可与现有 Vue 界面统一，且天然支持后续切片的拖放与富布局；代价是引入 WebView2 嵌入代码 |

选第三条。项目已间接依赖 `github.com/wailsapp/go-webview2 v1.0.22`，其 `pkg/edge` 提供 `NewChromium()` / `Embed(hwnd)` / `NavigateToString(html)` / `MessageCallback`，正好覆盖「把 WebView2 嵌入自建窗口并双向通信」。本迭代将其提升为直接依赖。

关键约束：`Chromium.DataPath` **留空**。留空时它取 `%AppData%/<exe 名>`，与 Wails 主窗口的 WebView2 用户数据目录一致；显式指定不同目录会导致同进程内两个 WebView2 环境的选项冲突而创建失败。

### 4. 浮窗 HTML 内联，不接入 Vite 构建

浮窗只有一条标题栏，通过 `NavigateToString` 直接加载内联 HTML 字符串即可，无需资源服务器。这样 `frontend/vite.config.ts` 不必增加 `rollupOptions.input` 多入口，`frontend/dist` 仍是单入口产物，前端构建与测试链路不受影响。

图标使用内联 SVG，与 `frontend/src/App.vue` 现有图标风格保持一致（同为线性描边 24×24 viewBox），配色取自 `frontend/src/style.css` 的既有 token。

### 5. 悬停收起用「图标与浮窗合并判定」而非单一 `NIN_POPUPCLOSE`

只依赖 `NIN_POPUPCLOSE` 会导致鼠标从图标移向浮窗的途中浮窗被收起，无法点击其中按钮。因此收起判定为：`NIN_POPUPCLOSE` 到达后启动短延迟，延迟内若光标进入浮窗矩形则取消收起；浮窗内 `mouseleave` 再次触发延迟收起。该结构同时是切片 2「固定」的挂载点——固定态只需短路整个收起路径。

### 6. WebView2 不可用时降级而非崩溃

WebView2 Runtime 缺失或环境创建失败时，浮窗创建失败不得影响托盘图标与主程序。失败时记录日志并退化为「无浮窗、仅菜单」的托盘，悬停回落到系统 tooltip。

## 代码影响

| 路径 | 职责 |
| --- | --- |
| `tray_windows.go` | 重写：原生 `Shell_NotifyIcon` 托盘，注册 v4 版本、处理悬停/左键/右键、驱动菜单与状态更新 |
| `tray_hover_windows.go` | 新增：浮窗窗口类注册、创建、显示/隐藏与定位；嵌入 WebView2；处理 JS → Go 消息 |
| `tray_hover_asset_windows.go` | 新增：浮窗内联 HTML/CSS/SVG 常量 |
| `internal/winui/`（暂定） | 新增：`user32.dll` / `shell32.dll` 的 `LazyProc` 封装与相关常量、结构体，供上述文件复用 |
| `go.mod` | `github.com/wailsapp/go-webview2` 由间接依赖提升为直接依赖；移除 `github.com/getlantern/systray` 直接依赖（macOS 侧本就不使用） |
| `tray_darwin.go` | 不改动 |
| `main.go`、`app.go` | 不改动（依赖 `startTray` 签名与既有回调保持不变） |

## 测试计划

```powershell
go test ./...
npm --prefix frontend test
npm --prefix frontend run build
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
```

重点覆盖：

- `tray_platform_test.go` 两条架构守卫必须继续通过：以 `GOOS=darwin` 解析包时不得导入 systray；`tray_native_darwin.m` 不得出现 `AppDelegate` / `setDelegate:` / `[NSApp run]`。
- 新增 Win32 封装的纯函数部分（浮窗定位计算：给定图标矩形与工作区，输出浮窗坐标）应有单元测试，覆盖任务栏四个停靠边缘与靠屏幕角落时的钳制。
- 构建前须退出正在运行的 `easyshare.exe`，否则 Windows 锁定可执行文件。

手工验收按上文「验收标准」10 条逐条执行，其中第 8、9 条需实际调整任务栏位置与显示缩放。

## 排障方法

1. **悬停无反应**：先确认 `NIM_SETVERSION` 是否成功（失败则不会投递 `NIN_POPUPOPEN`，退化为普通 tooltip）；再确认回调消息号与 `WndProc` 的分支是否匹配；注意 v4 下 `wParam` 携带鼠标坐标、`lParam` 低位是事件码高位是图标 ID，与旧版本语义相反。
2. **浮窗白屏或完全透明（看到的是桌面壁纸）**：WebView2 控制器创建后**必须显式调用 `Show()`**。嵌入场景下控制器可见性不保证默认为真，不设时它根本不创建渲染子窗口，窗口类若又没有背景画刷，视觉上就是完全透明。判断依据：`EnumChildWindows` 查不到任何子窗口。
3. **`CoInitialize has not been called`**：WebView2 是 COM 组件，创建环境前必须在**该线程**先 `CoInitializeEx`（UI 线程用 STA）。COM 初始化绑定线程而非 goroutine，因此必须先 `runtime.LockOSThread`。
4. **控制器创建失败 `HRESULT 0x8007139F`（ERROR_INVALID_STATE）**：同一进程内两个 WebView2 环境使用了**相同用户数据目录但不同选项**。Wails 主窗口默认落在 `%AppData%\easyshare.exe` 且带自己的 `AdditionalBrowserArgs`，因此浮窗必须显式指定独立的 `DataPath`。
5. **`go-webview2` 的 `Embed` 永远返回 `true`**：错误只经 `SetErrorCallback` 上报，不能用返回值判断成败。必须在回调里收集错误，并额外校验 `GetController() != nil`。
6. **浮窗位置偏移**：不要用任务栏窗口位置推算，改用 `Shell_NotifyIconGetRect`；多显示器下坐标是虚拟桌面坐标，可能为负值。
7. **`MonitorFromPoint` 静默返回 NULL**：它的第一个参数是**按值传递的 `POINT`（8 字节）**，在 x64 上打包进单个寄存器。用 `LazyProc.Call` 传两个 uintptr 会让 `dwFlags` 收到 Y 坐标，函数返回 NULL 且不报错。改用收指针的 `MonitorFromRect` 可从根上避开。
8. **浮窗一闪即消**：收起延迟过短，或浮窗矩形命中判定使用了客户区坐标而非屏幕坐标。
9. **托盘图标残留**：进程退出路径未执行 `NIM_DELETE`。退出与崩溃路径都需清理。
10. **图标不显示**：`SetIcon` 需要 ICO 字节，PNG 无效；仍使用 `build/windows/icon.ico`。
11. **`Shell_NotifyIcon` 静默失败**：`NOTIFYICONDATAW` 的字段顺序或对齐与 C 不一致会导致 `CbSize` 校验不过，图标不出现且无报错。amd64 下应为 976 字节，已由 `internal/winui/win32_windows_test.go` 锁死。

## 回滚方式

改动集中在 `tray_windows.go` 与三个新增文件，`main.go` / `app.go` / 前端不受影响。回滚只需恢复 `tray_windows.go` 的 systray 实现、删除新增文件、并在 `go.mod` 还原两项依赖的直接/间接状态，无配置迁移、无数据结构变更、无 API 变更。

## 完成记录

托盘悬停链路已进入代码基线并通过程序化验证。

### 验证结果

```text
go build ./...                          通过
go vet ./...                            通过
go test ./internal/winui/               通过（13 个用例：定位计算、多屏负坐标、
                                        四个任务栏边缘、结构体布局与偏移锁定）
go test .                               通过（含两条 macOS 架构守卫）
npm --prefix frontend test              通过（5 文件 19 用例）
npm --prefix frontend run build         通过
wails build --nsis                      通过，产出 easyshare.exe / easyshare-core.exe
                                        / EasyShare-amd64-installer.exe

窗口结构（EnumWindows 实测）
  EasyShareTrayHost      Visible=False   消息宿主
  EasyShareHoverPopup    Visible=False   浮窗，悬停时才显示
  wailsWindow            Visible=True    主窗口

托盘图标（Shell_NotifyIconGetRect 实测）
  HRESULT = S_OK，矩形 L2054 T1078 R2086 B1126

悬停链路（向宿主投递 NIN_POPUPOPEN / NIN_POPUPCLOSE 实测）
  初始              可见=False
  NIN_POPUPOPEN 后   可见=True   位置(1910,1016) 尺寸 320x56
  NIN_POPUPCLOSE 后  可见=False（延迟收起生效）

定位正确性：图标中心 x=2070，浮窗居中后 x=2070-160=1910；
底边 1016+56=1072，恰好贴合自动隐藏任务栏的工作区下沿。

渲染：截图确认标题栏正常显示（图标 / 私人云盘 / 头像 / 设置），
系统处于深色模式时自动走深色配色。
```

### 依赖变化

`go mod tidy` 移除 `github.com/getlantern/systray` 及其 8 个传递依赖，
`github.com/wailsapp/go-webview2` 由间接依赖提升为直接依赖。

### 已知限制与待验收项

- **未做真实鼠标交互验收**：上述悬停验证是向窗口投递 `NIN_POPUPOPEN`/`NIN_POPUPCLOSE` 完成的，
  等价于系统在悬停时的行为，但真实鼠标移入移出、从图标移向浮窗、点击设置图标、
  右键菜单三项、退出后图标是否残留，仍需人工确认。
- **验收标准第 8、9 条未验证**：需实际调整任务栏停靠边缘与显示缩放。本机任务栏为自动隐藏、
  显示缩放 100%，仅覆盖了「底部 + 100%」一种组合。
- **本机无法跑完整 `scripts/build.ps1`**：流水线第一步 `go test` 会被既有缺陷
  [KI-4](../known-issues.md#ki-4预览测试依赖本机注册表的-mime-映射) 阻断，
  该缺陷与本迭代无关（未改动的基线副本同样失败）。本轮改为逐段执行等价命令。
- **macOS 未实现对等能力**，属本迭代非目标。
- **头像为纯占位**，不请求任何用户数据。

### 后续切片

用户确认的形态是「桌面常驻悬浮窗 + 托盘图标悬停」两者都要，本切片只完成后者。
桌面常驻悬浮窗（可拖动、可固定、可拖入文件）为下一切片，其窗口创建、WebView2 嵌入、
定位计算与标题栏页面可直接复用本切片成果。

### 关于参照 WPS 的调研结论

WPS 的弹窗界面确为 CEF 宿主（`promecefpluginhost.exe`）加载磁盘上的 Vue 单页应用，
资源位于 `office6/addons/<模块>/mui/.../res/<页面>/`，入口由 `run.ini` 的
`entry=%workingroot%/index.html` 声明。这验证了「原生窗口 + 浏览器内核渲染 HTML」
的选型方向一致。

但**窗口机制无法从其前端资源中学到**：`run.ini` 仅含入口路径，CSS/JS 中没有任何
窗口尺寸、置顶、悬停触发相关内容；这些位于已编译的原生 DLL（`kdockpanelhost.dll`、
`qing` 模块等）中。因此窗口层实现以 Win32 文档与实测为准。

DeskBox（桌面整理参照项目）为 GPL-3.0 的 C#/WinUI3 项目，**只借鉴架构不移植代码**：
其整理模块拆分为 扫描 → 分类 → 规则解析 → 计划 → 放置规划 → 事务执行，
并单独实现了事务回滚与崩溃恢复、自动整理的抑制机制。这些取舍在后续「自动整理」
切片中作为设计参考，Go 侧自主实现。
