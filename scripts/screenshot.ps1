# 截图指定窗口（默认 EasyShare 主窗口），存为 PNG。
#
# 排查/验收用：把窗口提到前台后整屏截取，再按窗口矩形裁剪。
#   powershell -NoProfile -File scripts/screenshot.ps1 -Out shot.png
#   powershell -NoProfile -File scripts/screenshot.ps1 -ProcessName explorer -Out pc.png

param(
    [string]$ProcessName = 'easyshare',
    [string]$Out = 'shot.png',
    [int]$DelaySeconds = 2
)

Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms

Add-Type @'
using System;
using System.Runtime.InteropServices;
public class Win {
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd);
    [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT r);
    [StructLayout(LayoutKind.Sequential)]
    public struct RECT { public int Left, Top, Right, Bottom; }
}
'@

$proc = Get-Process -Name $ProcessName -ErrorAction SilentlyContinue |
    Where-Object { $_.MainWindowHandle -ne 0 } | Select-Object -First 1
if ($null -eq $proc) {
    Write-Output "NO_WINDOW: $ProcessName 没有可见主窗口"
    exit 1
}

$h = $proc.MainWindowHandle
[void][Win]::ShowWindow($h, 9)   # SW_RESTORE
[void][Win]::SetForegroundWindow($h)
Start-Sleep -Seconds $DelaySeconds

$rect = New-Object Win+RECT
if (-not [Win]::GetWindowRect($h, [ref]$rect)) {
    Write-Output 'RECT_FAILED'
    exit 1
}
$w = $rect.Right - $rect.Left
$hh = $rect.Bottom - $rect.Top
if ($w -le 0 -or $hh -le 0) {
    Write-Output "BAD_RECT: ${w}x${hh}"
    exit 1
}

$bmp = New-Object System.Drawing.Bitmap $w, $hh
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($rect.Left, $rect.Top, 0, 0, $bmp.Size)
$g.Dispose()

$full = if ([System.IO.Path]::IsPathRooted($Out)) { $Out } else { Join-Path (Get-Location).Path $Out }
$bmp.Save($full, [System.Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()
Write-Output "SAVED: $full (${w}x${hh})"
