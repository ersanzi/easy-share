"""可配置清洗规则引擎：结构噪声清除 + PII 脱敏 + 自定义正则。

规则集是可序列化 JSON 数据而非硬编码逻辑：当前从本地 `data/cleaning_rules.json`
加载（无文件则用内置默认）；里程碑 2 起由 Java 控制面按租户下发同一 schema，
Python 端零改动。命中统计写入 manifest，保证清洗可追溯、可对账。

规则类别与默认开关：
- 结构噪声（默认开）：跨页重复页眉页脚、独立页码行——只作用于分页文档，提升检索质量。
- PII 脱敏（默认关）：手机号/身份证/邮箱/地址——脱敏会伤问答召回，是业务决策，按需开启。

外部规则（将来来自 Java/租户）执行前有防护：正则长度上限、规则数上限、
编译失败降级为警告并跳过，避免 ReDoS 或坏规则拖死管线。
"""
from __future__ import annotations

import json
import logging
import re
from dataclasses import dataclass, field
from pathlib import Path

from app.parsing.models import DocumentBlock, ParsedDocument

logger = logging.getLogger(__name__)

MAX_PATTERN_LENGTH = 200
MAX_RULES = 100
MASK_CHAR = "*"
# 清洗动作明细上限：防止超大文档把 manifest 撑爆；文本截断保证单条明细可控
MAX_ACTIONS = 1000
ACTION_TEXT_LIMIT = 600

# kind 取值：
#   header_footer     跨页重复行检测（结构规则，无 pattern，params.min_pages/max_line_chars）
#   regex_remove_line 整行命中即删除该行（params.pages_only 限定分页文档）
#   regex_mask        命中片段脱敏，保留前 keep_prefix / 后 keep_suffix 个字符
#   regex_replace     命中片段替换为 replacement
VALID_KINDS = {"header_footer", "regex_remove_line", "regex_mask", "regex_replace"}


@dataclass(slots=True)
class CleaningRule:
    id: str
    name: str
    kind: str
    enabled: bool = True
    pattern: str | None = None
    replacement: str = ""
    keep_prefix: int = 0
    keep_suffix: int = 0
    params: dict = field(default_factory=dict)

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "name": self.name,
            "kind": self.kind,
            "enabled": self.enabled,
            "pattern": self.pattern,
            "replacement": self.replacement,
            "keep_prefix": self.keep_prefix,
            "keep_suffix": self.keep_suffix,
            "params": self.params,
        }


def default_rules() -> list[CleaningRule]:
    return [
        CleaningRule(
            id="header-footer",
            name="跨页重复页眉页脚",
            kind="header_footer",
            enabled=True,
            params={"min_pages": 3, "min_ratio": 0.6, "max_line_chars": 60},
        ),
        CleaningRule(
            id="page-number",
            name="独立页码行",
            kind="regex_remove_line",
            enabled=True,
            pattern=r"^\s*(?:[-—–]\s*)?(?:\d{1,4}|第\s*\d{1,4}\s*页(?:\s*[/，,]?\s*共\s*\d{1,4}\s*页)?)(?:\s*[-—–])?\s*$",
            params={"pages_only": True},
        ),
        CleaningRule(
            id="phone-mask",
            name="手机号脱敏",
            kind="regex_mask",
            enabled=False,
            pattern=r"(?<!\d)1[3-9]\d{9}(?!\d)",
            keep_prefix=3,
            keep_suffix=4,
        ),
        CleaningRule(
            id="id-card-mask",
            name="身份证号脱敏",
            kind="regex_mask",
            enabled=False,
            pattern=r"(?<!\d)\d{17}[\dXx](?!\d)",
            keep_prefix=6,
            keep_suffix=4,
        ),
        CleaningRule(
            id="email-mask",
            name="邮箱脱敏",
            kind="regex_mask",
            enabled=False,
            pattern=r"(?<![A-Za-z0-9._%+-])[A-Za-z0-9._%+-]{3,64}(?=@[A-Za-z0-9.-]+\.[A-Za-z]{2,})",
            keep_prefix=2,
            keep_suffix=0,
        ),
        CleaningRule(
            id="address-mask",
            name="地址脱敏（实验性，正则误伤率高）",
            kind="regex_mask",
            enabled=False,
            pattern=(
                r"[一-龥]{2,8}(?:省|自治区)?[一-龥]{2,8}市"
                r"[一-龥]{2,12}(?:区|县|旗)[一-龥，0-9A-Za-z-]{2,30}?"
                r"(?:路|街|道|巷|号|栋|楼|室|单元)[0-9A-Za-z一-龥-]{0,12}"
            ),
            keep_prefix=6,
            keep_suffix=0,
        ),
    ]


