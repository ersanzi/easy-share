from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from app.parsing.extractor import parse_document
from app.parsing.renderer import render_markdown
from tests.golden.builders import build_case

CASES_PATH = Path(__file__).with_name("cases.json")
CASES: list[dict[str, Any]] = json.loads(CASES_PATH.read_text(encoding="utf-8"))


def _assert_expected_block(actual: dict[str, Any], expected: dict[str, Any]) -> None:
    for key, expected_value in expected.items():
        assert actual.get(key) == expected_value, f"字段 {key} 不符合黄金预期"


@pytest.mark.parametrize("case", CASES, ids=lambda case: case["filename"])
def test_office_golden_document_structure(case: dict[str, Any]) -> None:
    content = build_case(case["builder"])
    document = parse_document(case["filename"], content)
    actual = document.to_dict()
    expected = case["expected"]

    assert actual["media_type"] == expected["media_type"]
    assert actual["metadata"] == expected["metadata"]
    assert actual["warnings"] == expected["warnings"]
    assert [block["id"] for block in actual["blocks"]] == [
        f"b{index}" for index in range(1, len(expected["blocks"]) + 1)
    ]
    assert len(actual["blocks"]) == len(expected["blocks"])
    for actual_block, expected_block in zip(actual["blocks"], expected["blocks"]):
        _assert_expected_block(actual_block, expected_block)

    markdown = render_markdown(document)
    for snippet in expected["markdown_contains"]:
        assert snippet in markdown


@pytest.mark.parametrize("case", CASES, ids=lambda case: case["filename"])
def test_golden_document_generation_is_semantically_repeatable(case: dict[str, Any]) -> None:
    first = parse_document(case["filename"], build_case(case["builder"])).to_dict()
    second = parse_document(case["filename"], build_case(case["builder"])).to_dict()
    assert first == second