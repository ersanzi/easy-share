"""按空间前缀统计 RustFS 桶用量。

与管理页的用量显示互为参照：本脚本**直连 RustFS**，不经控制面，因此在控制面停机时仍可用，
也可用来核对管理页的数字是否可信（管理页的值来自控制面聚合 + 60s 缓存）。

凭据从环境变量读，不打印密钥：
    set -a && . <(sed 's/^\\xEF\\xBB\\xBF//' deploy/rustfs/.env) && set +a
    export RUSTFS_ENDPOINT=http://127.0.0.1:9000 RUSTFS_BUCKET=easyshare
    python scripts/bucket-usage.py

注意 .env 可能带 UTF-8 BOM，直接 source 会让第一行解析失败、凭据变空——上面的 sed 是必需的。
"""
import datetime
import hashlib
import hmac
import os
import sys
import urllib.request
import xml.etree.ElementTree as ET

AK = os.environ["RUSTFS_ACCESS_KEY"]
SK = os.environ["RUSTFS_SECRET_KEY"]
ENDPOINT = os.environ.get("RUSTFS_ENDPOINT", "http://127.0.0.1:9000").rstrip("/")
BUCKET = os.environ.get("RUSTFS_BUCKET", "easyshare")
REGION = "us-east-1"
HOST = ENDPOINT.split("://", 1)[1]
NS = "{http://s3.amazonaws.com/doc/2006-03-01/}"


def sign(key, msg):
    return hmac.new(key, msg.encode(), hashlib.sha256).digest()


def request(query):
    now = datetime.datetime.now(datetime.timezone.utc)
    stamp = now.strftime("%Y%m%dT%H%M%SZ")
    date = now.strftime("%Y%m%d")
    payload = hashlib.sha256(b"").hexdigest()
    canonical_query = "&".join(f"{k}={urllib.parse.quote(v, safe='')}" for k, v in sorted(query.items()))
    canonical = (
        f"GET\n/{BUCKET}\n{canonical_query}\n"
        f"host:{HOST}\nx-amz-content-sha256:{payload}\nx-amz-date:{stamp}\n\n"
        f"host;x-amz-content-sha256;x-amz-date\n{payload}"
    )
    scope = f"{date}/{REGION}/s3/aws4_request"
    to_sign = f"AWS4-HMAC-SHA256\n{stamp}\n{scope}\n{hashlib.sha256(canonical.encode()).hexdigest()}"
    k = sign(sign(sign(sign(f"AWS4{SK}".encode(), date), REGION), "s3"), "aws4_request")
    signature = hmac.new(k, to_sign.encode(), hashlib.sha256).hexdigest()
    req = urllib.request.Request(
        f"{ENDPOINT}/{BUCKET}?{canonical_query}",
        headers={
            "Host": HOST,
            "x-amz-content-sha256": payload,
            "x-amz-date": stamp,
            "Authorization": (
                f"AWS4-HMAC-SHA256 Credential={AK}/{scope}, "
                f"SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature={signature}"
            ),
        },
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return ET.fromstring(resp.read())


def main():
    usage, token, total_objects = {}, None, 0
    while True:
        query = {"list-type": "2", "max-keys": "1000"}
        if token:
            query["continuation-token"] = token
        root = request(query)
        for item in root.findall(f"{NS}Contents"):
            key = item.findtext(f"{NS}Key")
            size = int(item.findtext(f"{NS}Size") or 0)
            total_objects += 1
            parts = key.split("/")
            prefix = "/".join(parts[:2]) + "/" if len(parts) > 2 else "(根)"
            slot = usage.setdefault(prefix, [0, 0])
            slot[0] += 1
            slot[1] += size
        if root.findtext(f"{NS}IsTruncated") == "true":
            token = root.findtext(f"{NS}NextContinuationToken")
        else:
            break
    print(f"桶 {BUCKET} 共 {total_objects} 个对象")
    for prefix in sorted(usage):
        count, size = usage[prefix]
        print(f"  {prefix:40s} {count:6d} 个  {size / 1024 / 1024:10.2f} MB")


if __name__ == "__main__":
    sys.exit(main())