class RuleEngine:
    """按规则集清洗结构化文档，返回逐规则命中统计。"""

    def __init__(self, rules: list[CleaningRule]) -> None:
        self.rules: list[CleaningRule] = []
        self.compiled: dict[str, re.Pattern] = {}
        self.load_warnings: list[str] = []
        for rule in rules[:MAX_RULES]:
            if rule.kind not in VALID_KINDS:
                self.load_warnings.append(f"清洗规则 {rule.id} 类型未知（{rule.kind}），已跳过")
                continue
            if rule.kind != "header_footer":
                if not rule.pattern:
                    self.load_warnings.append(f"清洗规则 {rule.id} 缺少 pattern，已跳过")
                    continue
                if len(rule.pattern) > MAX_PATTERN_LENGTH:
                    self.load_warnings.append(f"清洗规则 {rule.id} 正则超过 {MAX_PATTERN_LENGTH} 字符，已跳过")
                    continue
                try:
                    self.compiled[rule.id] = re.compile(rule.pattern)
                except re.error as exc:
                    self.load_warnings.append(f"清洗规则 {rule.id} 正则无效（{exc}），已跳过")
                    continue
            self.rules.append(rule)
        if len(rules) > MAX_RULES:
            self.load_warnings.append(f"清洗规则超过 {MAX_RULES} 条上限，超出部分已忽略")

    def enabled_rules(self) -> list[CleaningRule]:
        return [rule for rule in self.rules if rule.enabled]

    def apply(self, document: ParsedDocument) -> dict[str, int]:
        """就地清洗 document.blocks，返回 {rule_id: 命中次数}（含 0 命中的启用规则）。"""
        self.actions: list[dict] = []  # 清洗动作明细，供驾驶舱 Diff 视图使用
        hits: dict[str, int] = {rule.id: 0 for rule in self.enabled_rules()}
        for rule in self.enabled_rules():
            if rule.kind == "header_footer":
                hits[rule.id] += self._apply_header_footer(document, rule)
        for rule in self.enabled_rules():
            if rule.kind == "header_footer":
                continue
            hits[rule.id] += self._apply_regex_rule(document, rule)
        document.blocks = [
            block for block in document.blocks if block.text.strip() or any(any(row) for row in block.rows)
        ]
        return hits

    def _record(
        self,
        rule_id: str,
        rule_name: str,
        block_id: str,
        kind: str,
        before: str,
        after: str = "",
    ) -> None:
        """记录一次清洗动作（截断控制体积），供清洗 Diff 视图渲染。"""
        if len(self.actions) >= MAX_ACTIONS:
            return
        self.actions.append({
            "rule_id": rule_id,
            "rule_name": rule_name,
            "kind": kind,
            "block_id": block_id,
            "before": before[:ACTION_TEXT_LIMIT],
            "after": after[:ACTION_TEXT_LIMIT],
        })

    # ---- 结构规则 ----

    def _apply_header_footer(self, document: ParsedDocument, rule: CleaningRule) -> int:
        """同一短行在足够多的不同分页上重复出现，即视为页眉/页脚并整块删除。"""
        min_pages = int(rule.params.get("min_pages", 3))
        min_ratio = float(rule.params.get("min_ratio", 0.6))
        max_chars = int(rule.params.get("max_line_chars", 60))

        pages = {block.source.page for block in document.blocks if block.source.page is not None}
        if len(pages) < min_pages:
            return 0

        text_pages: dict[str, set[int]] = {}
        for block in document.blocks:
            if block.source.page is None or block.rows or not block.text:
                continue
            text = block.text.strip()
            if not text or len(text) > max_chars:
                continue
            text_pages.setdefault(text, set()).add(block.source.page)

        threshold = max(min_pages, int(len(pages) * min_ratio + 0.999))
        noisy = {text for text, seen in text_pages.items() if len(seen) >= threshold}
        if not noisy:
            return 0

        removed = 0
        kept: list[DocumentBlock] = []
        for block in document.blocks:
            if block.source.page is not None and not block.rows and block.text.strip() in noisy:
                removed += 1
                self._record(rule.id, rule.name, block.id, "remove_block", block.text)
                continue
            kept.append(block)
        document.blocks = kept
        return removed

    # ---- 正则规则 ----

    def _apply_regex_rule(self, document: ParsedDocument, rule: CleaningRule) -> int:
        pattern = self.compiled[rule.id]
        pages_only = bool(rule.params.get("pages_only", False))
        count = 0
        for block in document.blocks:
            if pages_only and block.source.page is None:
                continue
            if block.text:
                block.text, changed = self._apply_to_text(block.text, rule, pattern)
                if changed:
                    self._record(rule.id, rule.name, block.id, "text", self._preview_before, block.text)
                count += changed
            if block.rows:
                new_rows = []
                for row in block.rows:
                    new_row = []
                    for cell in row:
                        cell_text, changed = self._apply_to_text(cell, rule, pattern, line_scope=False)
                        if changed:
                            self._record(rule.id, rule.name, block.id, "text", self._preview_before, cell_text)
                        count += changed
                        new_row.append(cell_text)
                    new_rows.append(new_row)
                block.rows = new_rows
        return count

    def _apply_to_text(
        self, text: str, rule: CleaningRule, pattern: re.Pattern, line_scope: bool = True
    ) -> tuple[str, int]:
        self._preview_before = text  # 供动作明细记录修改前的文本
        if rule.kind == "regex_remove_line":
            if not line_scope:
                return text, 0
            kept_lines = []
            removed = 0
            for line in text.split("\n"):
                if pattern.fullmatch(line.strip()) or pattern.fullmatch(line):
                    removed += 1
                    continue
                kept_lines.append(line)
            return "\n".join(kept_lines), removed

        if rule.kind == "regex_mask":
            def mask(match: re.Match) -> str:
                value = match.group(0)
                prefix = value[: rule.keep_prefix] if rule.keep_prefix > 0 else ""
                suffix = value[len(value) - rule.keep_suffix :] if rule.keep_suffix > 0 else ""
                middle_len = max(0, len(value) - len(prefix) - len(suffix))
                return f"{prefix}{MASK_CHAR * middle_len}{suffix}"

            new_text, count = pattern.subn(mask, text)
            return new_text, count

        # regex_replace
        new_text, count = pattern.subn(rule.replacement, text)
        return new_text, count


