# 排查用：查浮窗运行时的真实扩展样式，确认 WS_EX_NOACTIVATE 是否已被摘掉。
#
# 拖放收不到时先跑这个：带 NOACTIVATE 的窗口不会被 Windows 当作放置目标，
# 光看代码不算数，要看运行时的实际值。
#
#   powershell -NoProfile -File scripts/check-popup-style.ps1

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class PopupInfo {
    public delegate bool EnumProc(IntPtr h, IntPtr p);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr p);
    [DllImport("user32.dll")] public static extern int GetClassNameW(IntPtr h, StringBuilder s, int n);
    [DllImport("user32.dll")] public static extern IntPtr GetWindowLongPtrW(IntPtr h, int idx);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
    [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr parent, EnumProc cb, IntPtr p);
}
'@

$GWL_EXSTYLE = -20
$WS_EX_NOACTIVATE = 0x08000000
$WS_EX_ACCEPTFILES = 0x00000010

$results = New-Object System.Collections.ArrayList

$describe = {
    param([IntPtr]$h, [string]$prefix)
    $cls = New-Object System.Text.StringBuilder 256
    [void][PopupInfo]::GetClassNameW($h, $cls, 256)
    $ex = [int][PopupInfo]::GetWindowLongPtrW($h, $GWL_EXSTYLE)
    $noAct = [bool]($ex -band $WS_EX_NOACTIVATE)
    $accept = [bool]($ex -band $WS_EX_ACCEPTFILES)
    [void]$results.Add([pscustomobject]@{
        层级          = $prefix
        类名          = $cls.ToString()
        扩展样式      = ('0x{0:X8}' -f $ex)
        NOACTIVATE    = $noAct
        ACCEPTFILES   = $accept
        可见          = [PopupInfo]::IsWindowVisible($h)
    })
}

$cb = [PopupInfo+EnumProc] {
    param($h, $p)
    $cls = New-Object System.Text.StringBuilder 256
    [void][PopupInfo]::GetClassNameW($h, $cls, 256)
    if ($cls.ToString() -ne 'EasyShareHoverPopup') { return $true }

    & $describe $h '浮窗(父)'

    # WebView2 会建子窗口盖住客户区；拖放实际落在最上层的那个窗口上，
    # 所以子窗口的样式同样要看。
    $childCb = [PopupInfo+EnumProc] {
        param($ch, $cp)
        & $describe $ch '  └ 子窗口'
        return $true
    }
    [void][PopupInfo]::EnumChildWindows($h, $childCb, [IntPtr]::Zero)
    return $true
}

[void][PopupInfo]::EnumWindows($cb, [IntPtr]::Zero)

if ($results.Count -eq 0) {
    Write-Output '未找到浮窗窗口（EasyShareHoverPopup）——客户端可能没在跑。'
} else {
    $results | Format-Table -AutoSize | Out-String -Width 200 | Write-Output
    Write-Output '判读：固定态下父窗口的 NOACTIVATE 应为 False，否则拖放收不到。'
}
