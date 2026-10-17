// FuncCallers.java — 找调用指定函数（或引用其地址）的所有位置与所在函数，并反编译调用者。
// 用法: -postScript FuncCallers.java <addr_hex|name> [<addr2>...]
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.DecompInterface;
import ghidra.app.decompiler.DecompileResults;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.Listing;
import ghidra.program.model.symbol.Reference;
import ghidra.program.model.symbol.ReferenceIterator;
import ghidra.util.task.ConsoleTaskMonitor;
import java.io.PrintWriter;
import java.io.FileWriter;
import java.util.LinkedHashSet;
import java.util.Set;

public class FuncCallers extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] args = getScriptArgs();
        String outPath = System.getenv("PROBE_OUT");
        PrintWriter out = outPath != null ? new PrintWriter(new FileWriter(outPath, true)) : new PrintWriter(System.out, true);
        Listing listing = currentProgram.getListing();
        Set<Function> callers = new LinkedHashSet<>();
        for (String a : args) {
            Address addr = a.toLowerCase().startsWith("0x") ? toAddr(Long.parseUnsignedLong(a.substring(2), 16)) : toAddr(a);
            out.println("=== FuncCallers target " + addr + " name=" + listing.getFunctionAt(addr));
            ReferenceIterator refs = currentProgram.getReferenceManager().getReferencesTo(addr);
            int n = 0;
            while (refs.hasNext()) {
                Reference r = refs.next();
                Function f = listing.getFunctionContaining(r.getFromAddress());
                out.println("  REF from " + r.getFromAddress() + " type=" + r.getReferenceType() + " in " + (f == null ? "<none>" : f.getName() + "@" + f.getEntryPoint()));
                if (f != null) callers.add(f);
                n++;
            }
            if (n == 0) out.println("  (no refs)");
            out.println("  total refs=" + n);
            out.flush();
        }
        DecompInterface di = new DecompInterface();
        di.toggleCCode(true);
        di.openProgram(currentProgram);
        for (Function f : callers) {
            DecompileResults res = di.decompileFunction(f, 120, new ConsoleTaskMonitor());
            out.println("  --- caller " + f.getName() + "@" + f.getEntryPoint() + " ---");
            out.println(res != null && res.decompileCompleted() ? res.getDecompiledFunction().getC() : "(decompile failed)");
            out.println("  --- end ---");
            out.flush();
        }
        out.flush();
        di.dispose();
    }
}
