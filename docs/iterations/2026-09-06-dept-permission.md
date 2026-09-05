# 部门级权限 — 空间级（片 1）+ 文档级可见性数据面（片 2）

- 日期：2026-09-06（用户拍板三项之二；设计定稿见 [`../../plans/2026-09-06-dept-permission.md`](../../plans/2026-09-06-dept-permission.md)）
- 状态：**片 1、片 2 数据面已完成**（Java 39 全绿 +7 用例；go 全绿；/query 联动核验后拆片 2b，见下）。

## 1. 设计要点（详见设计定稿，此处只记落定结论）

- 授权主体泛化：es_space_member 加 `member_type`（user/dept），`user_id` 列复用存主体 ID
  （列名不动、语义复用——存量数据默认 user 型零迁移）；唯一索引重建为
  `(space_id, member_type, user_id)`。
- **生效权限 = max(个人行, 所属部门行)**，write > read：显式个人授权不会被部门只读压窄，
  收窄要么删个人行、要么删部门行，规则可预期。
- 用户部门归属：只读投影 `sys_user(user_id, dept_id, user_name, nick_name)`（同进程同库，
  只读不动上游）；v1 精确匹配本部门，不含祖先链（重评估条件已记设计文档）。
- 部门下拉：只读投影 `sys_dept`（status='0' 且 del_flag='0'）。

## 2. 实现

| 位置 | 内容 |
| --- | --- |
| `deploy/ruoyi-db/easyshare-space-member-type.sql`（新） | ALTER 加列 + 重建唯一索引 |
| `domain/EsSpaceMember` + `SysUserDept`/`SysDeptRow` + 两个只读 Mapper | 主体泛化与系统表投影 |
| `SpaceService` | `grantSharedTo(memberType,…)`（泛化，旧签名委托）、`sharedPermissionOf` 升级为生效合并、`deptIdOf`/`listDepts` |
| `SpaceController` | `shared-members` 响应升级为 `List<MemberVo{memberType,memberId,permission,name}>`（name=昵称/部门名）；`shared-grant` 加 memberType（缺省 user，旧调用不变）；新增 `GET /admin/depts` |
| `internal/account/space.go` | SharedMembers 新结构 + **部署窗口期双格式兼容**（老 jar 返回 map 自动转换）；GrantShared 加 memberType；ListDepts |
| `app.go` + Wails 级联 + `AdminPanel.vue` | AdminSharedMembers/AdminGrantShared/AdminListDepts 新签名；授权区拆「按账号」「按部门」两块（部门下拉 + 授权/撤销；提示生效规则） |

## 3. 验证

- Java 37/37（+5：生效合并取宽/部门独立授权/无授权/类型化插入/非法类型拒绝）。
- go build/test 全绿；前端 vitest 40（AdminPanel 测试契约同步：mock 状态链）、vue-tsc 干净。
- 真机：管理页部门授权操作 + 部门成员共享盘可见性，随下轮打包冒烟。

## 4. 片 2 文档级可见性 — 数据面已落地（2026-09-06 同日完成）

- DDL `easyshare-file-visibility.sql`：es_file 加 `visible_depts`（空=全体可见，逗号分隔部门 ID）；
- 登记：`presign-put` 请求体加可选 `visibleDepts`（仅共享空间语义，登记后写入目录层）；
- 设置：`POST /easyshare/drive/file-visibility`（操作者校验：个人=owner、共享=上传者本人）；
- **裁剪点收口在唯一列举出口**：`/objects` 共享列表按 `用户部门 ∈ visible_depts ∨ 上传者本人`
  过滤——网盘页/此电脑挂载盘/全局快搜文件路全部经此出口，自动生效；
- Go：`SetFileVisibility` 客户端 + `SetFileVisibility` 绑定（UI 入口待共享文件浏览片）；
- 测试 +2：操作者归属校验、过滤语义（部门命中/不中/上传者恒可见）。

## 5. /query 检索联动核验结论（拆片 2b）

核验结果：`/query` 的鉴权是**知识服务本地 auth（2a auth.db）**，`request.state.user`
只含 username/role——控制面 JWT 的 deptId claims 与此无关（核验了此前"JWT 带
deptId"的假设，不成立）。联动前置 = 知识服务用户-部门模型：
① auth 用户表加 dept 列 + 管理端同步；② ingest 请求透传 visible_depts 进 chunk
metadata；③ `visible_doc_ids` 扩展部门过滤。三步独立成片（2b），随知识服务侧
排期执行，数据面已先行兼容（列表/文件消费方已生效）。


es_file 加 `visible_depts`；网盘共享列表过滤；`/query` 检索按 visible_depts 裁剪
（核验控制面 JWT claims 是否含 deptId，无则登录态下发处补）；管理/上传入口设置可见性。
