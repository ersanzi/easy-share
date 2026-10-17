// ConstRefs.java — 扫描 .text 指令流，找「立即数 == 目标值」的指令及其所在函数，并对命中函数反编译。
// 用途：定位 partSizeOfClient 之类的数值常量（字符串里没有，只能在代码里）。
// 用法: -postScript ConstRefs.java <v1> [v2] ...   支持十进制或 0x 十六进制
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.DecompInterface;
import ghidra.app.decompiler.DecompileResults;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.FunctionIterator;
import ghidra.program.model.listing.Instruction;
import ghidra.program.model.listing.InstructionIterator;
import ghidra.program.model.listing.Listing;
import ghidra.program.model.scalar.Scalar;
import ghidra.util.task.ConsoleTaskMonitor;
import java.io.PrintWriter;
import java.io.FileWriter;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;

public class ConstRefs extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] args = getScriptArgs();
        String outPath = System.getenv("PROBE_OUT");
        PrintWriter out = outPath != null
            ? new PrintWriter(new FileWriter(outPath, true))
            : new PrintWriter(System.out, true);

        Map<Long, String> targets = new LinkedHashMap<>();
        for (String a : args) {
            if (a.isEmpty()) continue;
            long v = a.toLowerCase().startsWith("0x")
                ? Long.parseUnsignedLong(a.substring(2), 16)
                : Long.parseLong(a);
            targets.put(v, a);
        }
        out.println("=== ConstRefs targets " + targets);
        out.flush();

        Listing listing = currentProgram.getListing();
        Map<String, Set<String>> hits = new LinkedHashMap<>();
        long scanned = 0;
        FunctionIterator fns = listing.getFunctions(true);
        while (fns.hasNext()) {
            Function f = fns.next();
            String fnKey = f.getName() + "@" + f.getEntryPoint();
            InstructionIterator ins = listing.getInstructions(f.getBody(), true);
            while (ins.hasNext()) {
                Instruction i = ins.next();
                scanned++;
                int n = i.getNumOperands();
                for (int k = 0; k < n; k++) {
                    for (Object o : i.getOpObjects(k)) {
                        if (!(o instanceof Scalar)) continue;
                        long val = ((Scalar) o).getUnsignedValue();
                        if (val == 0L || !targets.containsKey(val)) continue;
                        String tag = "0x" + Long.toHexString(val) + " (" + targets.get(val) + ")";
                        hits.computeIfAbsent(fnKey, x -> new HashSet<>()).add(tag);
                    }
                }
            }
        }
        out.println("  scanned instructions: " + scanned + "  hit functions: " + hits.size());
        for (Map.Entry<String, Set<String>> e : hits.entrySet()) {
            out.println("  HITFN " + e.getKey() + "  <- " + e.getValue());
        }
        out.flush();

        if (hits.isEmpty()) { out.flush(); return; }

        DecompInterface di = new DecompInterface();
        di.toggleCCode(true);
        di.openProgram(currentProgram);
        Set<String> done = new HashSet<>();
        FunctionIterator fns2 = listing.getFunctions(true);
        while (fns2.hasNext()) {
            Function f = fns2.next();
            String fnKey = f.getName() + "@" + f.getEntryPoint();
            if (!hits.containsKey(fnKey) || !done.add(fnKey)) continue;
            DecompileResults res = di.decompileFunction(f, 120, new ConsoleTaskMonitor());
            out.println("  --- pseudocode " + fnKey + " ---");
            if (res != null && res.decompileCompleted()) {
                out.println(res.getDecompiledFunction().getC());
            } else {
                out.println("  (decompile failed)");
            }
            out.println("  --- end " + fnKey + " ---");
            out.flush();
        }
        di.dispose();
        out.flush();
    }
}
