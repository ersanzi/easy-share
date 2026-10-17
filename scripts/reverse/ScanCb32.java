// ScanCb32.java — 在 32 位 PE 里扫描 CF_CALLBACK_REGISTRATION 数组（8 字节/项）。
import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.Listing;
import ghidra.program.model.mem.Memory;
import ghidra.program.model.mem.MemoryBlock;
import java.io.PrintWriter;
import java.io.FileWriter;

public class ScanCb32 extends GhidraScript {
    @Override
    public void run() throws Exception {
        String outPath = System.getenv("PROBE_OUT");
        PrintWriter out = outPath != null ? new PrintWriter(new FileWriter(outPath, true)) : new PrintWriter(System.out, true);
        Memory mem = currentProgram.getMemory();
        Listing listing = currentProgram.getListing();
        out.println("=== ScanCb32 " + currentProgram.getName());
        int found = 0;
        for (MemoryBlock b : mem.getBlocks()) {
            if (!b.isInitialized() || !b.isRead() || b.isExecute()) continue;
            long size = b.getSize();
            Address start = b.getStart();
            for (long off = 0; off + 8 * 3 <= size; off += 4) {
                Address a = start.add(off);
                try {
                    int t0 = mem.getInt(a);
                    long p0 = mem.getInt(a.add(4)) & 0xffffffffL;
                    if (t0 < 0 || t0 > 13) continue;
                    if (p0 == 0) continue;
                    Address pa0 = toAddr(p0);
                    if (!mem.contains(pa0)) continue;
                    MemoryBlock pb0 = mem.getBlock(pa0);
                    if (pb0 == null || !pb0.isExecute()) continue;
                    // 往后走
                    StringBuilder sb = new StringBuilder();
                    int count = 0;
                    long cur = off;
                    boolean term = false;
                    while (cur + 8 <= size && count < 20) {
                        Address ca = start.add(cur);
                        int t = mem.getInt(ca);
                        long p = mem.getInt(ca.add(4)) & 0xffffffffL;
                        if (t == -1) { if (p == 0) term = true; break; }
                        if (t < 0 || t > 13) break;
                        if (p == 0) break;
                        Address pa = toAddr(p);
                        if (!mem.contains(pa)) break;
                        MemoryBlock pb = mem.getBlock(pa);
                        if (pb == null || !pb.isExecute()) break;
                        Function f = listing.getFunctionContaining(pa);
                        sb.append(String.format("        type=%d cb=0x%08x %s%n", t, p, f == null ? "" : f.getName()));
                        count++; cur += 8;
                    }
                    if (count >= 2 && term) {
                        out.println("  TABLE @ " + a + " entries=" + count);
                        out.print(sb.toString());
                        out.flush();
                        found++;
                        off = cur;  // 跳过已识别区域
                    }
                } catch (Exception e) { }
            }
        }
        out.println("  tables found: " + found);
        out.flush();
    }
}
