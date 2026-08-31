# 排查用：列出桌面挂件类窗口的类名与扩展样式，重点看它们是否带 WS_EX_NOACTIVATE。
#
# 目的：EasyShare 浮窗收不到拖放，怀疑与窗口扩展样式有关。
# 拿本机能正常接收拖放的挂件做对照，比猜测可靠。
#
#   powershell -NoProfile -File scripts/inspect-drop-windows.ps1

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class WinInfo {
    public delegate bool EnumProc(IntPtr h, IntPtr p);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
    [DllImport("user32.dll")] public static extern int GetWindowTextW(IntPtr h, StringBuilder s, int n);
    [DllImport("user32.dll")] public static extern int GetClassNameW(IntPtr h, StringBuilder s, int n);
    [DllImport("user32.dll")] public static extern int GetWindowLongW(IntPtr h, int idx);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
}
'@

$GWL_EXSTYLE = -20
$GWL_STYLE = -16
$WS_EX_NOACTIVATE = 0x08000000
$WS_EX_ACCEPTFILES = 0x00000010
$WS_EX_TOPMOST = 0x00000008
$WS_EX_TOOLWINDOW = 0x00000080
$WS_EX_LAYERED = 0x00080000
$WS_EX_TRANSPARENT = 0x00000020

# 只看这些进程：桌面挂件候选 + 我们自己的浮窗做对照
$interesting = @('Nexus', 'easyshare', 'PowerToys.QuickAccess', 'Weixin')

$rows = New-Object System.Collections.ArrayList

$cb = [WinInfo+EnumProc] {
    param($h, $p)
    if (-not [WinInfo]::IsWindowVisible($h)) { return $true }

    # 不能用 $pid：PowerShell 的保留只读变量
    $procId = 0
    [void][WinInfo]::GetWindowThreadProcessId($h, [ref]$procId)
    $proc = Get-Process -Id $procId -ErrorAction SilentlyContinue
    if ($null -eq $proc) { return $true }
    if ($interesting -notcontains $proc.ProcessName) { return $true }

    $cls = New-Object System.Text.StringBuilder 256
    [void][WinInfo]::GetClassNameW($h, $cls, 256)
    $txt = New-Object System.Text.StringBuilder 256
    [void][WinInfo]::GetWindowTextW($h, $txt, 256)

    $ex = [WinInfo]::GetWindowLongW($h, $GWL_EXSTYLE)

    $flags = @()
    if ($ex -band $WS_EX_NOACTIVATE) { $flags += 'NOACTIVATE' }
    if ($ex -band $WS_EX_ACCEPTFILES) { $flags += 'ACCEPTFILES' }
    if ($ex -band $WS_EX_TOPMOST) { $flags += 'TOPMOST' }
    if ($ex -band $WS_EX_TOOLWINDOW) { $flags += 'TOOLWINDOW' }
    if ($ex -band $WS_EX_LAYERED) { $flags += 'LAYERED' }
    if ($ex -band $WS_EX_TRANSPARENT) { $flags += 'TRANSPARENT' }

    [void]$rows.Add([pscustomobject]@{
        进程    = $proc.ProcessName
        类名    = $cls.ToString()
        标题    = $txt.ToString()
        扩展样式 = ('0x{0:X8}' -f $ex)
        关键标志 = ($flags -join ' ')
    })
    return $true
}

[void][WinInfo]::EnumWindows($cb, [IntPtr]::Zero)

$rows | Sort-Object 进程 | Format-Table -AutoSize | Out-String -Width 220 | Write-Output

Write-Output ''
Write-Output '判读要点：'
Write-Output '  NOACTIVATE  —— 窗口不接受激活。Windows 通常不向这类窗口投递拖放。'
Write-Output '  ACCEPTFILES —— 调过 DragAcceptFiles，可收 WM_DROPFILES。'
Write-Output '  能正常接收拖放的挂件若不带 NOACTIVATE，就说明该标志正是我们的症结。'
