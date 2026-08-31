# 拖放收不到时的根因诊断：比对本机「能拖入」的桌面挂件与 EasyShare 浮窗。
#
# 为什么要这么查：拖放失败往往毫无报错——没有事件、没有日志、没有异常。
# 光改接收代码是猜；要先确认「窗口是否有资格成为放置目标」。
#
# 查三件事，按可能性排序：
#   1. 完整性级别（UIPI）：提权进程收不到来自非提权 explorer 的拖放，且完全静默。
#   2. WS_EX_NOACTIVATE：不可激活的窗口不会被当作放置目标。
#   3. 窗口层级：拖放落在最上层子窗口上，父窗口注册了也没用。
#
#   powershell -NoProfile -File scripts/diagnose-drop.ps1

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

Add-Type -TypeDefinition @'
using System;
using System.Text;
using System.Collections.Generic;
using System.Runtime.InteropServices;

public class DropDiag {
    delegate bool EnumProc(IntPtr h, IntPtr p);
    [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc cb, IntPtr p);
    [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr parent, EnumProc cb, IntPtr p);
    [DllImport("user32.dll")] static extern int GetClassNameW(IntPtr h, StringBuilder s, int n);
    [DllImport("user32.dll")] static extern IntPtr GetWindowLongPtrW(IntPtr h, int i);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
    [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);

    [DllImport("advapi32.dll", SetLastError = true)]
    static extern bool OpenProcessToken(IntPtr h, uint access, out IntPtr token);
    [DllImport("advapi32.dll", SetLastError = true)]
    static extern bool GetTokenInformation(IntPtr token, uint cls, IntPtr info, uint len, out uint ret);
    [DllImport("kernel32.dll")] static extern IntPtr OpenProcess(uint access, bool inherit, uint pid);
    [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);

    // 完整性级别：拖放的源与目标必须相容。
    // 目标（我们的窗口）级别高于源（explorer）时，系统静默丢弃拖放。
    public static string IntegrityLevel(uint pid) {
        IntPtr proc = OpenProcess(0x1000, false, pid); // PROCESS_QUERY_LIMITED_INFORMATION
        if (proc == IntPtr.Zero) return "无法打开进程";
        try {
            IntPtr token;
            if (!OpenProcessToken(proc, 0x0008, out token)) return "无法打开令牌"; // TOKEN_QUERY
            try {
                uint size = 0;
                GetTokenInformation(token, 25, IntPtr.Zero, 0, out size); // TokenIntegrityLevel
                if (size == 0) return "查询失败";
                IntPtr buf = Marshal.AllocHGlobal((int)size);
                try {
                    if (!GetTokenInformation(token, 25, buf, size, out size)) return "查询失败";
                    IntPtr sid = Marshal.ReadIntPtr(buf);
                    int count = Marshal.ReadByte(sid, 1);
                    int rid = Marshal.ReadInt32(sid, 8 + (count - 1) * 4);
                    if (rid >= 0x4000) return "System(0x4000)";
                    if (rid >= 0x3000) return "High(高/已提权)";
                    if (rid >= 0x2000) return "Medium(中/普通)";
                    if (rid >= 0x1000) return "Low(低)";
                    return "Untrusted";
                } finally { Marshal.FreeHGlobal(buf); }
            } finally { CloseHandle(token); }
        } finally { CloseHandle(proc); }
    }

    public class WinRow {
        public string Owner; public string Cls; public string ExStyle;
        public bool NoActivate; public bool Visible; public string Level; public bool IsChild;
    }

    public static List<WinRow> Inspect(string[] procNames) {
        var rows = new List<WinRow>();
        EnumWindows((h, p) => {
            uint pid; GetWindowThreadProcessId(h, out pid);
            string owner = null;
            try { owner = System.Diagnostics.Process.GetProcessById((int)pid).ProcessName; } catch { return true; }
            bool want = false;
            foreach (var n in procNames) if (string.Equals(owner, n, StringComparison.OrdinalIgnoreCase)) want = true;
            if (!want) return true;

            rows.Add(Describe(h, owner, pid, false));
            EnumChildWindows(h, (ch, cp) => {
                uint cpid; GetWindowThreadProcessId(ch, out cpid);
                string co = owner;
                try { co = System.Diagnostics.Process.GetProcessById((int)cpid).ProcessName; } catch {}
                rows.Add(Describe(ch, co, cpid, true));
                return true;
            }, IntPtr.Zero);
            return true;
        }, IntPtr.Zero);
        return rows;
    }

    static WinRow Describe(IntPtr h, string owner, uint pid, bool isChild) {
        var sb = new StringBuilder(256);
        GetClassNameW(h, sb, 256);
        int ex = (int)GetWindowLongPtrW(h, -20);
        return new WinRow {
            Owner = owner,
            Cls = sb.ToString(),
            ExStyle = "0x" + ex.ToString("X8"),
            NoActivate = (ex & 0x08000000) != 0,
            Visible = IsWindowVisible(h),
            Level = IntegrityLevel(pid),
            IsChild = isChild,
        };
    }
}
'@ -Language CSharp

# 关注：我们自己、explorer（拖放的源）、以及本机能正常拖入的挂件
$targets = @('easyshare', 'explorer', 'Nexus', 'PowerToys.QuickAccess')

Write-Output '=== 进程完整性级别（拖放的先决条件）==='
foreach ($n in $targets) {
    Get-Process $n -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Output ("  {0,-24} pid={1,-6} {2}" -f $_.ProcessName, $_.Id, [DropDiag]::IntegrityLevel([uint32]$_.Id))
    }
}

Write-Output ''
Write-Output '=== 窗口样式（只列顶层与可见子窗口）==='
[DropDiag]::Inspect($targets) |
    Where-Object { -not $_.IsChild -or $_.Visible } |
    Select-Object Owner, Cls, ExStyle, NoActivate, Visible, IsChild |
    Format-Table -AutoSize | Out-String -Width 220 | Write-Output

Write-Output '判读：'
Write-Output '  目标进程的完整性级别高于 explorer 时，拖放被系统静默丢弃（UIPI）——这是最隐蔽的一种。'
Write-Output '  NOACTIVATE=True 的窗口不会被当作放置目标。'
Write-Output '  能正常拖入的挂件（Nexus 等）的取值即为参照答案。'
