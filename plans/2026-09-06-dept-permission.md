# 部门级权限 — 设计定稿（2026-09-06，用户拍板）

> 背景：用户部署评估拍板「这文档只有研发能看」需要部门维度，借 RuoYi `sys_dept` 现成模型。
> 本设计拆两片：**片 1 空间级**（共享盘按部门授权进出，本轮交付）、**片 2 文档级**（es_file 可见部门 + 检索/列表过滤联动，下一轮）。

## 1. 主体模型泛化（片 1，本轮）

es_space_member 加 `member_type`（`user`/`dept`），授权主体从"账号"泛化为"账号或部门"：

- `user` 行：`user_id` 列 = 用户 ID（存量数据默认 user 型，零迁移成本）；
- `dept` 行：`user_id` 列 = 部门 ID（列名不动、语义复用——迁移只加一列 + 重建唯一索引）；
- 唯一索引 `(space_id, member_type, user_id)`。

**生效权限**：`effective(user) = max(user 行, 用户所属部门行)`，write > read。
两行并存时取宽——显式个人授权不应被部门只读压窄（管理员想收窄就删部门行或个人行，规则可预期）。

**用户部门归属**：只读映射 `sys_user(user_id, dept_id, user_name, nick_name)`——platform-drive
与 RuoYi 同进程同库，直接读系统表（只读、不动上游）；v1 只认本部门精确匹配，
不含祖先部门链（重评估条件：出现"全公司/全事业部授权"诉求再扩 ancestors 匹配）。

**接口变化**（全部 `/easyshare/space/admin/*`，superadmin 角色把关不变）：
- `shared-members` 响应 Map<userId,perm> → `List<{memberType,memberId,permission,name}>`；
  Go 客户端做双格式兼容解析（部署窗口期老 jar 返回 map 也能解析）。
- `shared-grant` 请求体加 `memberType`（缺省 user，旧调用不变）。
- 新增 `GET /admin/depts`：启用中的部门列表（dept_id/dept_name）。

**客户端**：AdminPanel 授权区拆「按账号」「按部门」两块；账号行显示生效权限
（含部门继承的标注）。悬浮窗空间切换/挂载走 `checkSharedReadable/Writable` →
service 层生效判定，客户端零改动。

## 2. 文档级可见性（片 2，下一轮）

- es_file 加 `visible_depts VARCHAR(500)`（部门 ID 逗号分隔；NULL/空 = 共享空间全体可见）；
- 上传/管理入口设置可见性；网盘列表（shared objects）按 `user deptId ∈ visible_depts` 过滤；
- 知识服务检索联动：2c 权限感知已按 owner 裁剪，扩展为 shared 文档再按 visible_depts
  裁剪——需要知识服务知道用户部门：确认控制面 JWT claims 是否带 deptId（RuoYi 登录
  响应含 deptId；知识服务 2c 解析的是控制面签发 token，大概率 claims 已有，实现时核验，
  若无则在登录态下发处补）；
- 与 MCP/WPS 通路天然复用（同 /query 出口）。

## 3. 不做

- 不做部门树祖先匹配（v1 精确匹配）；不做多部门叠加（v1 一用户一部门，RuoYi 模型如此）；
- 不做个人空间的部门维度（个人空间归 owner 独占，与部门正交）。