def load_rules(path: str | Path | None) -> RuleEngine:
    """从 JSON 配置构建引擎：文件不存在用内置默认；文件中的规则按 id 覆盖内置默认，
    新 id 追加。文件损坏时退回内置默认并记录警告，不让坏配置中断管线。"""
    rules = {rule.id: rule for rule in default_rules()}
    engine_warnings: list[str] = []
    if path:
        file_path = Path(path)
        if file_path.exists():
            try:
                payload = json.loads(file_path.read_text(encoding="utf-8"))
                for raw in payload.get("rules", []):
                    rule_id = str(raw["id"])
                    base = rules.get(rule_id)
                    # 覆盖内置规则时未给出的字段继承内置值；全新规则用空缺省
                    rule = CleaningRule(
                        id=rule_id,
                        name=str(raw.get("name", base.name if base else rule_id)),
                        kind=str(raw.get("kind", base.kind if base else "")),
                        enabled=bool(raw.get("enabled", base.enabled if base else True)),
                        pattern=raw.get("pattern", base.pattern if base else None),
                        replacement=str(raw.get("replacement", base.replacement if base else "")),
                        keep_prefix=int(raw.get("keep_prefix", base.keep_prefix if base else 0)),
                        keep_suffix=int(raw.get("keep_suffix", base.keep_suffix if base else 0)),
                        params=dict(raw.get("params", base.params if base else {})),
                    )
                    rules[rule.id] = rule
            except (json.JSONDecodeError, KeyError, TypeError, ValueError) as exc:
                engine_warnings.append(f"清洗规则配置 {file_path} 解析失败（{exc}），已退回内置默认规则")
                logger.warning("清洗规则配置解析失败: %s", exc)
                rules = {rule.id: rule for rule in default_rules()}
    engine = RuleEngine(list(rules.values()))
    engine.load_warnings = engine_warnings + engine.load_warnings
    return engine
