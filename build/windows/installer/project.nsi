Unicode true

####
## EasyShare NSIS 安装包
## 双进程部署：easyshare.exe（桌面）+ easyshare-core.exe（后台服务）
## 用户级安装，中文界面，可选开机自启动
####

## 产品元信息（由 wails_tools.nsh 从 wails.json info 字段注入）
## 如需手动调试可在此覆盖：
## !define INFO_PROJECTNAME    "EasyShare"
## !define INFO_COMPANYNAME    "EasyShare"
## !define INFO_PRODUCTNAME    "EasyShare"
## !define INFO_PRODUCTVERSION "0.1.0"
## !define INFO_COPYRIGHT      "Copyright 2026 laifeng"

!define PRODUCT_EXECUTABLE  "easyshare.exe"
!define CORE_EXECUTABLE     "easyshare-core.exe"
!define UNINST_KEY_NAME     "EasyShare"

## 用户级安装，不需要管理员权限
!define REQUEST_EXECUTION_LEVEL "user"
!define WAILS_INSTALL_SCOPE "user"

## Core 二进制路径（由 build.ps1 通过 -D 传入，或默认相对路径）
!ifndef ARG_CORE_BINARY
    !define ARG_CORE_BINARY "..\..\bin\easyshare-core.exe"
!endif

####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} 安装包"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support
ManifestDPIAware true

!include "MUI.nsh"

## 在线升级：解析命令行 /update 标志（客户端静默升级时传 "/S /update"）
!include "FileFunc.nsh"
!insertmacro GetParameters
!insertmacro GetOptions

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"

## 中文界面
!define MUI_WELCOMEPAGE_TITLE "欢迎安装 ${INFO_PRODUCTNAME}"
!define MUI_WELCOMEPAGE_TEXT "本向导将安装 ${INFO_PRODUCTNAME} 到您的计算机。$\r$\n$\r$\n${INFO_PRODUCTNAME} 是一款局域网文件传输工具，支持设备发现、文件互传和网络驱动器映射。$\r$\n$\r$\n建议在继续之前关闭所有正在运行的 EasyShare 进程。$\r$\n$\r$\n$_CLICK"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING
!define MUI_ABORTWARNING_TEXT "确定要退出 ${INFO_PRODUCTNAME} 安装向导吗？"

## 完成页选项
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "立即启动 ${INFO_PRODUCTNAME}"
!define MUI_FINISHPAGE_RUN_NOTCHECKED

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
InstallDirRegKey HKCU "Software\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}" "InstallDir"
ShowInstDetails show
ShowUninstDetails show

## 自启动选项变量
Var AutoStart
## 在线升级标志：1 = 静默升级安装，完成后自动重启应用
Var UpdateMode

Function .onInit
    !insertmacro wails.checkArchitecture
    StrCpy $AutoStart "0"
    StrCpy $UpdateMode "0"
    ${GetParameters} $R0
    ${GetOptions} $R0 "/update" $R1
    ${IfNot} ${Errors}
        StrCpy $UpdateMode "1"
    ${EndIf}
FunctionEnd

## 安装区段
Section "Install"
    !insertmacro wails.setShellContext

    ## 覆盖安装前终止正在运行的 EasyShare 进程（exe 被占用会导致覆盖失败）。
    ## 交互式重装与静默升级共用；进程不存在时 taskkill 静默失败，不影响安装。
    DetailPrint "正在停止 EasyShare 进程..."
    nsExec::ExecToLog 'taskkill /F /IM "${PRODUCT_EXECUTABLE}" /T'
    nsExec::ExecToLog 'taskkill /F /IM "${CORE_EXECUTABLE}" /T'
    Sleep 1000

    ## 安装 WebView2 Runtime（如果缺失）
    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    ## 部署主程序
    !insertmacro wails.files

    ## 部署 Core 进程
    File "/oname=${CORE_EXECUTABLE}" "${ARG_CORE_BINARY}"

    ## 创建快捷方式
    CreateDirectory "$SMPROGRAMS\${INFO_PRODUCTNAME}"
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortcut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    ## 写入安装路径（供升级和卸载使用）
    WriteRegStr HKCU "Software\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}" "InstallDir" "$INSTDIR"
    WriteRegStr HKCU "Software\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}" "Version" "${INFO_PRODUCTVERSION}"

    ## 写入卸载信息
    !insertmacro wails.writeUninstaller

    ## 询问是否开机自启动（静默升级 /S 不弹窗：MessageBox 在静默模式下的返回值
    ## 是默认按钮 IDYES，会误开自启，必须显式跳过）
    ${IfNot} ${Silent}
        MessageBox MB_YESNO|MB_ICONQUESTION "是否在 Windows 启动时自动运行 ${INFO_PRODUCTNAME}？" IDNO noAutoStart
            StrCpy $AutoStart "1"
            WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${INFO_PRODUCTNAME}" "$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\""
        noAutoStart:
    ${EndIf}

    ## 在线升级（/update）：安装完成后自动重启应用，对齐主流软件的升级体验
    ${If} $UpdateMode == "1"
        DetailPrint "升级完成，正在重启 ${INFO_PRODUCTNAME}..."
        Exec '"$INSTDIR\${PRODUCT_EXECUTABLE}"'
    ${EndIf}

SectionEnd

## 卸载区段
Section "Uninstall"
    !insertmacro wails.setShellContext

    ## 终止 EasyShare 进程
    DetailPrint "正在停止 EasyShare 进程..."
    nsExec::ExecToLog 'taskkill /F /IM "${PRODUCT_EXECUTABLE}" /T'
    nsExec::ExecToLog 'taskkill /F /IM "${CORE_EXECUTABLE}" /T'
    Sleep 1000

    ## 移除 EasyShare 创建的网络映射
    ## 通过 UNC 路径匹配：\\127.0.0.1@19080\DavWWWRoot
    DetailPrint "正在清理网络驱动器映射..."
    nsExec::ExecToLog 'net use Z: /delete /y'
    ## 注意：只尝试删除默认盘符 Z:，如果用户修改过盘符则需要手动清理
    ## net use 在映射不存在时会静默失败，不影响卸载流程

    ## 删除开机自启动
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${INFO_PRODUCTNAME}"

    ## 删除快捷方式
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}\${INFO_PRODUCTNAME}.lnk"
    RMDir "$SMPROGRAMS\${INFO_PRODUCTNAME}"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    ## 删除安装目录
    RMDir /r $INSTDIR

    ## 删除安装信息注册表
    DeleteRegKey HKCU "Software\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"

    ## 删除卸载信息
    !insertmacro wails.deleteUninstaller

    ## 提示用户数据未删除
    MessageBox MB_OK|MB_ICONINFORMATION "EasyShare 已卸载。$\r$\n$\r$\n您的个人数据（配置、日志、接收的文件和共享目录）未被删除。$\r$\n如需完全清理，请手动删除：$\r$\n%LOCALAPPDATA%\EasyShare$\r$\n%USERPROFILE%\Downloads\EasyShare$\r$\n%USERPROFILE%\EasyShare"

SectionEnd
