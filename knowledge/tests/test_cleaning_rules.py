from __future__ import annotations

import json
import time

from fastapi.testclient import TestClient

from app.main import create_app
from app.parsing.models import DocumentBlock, ParsedDocument, SourceLocation
from app.parsing.rules import default_rules, load_rules
from tests.helpers import FakeStorage, make_services


def _paged_doc(pages: int, header: str = "机密文件 请勿外传") -> ParsedDocument:
    blocks: list[DocumentBlock] = []
    for page in range(1, pages + 1):
        blocks.append(DocumentBlock(id=f"h{page}", type="paragraph", text=header, source=SourceLocation(page=page)))
        blocks.append(
            DocumentBlock(
                id=f"b{page}", type="paragraph", text=f"第 {page} 页的正文内容各不相同。", source=SourceLocation(page=page)
            )
        )
        blocks.append(DocumentBlock(id=f"n{page}", type="paragraph", text=f"- {page} -", source=SourceLocation(page=page)))
    return ParsedDocument(filename="doc.pdf", media_type="application/pdf", blocks=blocks)


def test_header_footer_removed_when_repeated_across_pages() -> None:
    engine = load_rules(None)
    document = _paged_doc(pages=4)
    hits = engine.apply(document)
    texts = [block.text for block in document.blocks]
    assert hits["header-footer"] == 4
    assert all("机密文件" not in text for text in texts)
    assert any("正文内容" in text for text in texts)


def test_header_footer_kept_when_too_few_pages() -> None:
    engine = load_rules(None)
    document = _paged_doc(pages=2)
    hits = engine.apply(document)
    assert hits["header-footer"] == 0
    assert any("机密文件" in block.text for block in document.blocks)


def test_page_number_lines_removed_only_in_paged_blocks() -> None:
    engine = load_rules(None)
    document = _paged_doc(pages=4)
    engine.apply(document)
    assert all("- 1 -" not in block.text for block in document.blocks)

    unpaged = ParsedDocument(
        filename="note.txt",
        media_type="text/plain",
        blocks=[DocumentBlock(id="b1", type="paragraph", text="42")],
    )
    hits = engine.apply(unpaged)
    assert hits["page-number"] == 0
    assert unpaged.blocks[0].text == "42"


def test_pii_masking_disabled_by_default_and_enabled_via_config(tmp_path) -> None:
    document = ParsedDocument(
        filename="contact.txt",
        media_type="text/plain",
        blocks=[DocumentBlock(id="b1", type="paragraph", text="联系张三 13812345678 或 zhangsan@example.com")],
    )
    load_rules(None).apply(document)
    assert "13812345678" in document.blocks[0].text

    rules_path = tmp_path / "rules.json"
    rules_path.write_text(
        json.dumps({"rules": [{"id": "phone-mask", "enabled": True}, {"id": "email-mask", "enabled": True}]}),
        encoding="utf-8",
    )
    document2 = ParsedDocument(
        filename="contact.txt",
        media_type="text/plain",
        blocks=[
            DocumentBlock(id="b1", type="paragraph", text="联系张三 13812345678 或 zhangsan@example.com"),
            DocumentBlock(id="t1", type="table", rows=[["电话", "13998887766"]]),
        ],
    )
    engine = load_rules(rules_path)
    hits = engine.apply(document2)
    assert hits["phone-mask"] == 2
    assert "138****5678" in document2.blocks[0].text
    assert document2.blocks[1].rows[0][1] == "139****7766"
    assert "zhangsan@example.com" not in document2.blocks[0].text
    assert "zh******@example.com" in document2.blocks[0].text


def test_id_card_mask_keeps_region_and_checksum(tmp_path) -> None:
    rules_path = tmp_path / "rules.json"
    rules_path.write_text(json.dumps({"rules": [{"id": "id-card-mask", "enabled": True}]}), encoding="utf-8")
    document = ParsedDocument(
        filename="hr.txt",
        media_type="text/plain",
        blocks=[DocumentBlock(id="b1", type="paragraph", text="证件号 11010119900307863X 备案")],
    )
    load_rules(rules_path).apply(document)
    assert "110101********863X" in document.blocks[0].text


