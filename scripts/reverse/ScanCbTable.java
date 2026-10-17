// ScanCbTable.java — 盲扫 CF_CALLBACK_REGISTRATION 数组（**64 位目标专用**）。
//
// 结构（x64）：16 字节/项 { int32 Type; int32 pad; void* Callback(8B) }，以
//              { CF_CALLBACK_TYPE_NONE(0xFFFFFFFF), NULL } 收尾。
// 结构（x86）：**8 字节/项** { int32 Type; void* Callback(4B) } —— 那种情形请用 ScanCb32.java。
//
// ⚠️ 踩过的坑：先拿本脚本（按 64 位 stride）去扫 32 位的 ksyncengine.dll，**扫不出任何东西**，
//    因为 stride 差一倍。确认目标位数（Ghidra 的 imageBase 与块地址范围可判：0x10000000 段是 32 位，
//    0x140000000 / 0x180000000 段是 64 位）后再选脚本。
//
// 另注：命中的候选**必须过滤 `Unwind@…` 伪符号**——MSVC 的 C++ 异常 unwind 表与回调表同形
//      （小整数 + 指向 .text 的偏移），实测在 32 位目标上 14 个候选里 13 个是 unwind 表。
import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.Address;
import ghidra.program.model.mem.Memory;
import ghidra.program.model.mem.MemoryBlock;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.Listing;
import java.io.PrintWriter;
import java.io.FileWriter;

public class ScanCbTable extends GhidraScript {
    @Override
    public void run() throws Exception {
        String outPath = System.getenv("PROBE_OUT");
        PrintWriter out = outPath != null ? new PrintWriter(new FileWriter(outPath, true)) : new PrintWriter(System.out, true);
        Memory mem = currentProgram.getMemory();
        Listing listing = currentProgram.getListing();
        out.println("=== ScanCbTable on " + currentProgram.getName());
        out.println("  imageBase=" + currentProgram.getImageBase());
        for (MemoryBlock b : mem.getBlocks()) out.println("  block " + b.getName() + " " + b.getStart() + "-" + b.getEnd() + " r=" + b.isRead() + " x=" + b.isExecute());
        out.flush();

        for (MemoryBlock b : mem.getBlocks()) {
            if (!b.isInitialized() || !b.isRead()) continue;
            if (b.isExecute()) continue;
            Address start = b.getStart();
            long size = b.getSize();
            for (long off = 0; off + 16 * 3 <= size; off += 8) {
                Address a = start.add(off);
                try {
                    int t0 = mem.getInt(a);
                    int pad0 = mem.getInt(a.add(4));
                    if (pad0 != 0) continue;
                    if (t0 < 0 || t0 > 12) continue;
                    long p0 = mem.getLong(a.add(8));
                    Address pa0 = toAddr(p0);
                    if (!mem.contains(pa0)) continue;
                    MemoryBlock pb0 = mem.getBlock(pa0);
                    if (pb0 == null || !pb0.isExecute()) continue;
                    // 这是候选首项；往后走项与终结项
                    StringBuilder sb = new StringBuilder();
                    int count = 0;
                    long cur = off;
                    boolean terminated = false;
                    while (cur + 16 <= size) {
                        Address ca = start.add(cur);
                        int t = mem.getInt(ca);
                        int pad = mem.getInt(ca.add(4));
                        long p = mem.getLong(ca.add(8));
                        if (pad != 0) break;
                        if (t == -1) {
                            if (p == 0) { terminated = true; }
                            break;
                        }
                        if (t < 0 || t > 12) break;
                        if (p == 0) break;
                        Address pa = toAddr(p);
                        if (!mem.contains(pa)) break;
                        MemoryBlock pb = mem.getBlock(pa);
                        if (pb == null || !pb.isExecute()) break;
                        Function f = listing.getFunctionContaining(pa);
                        sb.append(String.format("        type=%d cb=%s %s%n", t, pa, f == null ? "(no fn)" : f.getName()));
                        count++;
                        cur += 16;
                        if (count > 24) break;
                    }
                    if (count >= 2 && terminated) {
                        Function f0 = listing.getFunctionContaining(toAddr(p0));
                        out.println("  CANDIDATE @ " + a + "  entries=" + count + "  first=" + (f0 == null ? "?" : f0.getName()));
                        out.println(sb.toString());
                        out.flush();
                    }
                } catch (Exception e) { }
            }
        }
        out.println("  done");
        out.flush();
    }
}
