# 设计定稿：权限第三片 · 文档级成员角色

> 日期：2026-11-05。承接三层精细化路线前两片（功能矩阵 → 角色组/子管理员 → **本片：文档级成员角色**）。
> 设计参照：[钉钉文档权限粒度对标](../docs/intelligence/2026-11-05-dingtalk-docs-permission-reverse.md)（角色四档 / 67 动作枚举 / 护栏错误码 / 安全开关）。
> 状态：**已实现**（同日落地，实现与设计稿的差异回填见 [`docs/iterations/2026-11-05-文档级成员角色.md`](../docs/iterations/2026-11-05-文档级成员角色.md)——默认档两处按行为对账修正：空间 write 默认 manage、visible_depts=download；UI 面板留待真机点验后做）。

## 1. 现状与缺口

| 现状 | 位置 | 缺口 |
| --- | --- | --- |
| 文档可见性二值：`es_file.visible_depts`（逗号分隔部门 ID，空=共享空间全体可见） | `easyshare-file-visibility.sql`；裁剪收口 `/objects` 列举 | 只有「看得见/看不见」，没有「能干什么」 |
| 空间授权主体泛化（user/dept + read/write） | `es_space_member.permission` | 档位只有两档，且只到空间级 |
| owner 裁剪 | `/query` 按 owner 裁剪可见文档 | owner 之外没有成员级表达 |
| 功能开关五项 | `es_user_permission` / 角色并集 | 是「能不能用功能」，不是「这份文件能操作到什么程度」 |

用户原话（角色片记录）：文档级成员角色 =「管理/可编辑/只读，是否可下载、复制、外链」。

## 2. 模型（对齐钉钉，砍到我们的 API 面）

### 2.1 角色四档（内部字符串枚举）

| 档位 | 值 | 能力（映射我们的动作） |
| --- | --- | --- |
| 可管理 | `manage` | 预览/下载/改名/删除/分享/上传新版/**授权他人**/改可见性 |
| 可编辑 | `edit` | 预览/下载/上传新版/改名/删除/分享（不含授权） |
| 可查看下载 | `download` | 预览/下载 |
| 仅可查看 | `read` | 仅预览（不下载）——**等价于今天 `visible_depts` 的可见** |

**刻意不做 67 项动作枚举**：我们的文档动作只有六个（预览/下载/改名/删除/分享/授权），逐动作建模是过度设计；按档位归并能力集合，拦截仍在既有收口点做（列举 `/objects`、下载/预览端点、删除/改名端点）。

### 2.2 存储：一张主体角色表，不散在 es_file 上

```sql
CREATE TABLE IF NOT EXISTS es_file_member (
    file_id    BIGINT       NOT NULL,   -- es_file.file_id
    member_type VARCHAR(8)  NOT NULL,   -- user / dept（沿用 es_space_member 的双主体口径）
    member_id  BIGINT       NOT NULL,   -- sys_user.user_id 或 sys_dept.dept_id
    role       VARCHAR(16)  NOT NULL,   -- manage / edit / download / read
    PRIMARY KEY (file_id, member_type, member_id)
);
```

- **owner 不落表**：`es_file.owner` 即隐式 `manage`（钉钉「作者/更新者天然有权限」同款），代码判定，删文件随行清理，无孤儿行。
- **`visible_depts` 保留不动**：它升级为「部门批量授 `read` 档」的简写（语义完全兼容现有行为，存量零迁移）；`es_file_member` 是成员/部门级精确角色。
- **裁决顺序一处收口**（新 `FileRoleService.effective(fileId, userId)`）：owner → `es_file_member`（user 行 > dept 行，取高者）→ `visible_depts` 含本人部门 → `read`（共享空间默认）→ 不可见。规则一个静态方法，列表显示与实际拦截共用——前两片同款纪律。

### 2.3 两条服务端护栏（钉钉错误码同款，必抄）

1. **不能修改自己的角色**（钉钉 `52610018`）：防子管理员/可管理者给自己提权；
2. **仅 `manage` 档可授权**（钉钉 `PERMISSION_MANAGE` 能力点）：授权入口按 `effective` 结果放行，`/admin/roles` 超管例外。

### 2.4 拦截点（沿用既有收口，不新增面）

| 动作 | 收口点 | 判定 |
| --- | --- | --- |
| 列举/可见 | `/objects`（唯一列举出口） | `effective != 不可见` |
| 下载/预览 | 下载与内容票据端点 | 档位 ≥ `download`；`read` 档拒下载（预览票据照发） |
| 删除/改名/分享 | 既有写端点 | 档位 ≥ `edit`（分享另议，见 2.5） |
| 授权/改角色 | 新成员角色端点 | owner 或 `manage`，护栏 2.3 |
| 知识检索 | `/query`（`visible_doc_ids` 联动） | `read` 及以上可见——与今天 visible_depts 口径一致，零改动 |

### 2.5 安全开关（防复制/水印/外链）——本片不做

钉钉的安全三开关是**空间级**且作用对象是「可查看/下载档」。我们挂 `es_space` 的收益要等观察期有真实合规诉求再评估；本片只把角色档做对，开关留给下一片（避免一片做两件事）。

## 3. 界面（桌面端）

- 管理页「空间」Tab 的文件列表（共享空间文件行）加「角色」列；超管与 `manage` 档成员可见可改。
- 设置页个人视角不变（入口显隐走功能矩阵，与本片正交）。
- 知识页/网盘页下载按钮按档位显隐（`read` 档灰置），提示文案与钉钉对齐：「当前权限仅可在线预览，不能下载」。

## 4. 兼容与迁移

- 新表幂等 DDL 进 `ship-control-plane.ps1` 的 `$easyshareSql`；`visible_depts` 存量行语义不变（=部门 read 档）。
- 未配任何成员角色的文件：行为与今天完全一致（全可见可下载）。
- Go/前端级联：`FileRole` 结构 + 四端点绑定 + `types/core.ts`/`services/core.ts` 同步；Wails `wails generate module`。

## 5. 验收标准

1. 共享空间文件 A 设 B=`download`、C=`read` → C 能看到 A 但下载被拒（服务端 403，非藏按钮）；B 可下载不可授权。
2. D 所在部门在 `visible_depts` → D 生效 `read`；给 D 单独授 `edit` → 成员行覆盖部门行。
3. E 试图修改自己的角色 → 服务端拒绝；非 manage 档 F 调授权端点 → 拒绝。
4. 删文件 → 成员行随行清理；owner 视角始终全权。
5. 四层回归绿 + 文档闸门绿；真机点验清单进迭代文档。

## 6. 切片建议

一片做完四档 + 护栏 + 拦截（约与数据权限范围片同体量）。知识服务侧零改动（`/query` 口径不变）；桌面端 UI 一并。
