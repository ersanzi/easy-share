"""
探针：只读查看 web_content.dat（ZIP）的目录结构与 ZIP 级字段，不解密、不写回任何安装目录。
输出：条目数、扩展名分布、是否加密、ZIP 注释、局部头 extra 字段。
"""
import collections
import struct
import sys
import zipfile

path = sys.argv[1]
with zipfile.ZipFile(path) as zf:
    infos = zf.infolist()
    print("entries:", len(infos))
    ext = collections.Counter()
    enc = collections.Counter()
    for i in infos:
        name = i.filename
        c = "." + name.rsplit(".", 1)[-1].lower() if "." in name.rsplit("/", 1)[-1] else "(none)"
        ext[c] += 1
        enc[bool(i.flag_bits & 0x1)] += 1
    print("encrypted:", dict(enc))
    print("ext:", ext.most_common(20))
    print("comment:", zf.comment[:80])
    names = [i.filename for i in infos]
    print("sample names:")
    for n in names[:12]:
        print("  ", n)
    for i in infos:
        if i.filename.lower().endswith((".json", ".pak", ".manifest", ".txt")):
            print("plain-entry:", i.filename, i.file_size, "enc=", bool(i.flag_bits & 1))

data = open(path, "rb").read()
# 局部文件头（PK\x03\x04）的 extensible 标志与 extra 字段
pos = 0
shown = 0
while shown < 8:
    pos = data.find(b"PK\x03\x04", pos)
    if pos < 0:
        break
    (sig, ver, flags, method, mtime, mdate, crc, csize, usize, nlen, elen) = struct.unpack("<IHHHHHIIIHH", data[pos:pos + 30])
    name = data[pos + 30:pos + 30 + nlen].decode("utf-8", "replace")
    extra = data[pos + 30 + nlen:pos + 30 + nlen + elen]
    print(f"local {name[:60]:60s} flags=0x{flags:04x} method={method} extra={extra[:40].hex()}")
    pos += 4
    shown += 1
