"""知识文件权限适配器：控制面实时裁决，来源绑定只存身份，不缓存授权。"""
from __future__ import annotations

import hashlib
import json
import logging
import re
import urllib.request

from fastapi import HTTPException

from app.rag.permissions import can_view_document

_log = logging.getLogger(__name__)


def managed_key(key: str | None) -> bool:
    # 可疑别名也算受管，绝不因为斜杠/大小写异常降级成共享文档。
    normalized = (key or "").strip().replace("\\", "/").lstrip("/").lower()
    return normalized in ("shared", "users") or normalized.startswith(("shared/", "users/"))


class KnowledgeAccess:
    def __init__(self, services, request):
        self.services = services
        self.user = getattr(request.state, "user", None)
        self.token = getattr(request.state, "control_plane_token", None)

    def _call(self, operation, body):
        sso = self.services.sso
        if not sso or not self.token or (self.user or {}).get("authority") != "ruoyi":
            raise HTTPException(403, "网盘来源需要控制面登录和可信绑定")
        request = urllib.request.Request(f"{sso.base_url}/easyshare/knowledge/acl/{operation}",
            data=json.dumps(body).encode(), headers={"Authorization": f"Bearer {self.token}",
            "clientid": sso.client_id, "Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=sso.timeout_seconds) as response:
                payload = json.loads(response.read())
            if not isinstance(payload, dict) or payload.get("code") != 200:
                raise ValueError("authorization rejected")
            return payload["data"]
        except (OSError, ValueError, KeyError, TypeError) as exc:
            _log.warning("知识来源授权不可用，受管文档拒绝访问")
            raise HTTPException(403, "网盘权限不可确认，请稍后重试或重新入库") from exc

    def bind(self, *, file_id=None, version_id=None, object_key=None, action="download"):
        body = {"objectKey": object_key, "action": action} if object_key is not None else {
            "fileId": file_id, "expectedVersionId": version_id}
        raw = self._call("bind", body)
        try:
            binding = {"authority": raw["authority"], "file_id": str(raw["fileId"]),
                "version_id": str(raw["versionId"]), "object_key": raw["objectKey"], "bucket": raw["bucket"],
                "source_sha256": raw["sourceSha256"], "source_bytes": raw["sourceBytes"]}
            if (binding["authority"] != "ruoyi" or not re.fullmatch(r"[1-9][0-9]{0,18}", binding["file_id"])
                or not re.fullmatch(r"[0-9]+", binding["version_id"])
                or not re.fullmatch(r"[a-f0-9]{64}", binding["source_sha256"])
                or not isinstance(binding["source_bytes"], int) or binding["source_bytes"] < 0
                or binding["bucket"] != self.services.config.rustfs_bucket
                or not managed_key(binding["object_key"])
                or (file_id is not None and (binding["file_id"] != file_id or binding["version_id"] != version_id))
                or (object_key is not None and binding["object_key"] != object_key)):
                raise ValueError("invalid binding")
            return binding, raw.get("filename")
        except (ValueError, KeyError, TypeError) as exc:
            raise HTTPException(403, "来源绑定不匹配，请重新入库") from exc

    def freeze(self, binding, file_id, version_id):
        key = f"derived/{file_id}/{version_id}/source"
        content = self.services.storage.read(binding["object_key"], max_bytes=self.services.config.max_source_bytes)
        if len(content) != binding["source_bytes"] or hashlib.sha256(content).hexdigest() != binding["source_sha256"]:
            raise HTTPException(409, "网盘正文已变化，请重新入库")
        self.services.storage.write(key, content, content_type="application/octet-stream")
        return key

    def binding_of(self, document):
        binding = document.get("source_binding") or self.services.job_store.source_binding(
            document.get("file_id") or document.get("doc_id"), document.get("version_id"))
        if binding and document.get("source_sha256") and document["source_sha256"] != binding.get("source_sha256"):
            raise HTTPException(403, "正文与可信来源绑定不一致，请重新入库")
        return binding

    def migrate(self, document):
        file_id = document.get("file_id") or document.get("doc_id")
        version_id = document.get("version_id")
        if not file_id or not version_id:
            raise HTTPException(403, "旧知识来源无法证明，请重新入库")
        manifest = self.services.pipeline.read_manifest(file_id, version_id)
        binding, _ = self.bind(object_key=manifest.get("object_key"), action="read")
        if (manifest.get("source_sha256") != binding["source_sha256"]
            or manifest.get("source_bytes") != binding["source_bytes"]):
            raise HTTPException(403, "旧正文版本无法证明，请重新入库")
        snapshot = self.freeze(binding, file_id, version_id)
        manifest.update(source_binding=binding, source_snapshot_key=snapshot)
        self.services.storage.write(f"derived/{file_id}/{version_id}/manifest.json",
            json.dumps(manifest, ensure_ascii=False).encode(), content_type="application/json")
        job = self.services.job_store.find_latest(file_id=file_id, version_id=version_id)
        if job:
            self.services.job_store.pin_source(job.id, binding, snapshot)
        document.update(manifest)
        return binding

    def decisions(self, documents, action="read"):
        results = [False] * len(documents)
        bound = {}
        for index, document in enumerate(documents):
            binding = self.binding_of(document)
            if binding:
                if binding.get("authority") == "ruoyi":
                    bound[index] = binding
            elif managed_key(document.get("object_key")):
                # 旧受管正文必须先证明内容来源，不能按今天同路径的另一个文件认领。
                try:
                    bound[index] = self.migrate(document)
                except (HTTPException, OSError, ValueError, KeyError, TypeError):
                    _log.warning("旧知识来源尚未核验，需重新入库: %s", document.get("file_id"))
            else:
                results[index] = can_view_document(document, self.user)
        if bound:
            try:
                ids = sorted({binding["file_id"] for binding in bound.values()})
                rows = []
                for start in range(0, len(ids), 200):
                    rows.extend(self._call("access", {"fileIds": ids[start:start + 200]}))
                by_id = {row["fileId"]: row for row in rows if isinstance(row, dict)}
                for index, binding in bound.items():
                    results[index] = by_id.get(binding["file_id"], {}).get(action) is True
            except (HTTPException, KeyError, TypeError):
                pass  # 所有故障保持拒绝；无 ACL 缓存或本地管理员后门。
        return results

    def require(self, document, action="read"):
        if not self.decisions([document], action)[0]:
            raise HTTPException(403, "无权下载原件" if action == "download" else "无权查看该文档")
