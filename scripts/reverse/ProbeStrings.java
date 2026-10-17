// ProbeStrings.java — 给定若干字符串针，打印其地址、全部 xref，以及引用函数的反编译伪码。
// 用法: -postScript ProbeStrings.java <needle1> [needle2] ...
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.DecompInterface;
import ghidra.app.decompiler.DecompileResults;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Data;
import ghidra.program.model.listing.DataIterator;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.Listing;
import ghidra.program.model.symbol.Reference;
import ghidra.program.model.symbol.ReferenceIterator;
import ghidra.program.model.symbol.ReferenceManager;
import ghidra.util.task.ConsoleTaskMonitor;
import java.io.PrintWriter;
import java.io.FileWriter;

public class ProbeStrings extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] needles = getScriptArgs();
        String outPath = System.getenv("PROBE_OUT");
        PrintWriter out = outPath != null
            ? new PrintWriter(new FileWriter(outPath, true))
            : new PrintWriter(System.out, true);

        DecompInterface di = new DecompInterface();
        di.toggleCCode(true);
        di.toggleSyntaxTree(true);
        di.openProgram(currentProgram);

        Listing listing = currentProgram.getListing();
        ReferenceManager rm = currentProgram.getReferenceManager();

        for (String needle : needles) {
            if (needle.isEmpty()) continue;
            out.println("=== NEEDLE: " + needle);
            DataIterator it = listing.getDefinedData(true);
            int found = 0;
            while (it.hasNext()) {
                Data d = it.next();
                Object v;
                try {
                    v = d.getValue();
                } catch (Exception e) {
                    continue;
                }
                if (!(v instanceof String)) continue;
                String s = (String) v;
                if (!s.contains(needle)) continue;
                found++;
                Address a = d.getAddress();
                out.println("  DATA @" + a + " len=" + s.length());
                ReferenceIterator refs = rm.getReferencesTo(a);
                boolean any = false;
                while (refs.hasNext()) {
                    any = true;
                    Reference r = refs.next();
                    Address from = r.getFromAddress();
                    Function f = listing.getFunctionContaining(from);
                    out.println("    XREF from " + from + " type=" + r.getReferenceType()
                        + " fn=" + (f == null ? "<none>" : f.getName() + "@" + f.getEntryPoint()));
                    if (f == null) continue;
                    DecompileResults res = di.decompileFunction(f, 120, new ConsoleTaskMonitor());
                    if (res != null && res.decompileCompleted()) {
                        String c = res.getDecompiledFunction().getC();
                        out.println("    --- pseudocode of " + f.getName() + " ---");
                        out.println(c);
                        out.println("    --- end ---");
                    }
                }
                if (!any) out.println("    (no xrefs)");
            }
            if (found == 0) out.println("  (string not found in defined data)");
            out.flush();
        }
        out.flush();
        di.dispose();
    }
}
