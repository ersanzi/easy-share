# 迭代记录 — OKF 化切片二：生产索引重建与原子替换（2026-10-11，用户直配）

> 触发：grilling 五问后用户授权「你认为需要做就做」。10-02 切片一验证了「索引可以从 derived
> 完整重建」，本片把最后一步补上：生产索引坏了/丢了，一条命令从 derived 重建并原子替换。
> 至此对标文档架构原则 1「derived/ 是唯一知识源，向量索引是可重建缓存」从口号变为完整可执行的恢复路径。

## 实现

- **重建核心上移** `knowledge/app/kb/rebuild.py`：`document_from_payload` / `rebuild_doc_items`
  从切片一脚本上移为可复用模块（脚本改为导入，验证口径零变化）；新增三块：
  - `collect_rebuild_targets`：文档清单**任务库为权威**（`JobStore.latest_completed_docs()`，
    每个 file_id 取最新一次 completed——丢块/丢文档的记录无法从索引自身反查，任务库才能恢复
    灾难场景），索引反查 version_id 兜底合并历史年代文档；
  - `plan_rebuild`：`--apply` 预览，只枚举不读 derived 不写盘（真实 embedder 重建要花 API 钱，
    预览必须零成本）；
  - `apply_rebuild`：重建到同目录临时文件 → 旧索引备份 `.bak-<时间戳>` → `os.replace` 原子替换。
- **安全语义**：任一文档重建失败即整体中止，生产索引文件保持原样（防静默丢文档）并清掉半成品
  临时文件；Milvus 后端显式拒绝（`_refuse_milvus`，切 Milvus 后重建验证另做）；
  embedding 用与线上查询同一 `build_embedder` 工厂——重建与查询口径必然一致，不存在
  「重建用 A 模型、查询用 B 模型」的维度错配。
- **CLI**：`scripts/rebuild_from_derived.py` 增 `--apply`（预览）/ `--apply --yes`（执行）双段式；
  文档字符串与输出反复强调**必须停服务后执行**——运行中服务持有内存索引，下次入库写回会覆盖
  重建结果；完成后重启服务，BM25 派生索引按 count 一致性自动懒重建。
- **测试例外**：apply 是覆盖生产索引的破坏性写路径，在冲刺期轻测试约定下例外写了 3 个用例
  （恢复丢失文档+备份留存 / 缺 derived 整体中止索引不动 / 任务库选最新 completed 版本且
  failed 不入选）。

## 验证

- pytest 全量 **150 passed, 1 skipped**（147 基线 + 3 新用例）。
- 切片一验证脚本端到端重跑：四指标与原留档**逐位一致**（0.952/0.857/0.892/0.905），重构零破坏。
- `--apply` 预览冒烟（本机真实数据）：枚举 4 文档、Embedder 识别为 OpenAIEmbedder（.env 真实
  配置）、目标路径正确、预览零写入、退出码 0。
- `--apply --yes` 真实替换留待有真实规模数据的部署机执行（本机 data/ 只有 4 个测试文档）。

## 边界与后续（不做清单）

- Milvus 后端重建不支持（显式拒绝）——观察期默认 JSON，切 Milvus 时另做；
- OKF frontmatter 导出（P1b）、文档级粗筛路由（P1c）仍留后续切片；
- `--apply` 运行中服务的写回冲突靠「停服务」纪律而非进程检测（观察期单机部署，纪律够用）。
