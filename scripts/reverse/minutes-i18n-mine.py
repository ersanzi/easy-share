import io, re, json, collections

src = io.open(r"C:\Users\lishi\AppData\Local\Temp\shanji\index.chunk.js", encoding="utf-8", errors="replace").read()

pat = re.compile(r'\"([a-zA-Z][a-zA-Z0-9_]{2,60}(?:\.[a-zA-Z0-9_]{2,40}){1,4})\":\"((?:[^\"\\]|\\.){2,240})\"')
unescape = re.compile(r'\\u([0-9a-fA-F]{4})(?:\\u([0-9a-fA-F]{4}))?')

def _dec(m):
    a = int(m.group(1), 16)
    b = int(m.group(2), 16) if m.group(2) else None
    if 0xD800 <= a <= 0xDBFF and b is not None and 0xDC00 <= b <= 0xDFFF:
        return chr(0x10000 + ((a - 0xD800) << 10) + (b - 0xDC00))
    return chr(a) + (chr(b) if b is not None else "")

found = {}
for m in pat.finditer(src):
    k, v = m.group(1), m.group(2)
    v = unescape.sub(_dec, v)
    v = v.replace('\\"', '"').replace("\\/", "/").replace("\\n", " ")
    found.setdefault(k, v)

groups = collections.defaultdict(dict)
for k, v in found.items():
    groups[k.split(".")[0]][k] = v

PREFS = ["mcp", "voicePrint", "hotwords", "todo", "visualization", "summary", "recorder",
         "upload", "detail", "template", "permission", "editor", "speaker", "keyword", "translate", "import"]
out = io.open(r"C:\Users\lishi\AppData\Local\Temp\shanji\i18n_report.txt", "w", encoding="utf-8")
out.write("total_entries %d\n\n" % len(found))
out.write("ALL prefixes: %s\n\n" % sorted(((len(v), p) for p, v in groups.items()), reverse=True))
for pref in PREFS:
    if pref in groups:
        out.write("===== %s (%d) =====\n" % (pref, len(groups[pref])))
        for k in sorted(groups[pref]):
            out.write("  %-44s | %s\n" % (k.replace(pref + ".", ""), groups[pref][k][:110]))
        out.write("\n")

# any key whose value mentions the interesting concepts
targets = ["声纹", "热词", "关键词", "待办", "章节", "切片", "思维导图", "脑图", "实时", "可视化", "模板", "MCP", "配额", "时长", "导入"]
out.write("===== keyword-matched strings across all locales =====\n")
seen = set()
for k, v in sorted(found.items()):
    if any(t in v for t in targets) and k not in seen:
        seen.add(k)
        out.write("  %-46s | %s\n" % (k, v[:110]))
out.close()
print("wrote i18n_report.txt entries_matched", len(seen))
