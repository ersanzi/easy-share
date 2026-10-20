"""
探针：判断 web_content.dat 里条目的加密方式。
只读安装目录，不落盘、不解密商业资源；结论只用来决定取证路线。

做法：取一个条目（11 字节加密头的第 12 字节按传统 ZipCrypto 规则取决于
"使用数据描述符"标志），若加密头后解出的字节能形成合法 HTML 开头（'<!' '<h' '<b'），
则是标准 ZipCrypto；否则是自定义加密。
"""
import struct
import sys

path = sys.argv[1]
target = sys.argv[2] if len(sys.argv) > 2 else "component_sample.html"
data = open(path, "rb").read()


def find_entry(name):
    pos = 0
    while True:
        pos = data.find(b"PK\x03\x04", pos)
        if pos < 0:
            return None
        sig, ver, flags, method, mt, md, crc, csz, usz, nlen, elen = struct.unpack(
            "<IHHHHHIIIHH", data[pos:pos + 30])
        fname = data[pos + 30:pos + 30 + nlen].decode("utf-8", "replace")
        if fname == name:
            start = pos + 30 + nlen + elen
            return flags, method, crc, data[start:start + csz]
        pos += 4


def crc32_update(crc, byte):
    crc = (crc ^ byte) & 0xFFFFFFFF
    for _ in range(8):
        crc = (crc >> 1) ^ (0xEDB88320 if crc & 1 else 0)
    return crc


def decrypt(payload, check_byte):
    keys = [0x12345678, 0x23456789, 0x34567890]
    body = bytearray(payload)
    body[11] = check_byte
    out = bytearray()
    for b in body:
        keys[0] = crc32_update(keys[0], b)
        o = (b ^ ((keys[2] >> 8) & 0xFF)) & 0xFF
        keys[1] = (keys[1] + o) & 0xFFFFFFFF
        keys[1] = (keys[1] * 134775813 + 1) & 0xFFFFFFFF
        keys[2] = crc32_update(keys[2], (keys[1] >> 24) & 0xFF)
        out.append(o)
    return bytes(out)


entry = find_entry(target)
if not entry:
    print("entry not found:", target)
    sys.exit(1)
flags, method, crc, payload = entry
print(f"entry={target} flags=0x{flags:04x} method={method} size={len(payload)} crc={crc:08x}")
print("first16", payload[:16].hex())

hits = []
for check in range(256):
    head = decrypt(payload, check)[12:16]
    if head[:1] in (b"<", b"{") or head[:2] in (b"\xef\xbb", b"\x1f\x8b"):
        hits.append((check, head))
print("plausible plaintext heads:", hits[:6] or "none -> 自定义加密（非标准 ZipCrypto）")
