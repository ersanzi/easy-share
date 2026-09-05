# 网盘目录导航 — 文件夹进入/面包屑/搜索/排序（Cloudreve 对标落地第一片）

- 日期：2026-09-06（用户指定主题：「把网盘这块优化下」，参考案例即 2026-07-25 Cloudreve 深度对标）
- 状态：**已完成**（vue-tsc 干净 / vitest 40 全绿 +7 用例 / go build+test 绿；真机 UI 冒烟随下轮打包验收）

## 1. 背景与切片选择

对标文档 [`cloudreve-benchmark.md`](../cloudreve-benchmark.md) 的 P0 路线是
「fileId + 轻量目录层 + Upload Session + 统一任务字段」。对照现状：

- 云盘对象以相对路径同时承担身份与定位（`Object{Path,Size,LastModified}`），无 fileId、无分片；
- 7 月对标后存储授权已归 RuoYi 控制面（ADR-0007），**权威目录层与 Multipart 的正解落点在控制面**（es_file 表 + 预签名 Multipart 端点），跨 Java/Go 两仓，不属于本轮；
- 而用户侧最痛的可见缺口是：上传文件夹保留目录结构后，网盘页把所有对象**摊平成一张列表**——`photos/2024/img.jpg` 和根目录文件混排，找不到、进不去、搜不了。

故本轮取「目录层的客户端最小实现」：**视图推导，不造第二个真相源**——从扁平 key
推导文件夹树做导航，未来控制面目录层上线后本模块直接换数据源，UI 不动。

## 2. 实现

| 位置 | 内容 |
| --- | --- |
| `frontend/src/utils/driveFolder.ts`（新） | 纯函数视图推导：`buildDriveView`（当前目录直接子项/一级文件夹聚合/跨后代搜索/三种排序）、`breadcrumbsFor`、`relativeDir`；S3 零字节目录占位对象（`dir/`）只参与文件夹推导；文件夹恒排文件前 |
| `CloudPanel.vue` | 面包屑（点击回跳）、文件夹行（点击进入）、搜索框（当前目录全部后代、命中行显示相对路径）、排序（时间/名称/大小 + 升降向）、统计徽标（N 文件夹 · M 文件 · 总大小） |
| `app.go` | `CloudUpload/CloudUploadFolder` 增加 `targetDir` 参数 + `joinObjectKey` 助手——**上传落当前目录**；拖拽/悬浮窗路径（`CloudUploadPaths`、spacemount）传空保持落根目录语义不变 |
| Wails 级联 | `wails generate module` 重生成绑定 → `services/core.ts` 透传 → `useEasyShare.ts` 封装带默认参 → `App.vue` 事件带目录 |

两个开关正交说明：上传落点 = 页面当前目录；目标空间（个人/共享）仍由悬浮窗切换器决定（拖拽链路），面板按钮链路固定个人空间（与现状一致）。

## 3. 验证

- vitest 新增 7 用例：根目录聚合、直接子项过滤、面包屑、跨后代搜索与范围外不命中、三种排序方向、占位对象语义、空目录兜底；全量 40 过。
- `vue-tsc --noEmit` 干净（级联后的类型一致）；`go build ./...` + drive/spacedav 包测试绿。
- 真机点验（进目录→上传落位→刷新→搜索）归下轮打包冒烟。

## 4. 遗留与下一步（网盘后续切片的既定顺序）

1. **控制面权威目录层 + fileId**（Java platform-drive：es_file 表、列表带 fileId、按 fileId 操作）——对标 P0 正主，解锁回收站/版本/分享重命名稳定/KI-3 关闭；
2. **Upload Session + Multipart 断点续传**（依赖控制面 Multipart 预签名端点）；
3. 缩略图缓存、批量操作逐项结果、分享记录管理（P1/P2）。
