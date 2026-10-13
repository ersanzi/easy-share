import io, re

src = io.open(r"C:\Users\lishi\AppData\Local\Temp\shanji\index.chunk.js", encoding="utf-8", errors="replace").read()
out = io.open(r"C:\Users\lishi\AppData\Local\Temp\shanji\enums_report.txt", "w", encoding="utf-8")

IDS = ["KeywordsStatus", "FullTextSummaryStatus", "SeparateStatus", "ChapterCoverState",
       "ExtractStatus", "TranscribeOrganizingStatus", "AudioSegmentsStatus", "SpeakerRecommendStatus",
       "MinutesRecordStatusChange", "TaskServerStatus", "FileImportStatus", "MediaStatus"]

for ident in IDS:
    hits = [m.start() for m in re.finditer(r'\b%s\b' % ident, src)]
    out.write("\n##### %s  occurrences=%d\n" % (ident, len(hits)))
    # prefer a definition-like context (contains ':' mapping)
    shown = 0
    for h in hits:
        seg = src[max(0, h - 60): h + 420]
        if re.search(r'[A-Za-z_]+\s*[:=]\s*["\d]', seg):
            out.write("  --- @%d\n  %s\n" % (h, seg.replace("\n", " ")))
            shown += 1
            if shown >= 2:
                break
    if shown == 0 and hits:
        h = hits[0]
        out.write("  (no mapping found) @%d\n  %s\n" % (h, src[max(0, h - 60):h + 320].replace("\n", " ")))

# status string literals used with these fields
out.write("\n##### status-ish string literals (SCREAMING_SNAKE)\n")
lits = re.findall(r'"([A-Z][A-Z0-9_]{3,28})"', src)
from collections import Counter
interesting = [k for k, c in Counter(lits).most_common(400) if re.search(r'(TRANSCRIB|SUMMAR|KEYWORD|SEPARAT|SPEAKER|CHAPTER|RECORD|MINUTE|UPLOAD|PROCESS|FINISH|ORGANIZ|EXPORT)', k)]
out.write(", ".join("%s(%d)" % (k, Counter(lits)[k]) for k in interesting) + "\n")

# websocket / long-connection topics
out.write("\n##### wss / long-connection topics\n")
for m in re.finditer(r'(webalfa[^"\']{0,40}|"\/long"|longConnect|lwp|LWP)', src):
    pass
out.write("lwp mentions: %d\n" % len(re.findall(r'\blwp\b', src, re.I)))
out.write("Adaptor rpc calls: \n  " + "\n  ".join(sorted(set(re.findall(r'/r/Adaptor/[A-Za-z0-9_]+/[A-Za-z0-9_]+', src)))) + "\n")
out.close()
print("wrote enums_report.txt")
