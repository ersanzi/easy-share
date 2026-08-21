# 安装/卸载 EasyShare WPS 知识加载项（写入本机 WPS jsaddons 登记）
# 在线模式：加载项页面由知识服务托管（url 指向 /wps/），WPS 启动时按登记拉取 ribbon.xml。
# 用法：
#   安装：powershell -ExecutionPolicy Bypass -File install_wps_addon.ps1 -ServerUrl http://192.168.1.10:8000
#   卸载：powershell -ExecutionPolicy Bypass -File install_wps_addon.ps1 -Remove
param(
    [Parameter(Mandatory = $false)]
    [string]$ServerUrl = "",
    [switch]$Remove
)

$ErrorActionPreference = "Stop"
$addonName = "EasyShareKnowledge"
$jsaddonsDir = Join-Path $env:APPDATA "kingsoft\wps\jsaddons"
# 新版 WPS 个人版（≥12.1.0.16910）只认 publish.xml；旧版读 jsplugins.xml。两个都写，新老通吃。
# 注意：在线模式必须用 <jspluginonline> 标签（enable="true"）；<jsplugin> 是离线模式
# （url 指向 .7z 包，WPS 解压到 jsaddons\名称_版本），写错标签会被当离线加载而找不到文件。
$pluginFiles = @("publish.xml", "jsplugins.xml")

function Remove-AddonEntry([string]$content) {
    # 删除本加载项登记（jspluginonline/jsplugin 两种标签、自闭合与配对两种写法都处理）
    $pattern = '(?s)<jsplugin(online)?[^>]*name="' + $addonName + '"[^>]*/>\s*'
    $paired = '(?s)<jsplugin(online)?[^>]*name="' + $addonName + '"[^>]*>.*?</jsplugin(online)?>\s*'
    return (regexReplace (regexReplace $content $pattern) $paired)
}

function regexReplace([string]$content, [string]$pattern) {
    return [regex]::Replace($content, $pattern, '')
}

function Write-Utf8NoBom([string]$path, [string]$content) {
    [System.IO.File]::WriteAllText($path, $content, (New-Object System.Text.UTF8Encoding($false)))
}

function Test-EmptyPlugins([string]$content) {
    $trimmed = $content.Trim()
    return ($trimmed -eq "" -or $trimmed -eq "<jsplugins></jsplugins>" -or $trimmed -eq "<jsplugins />" -or $trimmed -eq "<jsplugins>`n</jsplugins>")
}

New-Item -ItemType Directory -Force -Path $jsaddonsDir | Out-Null

if (-not $Remove -and -not $ServerUrl) {
    Write-Host "缺少 -ServerUrl，例如：powershell -ExecutionPolicy Bypass -File install_wps_addon.ps1 -ServerUrl http://192.168.1.10:8000"
    exit 1
}

foreach ($fileName in $pluginFiles) {
    $filePath = Join-Path $jsaddonsDir $fileName

    if ($Remove) {
        if (Test-Path $filePath) {
            $updated = Remove-AddonEntry (Get-Content $filePath -Raw -Encoding UTF8)
            if (Test-EmptyPlugins $updated) {
                Remove-Item $filePath
                Write-Host "已卸载：$addonName（$fileName 已清理）"
            } else {
                Write-Utf8NoBom $filePath $updated
                Write-Host "已卸载：$addonName（$fileName 保留其他加载项）"
            }
        }
        continue
    }

    $url = $ServerUrl.TrimEnd("/")
    $entry = "<jspluginonline name=`"$addonName`" url=`"$url/wps/`" type=`"wps`" enable=`"true`" />"

    if (Test-Path $filePath) {
        $content = (Get-Content $filePath -Raw -Encoding UTF8).TrimEnd()
        $content = (Remove-AddonEntry $content).TrimEnd()  # 已登记则先删旧条目（换地址场景）
        if ($content -match '</jsplugins>\s*$') {
            $content = $content -replace '</jsplugins>\s*$', "$entry`n</jsplugins>"
        } else {
            $content = "$content`n$entry"
        }
        Write-Utf8NoBom $filePath $content
        Write-Host "已登记（在线模式）：$addonName -> $url/wps/（$fileName）"
    } else {
        $xml = "<?xml version=`"1.0`" encoding=`"UTF-8`"?>`n<jsplugins>`n$entry`n</jsplugins>`n"
        Write-Utf8NoBom $filePath $xml
        Write-Host "已创建登记（在线模式）：$addonName -> $url/wps/（$fileName）"
    }
}

if ($Remove) {
    Write-Host "重启 WPS 后生效。"
} else {
    Write-Host ""
    Write-Host "完成。请完全退出 WPS（含后台进程，可用 taskkill /IM wps.exe /F）后重新打开，"
    Write-Host "功能区会出现「知识」页签。若仍未出现：WPS 中按 Alt+F12 打开调试工具排查。"
}