def test_custom_rule_and_bad_rules_are_guarded(tmp_path) -> None:
    rules_path = tmp_path / "rules.json"
    rules_path.write_text(
        json.dumps(
            {
                "rules": [
                    {"id": "watermark", "name": "去水印", "kind": "regex_replace", "pattern": "内部资料", "replacement": ""},
                    {"id": "broken", "name": "坏正则", "kind": "regex_mask", "pattern": "([unclosed"},
                    {"id": "huge", "name": "超长", "kind": "regex_mask", "pattern": "a" * 500},
                ]
            }
        ),
        encoding="utf-8",
    )
    engine = load_rules(rules_path)
    assert {rule.id for rule in engine.rules} >= {"watermark", "phone-mask"}
    assert all(rule.id not in {"broken", "huge"} for rule in engine.rules)
    assert len(engine.load_warnings) == 2

    document = ParsedDocument(
        filename="a.txt",
        media_type="text/plain",
        blocks=[DocumentBlock(id="b1", type="paragraph", text="内部资料 请注意")],
    )
    hits = engine.apply(document)
    assert hits["watermark"] == 1
    assert document.blocks[0].text.strip() == "请注意"


def test_corrupt_rules_file_falls_back_to_defaults(tmp_path) -> None:
    rules_path = tmp_path / "rules.json"
    rules_path.write_text("{ not json", encoding="utf-8")
    engine = load_rules(rules_path)
    assert {rule.id for rule in engine.rules} == {rule.id for rule in default_rules()}
    assert any("退回内置默认" in warning for warning in engine.load_warnings)


def _wait_for_terminal(client: TestClient, job_id: str) -> dict:
    for _ in range(150):
        response = client.get(f"/jobs/{job_id}")
        payload = response.json()
        if payload["status"] in {"completed", "failed"}:
            return payload
        time.sleep(0.01)
    raise AssertionError("job did not finish in time")


def test_pipeline_records_cleaning_hits_in_manifest(tmp_path) -> None:
    storage = FakeStorage({"source/contact.txt": "联系人电话 13812345678，请保密。".encode("utf-8")})
    services = make_services(tmp_path, storage)
    rules_path = tmp_path / "cleaning_rules.json"
    rules_path.write_text(json.dumps({"rules": [{"id": "phone-mask", "enabled": True}]}), encoding="utf-8")

    app = create_app(services)
    with TestClient(app) as client:
        submitted = client.post(
            "/documents/process",
            json={"file_id": "file-c", "version_id": "v1", "object_key": "source/contact.txt"},
        )
        completed = _wait_for_terminal(client, submitted.json()["id"])
        assert completed["status"] == "completed"

        manifest = client.get("/documents/file-c/versions/v1/artifacts").json()
        cleaning = manifest["cleaning"]
        assert {"id": "phone-mask", "name": "手机号脱敏", "hits": 1} in cleaning["rules"]

        clean = client.get("/documents/file-c/versions/v1/artifacts/clean.md").text
        assert "13812345678" not in clean
        assert "138****5678" in clean


def test_cleaning_rules_endpoint_lists_effective_rules(tmp_path) -> None:
    services = make_services(tmp_path, FakeStorage())
    app = create_app(services)
    with TestClient(app) as client:
        response = client.get("/cleaning/rules")
        assert response.status_code == 200
        payload = response.json()
        rule_ids = {rule["id"] for rule in payload["rules"]}
        assert {"header-footer", "page-number", "phone-mask"} <= rule_ids
        enabled = {rule["id"] for rule in payload["rules"] if rule["enabled"]}
        assert "header-footer" in enabled
        assert "phone-mask" not in enabled
