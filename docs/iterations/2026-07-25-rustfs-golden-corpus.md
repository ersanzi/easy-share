# RustFS 真实集成测试与 Office 黄金语料

> 日期：2026-07-25
> 状态：进行中

## 1. 用户问题

在 Python 文档处理闭环完成后，用户确认继续推进两个 P0：

1. 使用真实 RustFS 验证原始对象读取、清洗产物写回和索引结果；
2. 建立首批 Office 黄金测试文档，固定 DOCX、XLSX、PPTX 和文本型 PDF 的结构化解析预期。

## 2. 本轮目标

- 集成测试默认不依赖外部服务，只有显式开启并提供凭据时才连接本地 RustFS；
- 测试在独立前缀下创建对象，结束时只清理该前缀，不能污染用户文件；
- 验证 `source object → Python pipeline → derived artifacts` 的真实 S3 兼容读写闭环；
- 黄金样本由代码确定性生成，避免提交来源不明的二进制文件；
- 预期结构采用可读 JSON 清单，既用于自动断言，也便于人工评审解析质量；
- Docker/RustFS 不可用时，单元测试仍可通过，并明确报告集成测试跳过原因。

## 3. 计划影响

- `knowledge/tests/integration/`：真实 RustFS 集成测试；
- `knowledge/tests/golden/`：黄金样本生成器、预期清单和解析测试；
- `knowledge/README.md`：运行命令、环境变量和样本维护说明；
- `docs/troubleshooting.md`：Docker、RustFS、bucket 与凭据排障；
- `docs/progress.md`：进度唯一真相源。

## 4. 验证计划

```powershell
# 默认回归：不要求 RustFS 在线
python -m pytest knowledge/tests -q

# 显式启用真实 RustFS 集成测试
$env:EASYSHARE_RUN_RUSTFS_INTEGRATION = '1'
$env:EASYSHARE_RUSTFS_ENDPOINT = 'http://127.0.0.1:9000'
$env:EASYSHARE_RUSTFS_ACCESS_KEY = '<access-key>'
$env:EASYSHARE_RUSTFS_SECRET_KEY = '<secret-key>'
$env:EASYSHARE_RUSTFS_BUCKET = 'easyshare'
python -m pytest knowledge/tests/integration -q -m integration
```

## 5. 排障记录

待实现与验证后补充。