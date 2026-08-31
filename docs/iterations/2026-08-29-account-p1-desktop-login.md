# 2026-08-29 账号控制面 P1：桌面客户端登录 + 登录态贯通

> 关联：[ADR-0007](../adr/0007-account-control-plane-ruoyi.md)、[P0 环境](2026-08-29-account-control-plane-p0.md)

## 用户问题

P0 把 RuoYi 控制面跑起来了，但桌面客户端还没接账号：打开就是主界面，没有登录、没有账号态。需要：登录门禁、登录后头像/昵称跟随账号、点头像进设置、**底部悬浮窗也要跟登录态连起来**（此前是静态占位「我」）、登出。

## 目标与非目标

### 目标
- 桌面客户端加登录门禁：未登录只显示登录页；登录调 RuoYi 拿 JWT（token 只留在桌面端进程，不下发前端）。
- 登录后主界面右上角显示头像（昵称首字）+ 昵称 + 登出。
- 点头像跳设置页。
- 悬浮窗头像跟随登录账号（登录/登出实时更新）。

### 非目标
- **不做**存储隔离（P2，修 KI-2/KI-3）——本阶段登录后云盘仍是旧的共享命名空间。
- **不做**管理员「管理」入口 / 管理后台（P3，需搭 plus-ui）。
- **不做**设置页改昵称/头像（后续增强，需 RuoYi 个人中心接口 + 头像上传 OSS）。
- **不做**接口加密适配：dev 走明文（RuoYi `--api-decrypt.enabled=false`）；生产加密适配后置。

## 完成记录

已完成并实测（SendKeys 自动登录 + 截图验证）。

### 落地内容
- **Go 控制面客户端** `internal/account/client.go`：`Login`（POST /auth/login，pc clientId）+ 取用户信息（GET /system/user/getInfo）+ `Logout`。注意 RuoYi 把 Long 型 ID 序列化为字符串，`User.UserID` 用 string。
- **配置** `internal/config/config.go`：新增 `PlatformBaseURL`（首个非 loopback 外部地址），默认 `http://localhost:8090`；**旧配置迁移**：加载时若该字段空则补默认值（老 config.json 无此字段）。
- **app.go**：新增导出方法 `Login/Logout/CurrentUser`（返回 `AuthUser`，不含 token）；会话存桌面端进程（`accountSession` + 锁）；`trayUserCh` 把登录态推给悬浮窗（模式同 `trayStatusCh`）。
- **前端**：`LoginView.vue` 登录页（账号预填 admin、密码框自动聚焦、回车登录）；`App.vue` 登录门禁 + 右上角账号 chip（头像点击进设置 + 登出）；`useEasyShare` 加 `currentUser/login/logout`；`services/core.ts` + `types/core.ts` + Wails 绑定三件套级联。
- **悬浮窗**：`tray_windows.go` 加 `watchUser` goroutine 读 `trayUserCh` → `hoverPopup.SetUser` → 跨线程消息 `hoverMsgSetUser` → `Eval(applyUser)`；`tray_hover_asset_windows.go` 头像加 id + `window.applyUser`。

### 验证结果
```text
go build ./... / go vet ./...     通过
go test ./internal/account ./internal/config .   通过
前端 vue-tsc + build + 19 测试     通过
真机（登录 admin/admin123）：
  登录页渲染 → 门禁生效                          ✓
  登录成功 → 主界面右上角「疯 疯狂的狮子Li ⎋」    ✓（头像=昵称首字）
  悬浮窗头像从「我」变为「疯」                     ✓（登录态贯通）
  错误密码 → 返回「密码输入错误N次」               ✓
```

### 踩的坑
1. **登录报「未配置账号服务地址」**：已存在的 config.json 是账号体系之前的，无 `platformBaseUrl` 字段，加载后为空（defaultConfig 只在新建时生效）。→ 加载时对空值补默认（旧配置迁移）。
2. **getInfo 用户信息为空**：RuoYi 把 Long ID 序列化为字符串，`UserID int64` 解析失败导致整个 user 解析报错。→ 改 string。
3. **SendKeys 登录打不进**：登录页密码框未聚焦。→ 密码框 `onMounted` 自动聚焦（也是 UX 改进）。
4. **程序化触发悬停弹窗失败**：托盘图标在 `^` 溢出区时 `Shell_NotifyIconGetRect` 取不到矩形，`showPopup` 跳过。验证悬浮窗头像时改用 `hoverMsgStartPinned` 直接右下角显示绕过。

### 已知限制与待验收项
- 登录后云盘文件仍是全局共享命名空间（隔离在 P2）。
- token 目前只在进程内存，重启桌面端需重新登录（安全存储 Credential Manager 后续做）。
- dev 关了接口加密/验证码；生产需加密适配。
- 手工交互验收：真实鼠标点头像跳设置、点登出、真实悬停托盘看头像。

## 回滚方式
改动集中在新增 `internal/account/`、`LoginView.vue` 与 app.go/前端/悬浮窗的账号相关增量；`PlatformBaseURL` 缺失时走默认、旧配置兼容。移除登录门禁即回到无账号旧行为。

## 后续
P2 存储隔离（object key 加用户前缀 + 控制面签发预签名 URL，修 KI-2/KI-3）→ P3 管理员+注册开关（含 plus-ui 后台、管理入口按角色显隐）→ 设置页账号资料（改昵称/头像）→ P4 悬浮窗滑动开关+共享盘。
