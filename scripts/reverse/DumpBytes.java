// DumpBytes.java — 转储指定地址区间的原始字节 + 按 CF_CALLBACK_REGISTRATION 解析。
// 用法: -postScript DumpBytes.java <start_hex> <len_dec>
import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.Listing;
import ghidra.program.model.mem.Memory;
import ghidra.program.model.mem.MemoryBlock;
import java.io.PrintWriter;
import java.io.FileWriter;

public class DumpBytes extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] a = getScriptArgs();
        long start = Long.parseUnsignedLong(a[0].toLowerCase().replace("0x", ""), 16);
        int len = Integer.parseInt(a[1]);
        String outPath = System.getenv("PROBE_OUT");
        PrintWriter out = outPath != null ? new PrintWriter(new FileWriter(outPath, true)) : new PrintWriter(System.out, true);
        Memory mem = currentProgram.getMemory();
        Listing listing = currentProgram.getListing();
        Address s = toAddr(start);
        out.println("=== DumpBytes " + s + " len=" + len);
        StringBuilder hex = new StringBuilder();
        for (int i = 0; i < len; i++) {
            try { hex.append(String.format("%02x", mem.getByte(s.add(i)) & 0xff)); }
            catch (Exception e) { hex.append("??"); }
            if (i % 16 == 15) { out.println("  " + String.format("%08x", start + i - 15) + "  " + hex); hex.setLength(0); }
        }
        if (hex.length() > 0) out.println("  (tail) " + hex);
        out.println("  --- as CF_CALLBACK_REGISTRATION (16B: int32 type, int32 pad, void* cb) ---");
        for (int i = 0; i + 16 <= len; i += 16) {
            Address e = s.add(i);
            try {
                int t = mem.getInt(e);
                int pad = mem.getInt(e.add(4));
                long p = mem.getLong(e.add(8));
                Address pa = toAddr(p);
                Function f = mem.contains(pa) ? listing.getFunctionContaining(pa) : null;
                String tn = t == -1 ? "NONE(0xFFFFFFFF)" : Integer.toString(t);
                out.println(String.format("  +0x%02x type=%-18s pad=%d cb=%s %s", i, tn, pad, p == 0 ? "NULL" : pa.toString(), f == null ? "" : f.getName()));
                if (t == -1) break;
            } catch (Exception ex) { out.println("  +0x" + Integer.toHexString(i) + " <err>"); }
        }
        out.flush();
    }
}
