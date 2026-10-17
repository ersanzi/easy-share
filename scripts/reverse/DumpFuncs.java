// DumpFuncs.java — 批量反编译给定函数地址。
// 用法: -postScript DumpFuncs.java <addr1> [addr2] ...
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.DecompInterface;
import ghidra.app.decompiler.DecompileResults;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.Listing;
import ghidra.util.task.ConsoleTaskMonitor;
import java.io.PrintWriter;
import java.io.FileWriter;

public class DumpFuncs extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] args = getScriptArgs();
        String outPath = System.getenv("PROBE_OUT");
        PrintWriter out = outPath != null ? new PrintWriter(new FileWriter(outPath, true)) : new PrintWriter(System.out, true);
        Listing listing = currentProgram.getListing();
        DecompInterface di = new DecompInterface();
        di.toggleCCode(true);
        di.openProgram(currentProgram);
        for (String a : args) {
            Address addr = toAddr(Long.parseUnsignedLong(a.toLowerCase().replace("0x", ""), 16));
            Function f = listing.getFunctionAt(addr);
            if (f == null) f = listing.getFunctionContaining(addr);
            out.println("=== DumpFuncs " + addr + " -> " + (f == null ? "NULL" : f.getName() + "@" + f.getEntryPoint()));
            if (f == null) { out.flush(); continue; }
            DecompileResults res = di.decompileFunction(f, 180, new ConsoleTaskMonitor());
            out.println(res != null && res.decompileCompleted() ? res.getDecompiledFunction().getC() : "(decompile failed)");
            out.println("=== end ===");
            out.flush();
        }
        out.flush();
        di.dispose();
    }
}
