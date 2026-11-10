# 钉钉文档权限粒度对标取证 — 角色档位 / 能力点枚举 / 安全设置

日期：2026-11-05 　目标：钉钉文档（alidocs）Web 端权限模型
用途：**权限第三片「文档级成员角色」的设计参照**（承接 [角色片](../iterations/2026-10-27-企业权限角色与子管理员.md) 声明的第三层路线）
方法：**资源解析（非 REA）**——视觉/交互层不写在机器码里，按 AGENTS.md 口径走 Web 产物字符串挖掘

## 1. 取证通路（可复现）

钉钉文档 Web 端外壳 `https://alidocs.dingtalk.com/i/` 需登录（与听记不同，听记外壳免登录——10-13 文档方法对听记成立、对文档不成立）。绕行通路：

1. 免登录营销首页 `https://alidocs.dingtalk.com/`（200，42 KB）；
2. 首页挂着**公开模板节点**链接（`/i/nodes/<id>`），抓其一 → 200（85 KB），页面直出 **`dingdoc-portal-pc/2.143.0`** 全量 chunk 清单（`g.alicdn.com/alidocs/dingdoc-portal-pc/...`）；
3. 下载 **24 个 JS chunk（8.7 MB）** 到 `%TEMP%\alidocs\bundle\`，Python 正则挖 i18n 与枚举。

> 权限面板本身在 portal 侧（成员/角色/安全设置的 i18n、枚举、错误码都在这 24 个 chunk 里），编辑器 canvas 才是另一个应用——**不需要登录就能拿到权限模型的核心**。

## 2. 权限模型（全部来自生产 bundle 字面量，标【S】）

### 2.1 两层角色，各自分档

| 层 | 档位 | 证据（i18n key → 文案） |
| --- | --- | --- |
| **空间/知识库**（`space_role_type_*`，三档） | 可管理 / 可编辑 / 可读 | `space_role_type_manage/edit/read` |
| **文档/条目**（`role_type_*`，四档） | 可管理 / 可编辑 / 可查看下载 / 仅可查看 | `role_type_manage="可管理"`、`role_type_editor`、`role_type_reader_desc="导出(下载)/在线预览"`、`role_type_read_only_desc="在线预览仅，不能导出(下载)"` |

门户侧枚举（chunk `4360`，模块 216304）只有三档字符串枚举：

```js
RoleEnum = { READ: "READ", DOWNLOAD: "DOWNLOAD", WRITE: "WRITE" }
```

「可管理」不走这个枚举——由独立的 `PERMISSION_MANAGE` 能力点与所有者身份表达（见 2.2/2.4）。

### 2.2 角色即能力集合，动作按能力点判定

chunk `3297` 的**角色→能力映射矩阵**（文件选择器按所需能力过滤动作）：

```js
Ee = { [READ]: ["read"], [DOWNLOAD]: ["download", "create-children"], [WRITE]: ["move", "create-children"] }
Ce = { [READ]: "role_type_read_only", [DOWNLOAD]: "role_type_reader", [WRITE]: "role_type_editor" }
```

细粒度**动作枚举全表（chunk `3927`，67 项，字符串值自描述）**，分三族：

- **文档级动作**（33 项）：`EDIT`、`RENAME`、`SHARE`、`DOWNLOAD`、`DUPLICATE_FILE(_TO)`、`MOVE_DENTRY_TO`、`SEND_COPY`、`PRINT`、`HISTORY`、`RECOVER`、`DELETE`、`SAVE_AS_TEMPLATE`、`DOWNLOAD_AS`、`UPLOAD_NEW_VERSION`、**`PERMISSION_MANAGE`**（谁能授权别人）、**`SECURITY_SETTING`**（谁能改安全开关）、`APPLY_FOR_EDIT`（权限申请）、`OPERATION_LOG`、`INTELLIGENT_SUMMARY`、`ENABLE_OFFLINE`、`ATTENTION` …
- **空间级动作**（10 项）：`SPACE_SHARE`、**`SPACE_PERMISSION_MANAGE`**、`SPACE_COPY_LINK`、`SPACE_RENAME`、`SPACE_RECYCLE`、`QUIT_SHARE`、`OPEN_LOCAL_SYNC`、`CLOSE_LOCAL_SYNC`、`SYNC_MANAGE`
- **节点状态/种类**：`NORMAL/DISABLED/HIDDEN`；`WORKSPACE/FOLDER/FILE/TEAM`

> **这就是「角色档位」与「权限点」的分工**：档位给 UI 选（四个好懂的词），服务端拦截按**动作枚举**判（67 个细粒度动作）。与钉钉子管理员「范围档位 + 服务端裁剪」同构。

### 2.3 隐式权限与护栏（服务端强制，可直接抄）

| 规则 | 证据 |
| --- | --- |
| **文档作者/更新者天然有权限**（不依赖授权表） | `workspace_writor_desc = "该知识库下的文档作者、更新者以及知识库管理员"` |
| **仅所有者、可管理权限可操作**（管理动作） | `workspace_authority_hint` |
| **不能修改自己的角色** | 错误码 `52610018 CANNOT_MODIFY_SELF_ROLE`（防越权自授） |
| **仅所有者或可管理可设所有者/管理员** | `52610016 ONLY_USER_CAN_SET_OWNER_OR_MANAGER` |
| 角色 ID 非法 / 申请档位不支持 | `52610017 INVALID_ROLE_ID`、`52610019 UNSUPPORTED_APPLY_ROLE` |
| 通用无权限 | `52600002 无当前操作权限，可联系管理员检查权限设置` |
| 空间管理员上限 100 条记录 | `52610008` |

### 2.4 安全设置（空间级三开关 + 文档 tag 展示）

| 开关 | 文案 | 作用对象 |
| --- | --- | --- |
| 访客水印 | `文档背景将显示访客的信息，防止截图盗用`；水印内容=「公司名称+姓名+手机号后4位」 | 空间级，文档列表 tag 提示 |
| 禁止复制 | `开启后，「可查看/下载」权限用户不能复制和导出内容` | **按角色档位生效**——只限查看/下载档，可编辑档不受限 |
| 公开分享 | `空间支持公开分享` | 企业级门控：`仅钉钉管理员可开启企业内公开` |

**企业外发三级管控**（`52614000/1/2` 错误码族）：可编辑及以上可添加企业外对象 → 文档所有者或可管理可发送 → 仅企业管理员。分享范围枚举：`仅协作者可见 / 指定人和{{orgName}} / 仅指定人可访问`，内部协作者 vs 外部协作者分列。

### 2.5 权限申请流

`ddocMainsite_permissionApply_*`：无权限访问文档 → 「向管理员发送权限申请」+ 理由 → 管理员同意/拒绝/忽略（`agree/disagree/ignore_success_desc`），另有 `PERMISSION_POLICY_PREFERENCE_GUIDE_*` 埋点（偏好引导）。

## 3. 对 EasyShare 权限第三片的设计建议

对照现状：我们已有 `es_file.visible_depts`（二值可见/不可见，部门粒度）与空间授权（user/dept 主体）。第三片要引入**文档级成员角色**，钉钉模型给出五条可直接落的设计参数：

1. **角色档位定四档**：可管理 / 可编辑 / 可查看下载 / 仅可查看——与钉钉对齐，用户零学习成本；内部存储用字符串枚举（`"manage"/"edit"/"download"/"read"`），拦截按档位归并出的能力集合判，**不在 API 层逐端点写档位名**。
2. **两条服务端护栏必须抄**：`不能修改自己的角色`（防止子管理员给自己提权——我们已在角色片踩过同类坑并拦过一次）与 `仅可管理档可授权`。护栏落在 Java 服务层一处收口，与 `canManage` 同风格。
3. **隐式权限规则**：文档上传者天然是可管理（我们是 owner 字段，`/query` 已按 owner 裁剪——把 owner 映射成「隐式可管理」即可，不落授权表）。
4. **安全开关先做空间级**：防复制/水印挂 `es_space`（与授权模型同表族），文档级 tag 只做展示；文档级开关留到有真实诉求再加（钉钉也是空间级优先）。
5. **错误码语义对齐**：把「谁能授权」「不能改自己」「角色 ID 非法」做成明确的服务端错误文案——钉钉的错误码族（52610016-19）证明这类护栏在企业场景是必要件而非锦上添花。

**不抄的部分**：`MOVE/RECYCLE/RECOVER/HISTORY/OPERATION_LOG` 等动作粒度对我们当前 API 面太细（我们的文档动作 = 上传/下载/删除/改名/分享，五项），拦截按路径收口（`DriveFeatureInterceptor` 模式）已够；动作枚举全表留档，等 API 面变密再对齐。

## 4. 本轮未闭合（诚实清单）

1. **授权 RPC 的请求/响应形态**未挖到（成员批量加角色、角色变更的协议字段）——在编辑器/服务侧 bundle，portal 不含；做第三片协议设计时如需可再挖（编辑器 bundle 入口需登录态或 share token）。
2. **真机 UI 观察**未做：角色选择器交互形态、权限面板布局（列了候选但本轮用资源解析先行——两候选中②已有模型级答案，①子管理员徽标交互仍待真机）。
3. 水印渲染实现、`SEND_COPY` 与防复制的强制点在客户端还是服务端——涉及编辑器侧，未取证。

## 5. 产物与复现

- chunk 缓存：`%TEMP%\alidocs\bundle\`（24 个 JS，8.7 MB，含 `translation_zh_CN.js` 全量 i18n）；外壳/节点页快照在 `%TEMP%\alidocs\`。Temp 目录会被清理，复现按 §1 三步重抓即可（版本号 2.143.0 会随上游滚动）。
- 本轮为纯公开资源解析：未登录任何账号、未抓包、未绕过鉴权（登录页 302 是服务端行为，改走公开节点入口）。
