// FieldUsers.java — 找「访问某结构体偏移」的函数：扫指令操作数里出现目标位移值的那些函数，
// 并过滤只保留函数体内含指定关键字（如 conflict）的。用于定位 `<struct>+0xb4` 的读取点。
// 用法: -postScript FieldUsers.java <disp_hex> <keyword1> [keyword2] ...
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
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;

public class FieldUsers extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] args = getScriptArgs();
        long disp = Long.parseUnsignedLong(args[0].toLowerCase().replace("0x", ""), 16);
        Set<String> fns = new LinkedHashSet<>();
        Listing listing = currentProgram.getListing();
        FunctionIterator it = listing.getFunctions(true);
        List<Function> cands = new ArrayList<>();
        while (it.hasNext()) {
            Function f = it.next();
            boolean hit = false;
            InstructionIterator ins = listing.getInstructions(f.getBody(), true);
            while (ins.hasNext() && !hit) {
                Instruction i = ins.next();
                for (int k = 0; k < i.getNumOperands() && !hit; k++) {
                    for (Object o : i.getOpObjects(k)) {
                        if (o instanceof Scalar) {
                            long v = ((Scalar) o).getUnsignedValue();
                            if (v == disp) { hit = true; fns.add(f.getName() + "@" + f.getEntryPoint()); cands.add(f); break; }
                        }
                    }
                }
            }
        }
        String outPath = System.getenv("PROBE_OUT");
        PrintWriter out = outPath != null ? new PrintWriter(new FileWriter(outPath, true)) : new PrintWriter(System.out, true);
        out.println("=== FieldUsers disp=0x" + Long.toHexString(disp) + " funcs=" + fns.size() + " args=" + java.util.Arrays.toString(args));
        DecompInterface di = new DecompInterface();
        di.toggleCCode(true);
        di.openProgram(currentProgram);
        for (Function f : cands) {
            DecompileResults res = di.decompileFunction(f, 120, new ConsoleTaskMonitor());
            if (res == null || !res.decompileCompleted()) continue;
            String c = res.getDecompiledFunction().getC();
            if (c == null) continue;
            // 只保留与参数关键字相关的
            boolean keep = false;
            for (int i = 1; i < args.length; i++) if (c.contains(args[i])) { keep = true; break; }
            if (!keep) continue;
            out.println("  --- " + f.getName() + "@" + f.getEntryPoint() + " ---");
            out.println(c);
            out.println("  --- end ---");
            out.flush();
        }
        out.flush();
        di.dispose();
    }
}
