# 转储 EasyShare 在「此电脑」的命名空间注册现状。
#
# 排查用：确认两个盘各自的 CLSID 树、目标路径、以及是否挂在 MyComputer\NameSpace 下。
# 只读，不改注册表。
#
#   powershell -NoProfile -File scripts/dump-namespace.ps1

[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$entries = [ordered]@{
    'EasyShare 网盘' = '{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}'
    'EasyShare 共享' = '{F6B2A3C4-D5E6-7F8A-9B0C-1D2E3F4A5B6C}'
}

# WPS 作为设计参照：看它如何组织账号维度的目录与副标题（学设计，不涉及其实现）
$wps = '{EEEEFCF7-867B-4FA2-9ABD-884CF531B610}'

function Dump-Clsid {
    param([string]$Label, [string]$Guid)

    $base = "HKCU:\Software\Classes\CLSID\$Guid"
    $ns = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\$Guid"

    Write-Output "=========== $Label  $Guid"
    if (-not (Test-Path $base)) {
        Write-Output "  CLSID: 未注册"
    } else {
        $paths = @($base) + (Get-ChildItem $base -Recurse -ErrorAction SilentlyContinue | ForEach-Object { $_.PSPath })
        foreach ($p in $paths) {
            $item = Get-Item $p -ErrorAction SilentlyContinue
            if ($null -eq $item) { continue }
            $rel = $item.Name.Substring($item.Name.IndexOf($Guid) + $Guid.Length)
            if ($rel -eq '') { $rel = '(root)' }
            foreach ($n in $item.GetValueNames()) {
                $label = if ($n -eq '') { '(default)' } else { $n }
                Write-Output ("  {0,-32} {1} = {2}" -f $rel, $label, $item.GetValue($n))
            }
        }
    }
    Write-Output ("  NameSpace 条目: " + (Test-Path $ns))
    Write-Output ''
}

foreach ($label in $entries.Keys) {
    Dump-Clsid -Label $label -Guid $entries[$label]
}

Write-Output '########## 参照：WPS（只读观察其前端设计）'
Dump-Clsid -Label 'WPS' -Guid $wps

Write-Output '########## WPSDrive 目录布局（账号维度的两个同级入口）'
$wpsDrive = Join-Path $env:USERPROFILE 'WPSDrive'
if (Test-Path $wpsDrive) {
    Get-ChildItem $wpsDrive -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Output ("  " + $_.Name + "  (账号目录)")
        Get-ChildItem $_.FullName -ErrorAction SilentlyContinue | ForEach-Object {
            $tag = if ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) { ' [重解析点]' } else { '' }
            Write-Output ("    - " + $_.Name + $tag)
        }
    }
} else {
    Write-Output '  未安装或路径不同'
}
