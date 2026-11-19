param([Parameter(Mandatory=$true)][string]$Manifest)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Windows.Forms,System.Drawing
Add-Type -Path (Join-Path $PSScriptRoot 'easyshare-gui-input.cs') -ReferencedAssemblies @('System.Windows.Forms','System.Drawing','UIAutomationClient','UIAutomationTypes','WindowsBase')
Add-Type -Path (Join-Path $PSScriptRoot 'owned-explorer-tree.cs')
Add-Type @'
using System;using System.Runtime.InteropServices;
public static class ExplorerOwned {
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] static extern bool AttachThreadInput(uint a,uint b,bool attach);
 public static bool Focus(IntPtr h){uint p;uint own=GetCurrentThreadId(),target=GetWindowThreadProcessId(h,out p),foreground=GetWindowThreadProcessId(GetForegroundWindow(),out p);bool a=false,b=false;try{if(own!=foreground&&foreground!=0)a=AttachThreadInput(own,foreground,true);if(own!=target&&target!=foreground)b=AttachThreadInput(own,target,true);SetForegroundWindow(h);return GetForegroundWindow()==h;}finally{if(b)AttachThreadInput(own,target,false);if(a)AttachThreadInput(own,foreground,false);}}
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h,int c);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h,out uint p);
 [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")] public static extern void mouse_event(uint flags,uint x,uint y,uint data,UIntPtr extra);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
}
'@
$m=Get-Content -Raw -Encoding UTF8 -LiteralPath $Manifest|ConvertFrom-Json
if([string]$m.clsid -in @('{8327AC46-BCC2-45D2-A46F-0737E2CC6557}','{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}')){
 $repo=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
 $allowed=Join-Path $repo '.rd-test\account-binding\data\cloud-drive\'
 if(-not $m.accountBindingTest -or -not ([string]$m.root).StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'Production CLSID forbidden outside isolated account binding'}
}
$sh=New-Object -ComObject Shell.Application
$before=@($sh.Windows()|ForEach-Object{[long]$_.HWND})
Start-Process explorer.exe -ArgumentList '/n,','"::{20D04FE0-3AEA-1069-A2D8-08002B30309D}"'
$window=$null
for($i=0;$i -lt 50;$i++){Start-Sleep -Milliseconds 150;$candidate=@($sh.Windows()|Where-Object{[long]$_.HWND -notin $before -and [string]$_.Document.Folder.Self.Path -eq '::{20D04FE0-3AEA-1069-A2D8-08002B30309D}'});if($candidate.Count -eq 1 -and @($candidate[0].Document.Folder.Items()|Where-Object{$_.Name -ceq $m.label}).Count -eq 1){$window=$candidate[0];break}}
if(-not $window){throw 'Unique new This PC window unavailable'}
$h=[IntPtr][long]$window.HWND;[uint32]$owner=0;[void][ExplorerOwned]::GetWindowThreadProcessId($h,[ref]$owner)
try {
 [void][ExplorerOwned]::ShowWindow($h,3);[void][ExplorerOwned]::Focus($h);Start-Sleep -Milliseconds 500
 $owned=@($window.Document.Folder.Items()|Where-Object{$_.Name -ceq $m.label})
 if($owned.Count -ne 1){throw 'Owned Shell tile unavailable'}
 $window.Document.SelectItem($owned[0],29);Start-Sleep -Milliseconds 300
 $ui=[Windows.Automation.AutomationElement]::FromHandle($h)
 $tiles=@($ui.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::ListItem))|Where-Object{$_.Current.Name -ceq $m.label})
 @{path=[string]$window.Document.Folder.Self.Path;label=[string]$m.label;items=@($window.Document.Folder.Items()|ForEach-Object{@{name=$_.Name;path=$_.Path}});uia=@($ui.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)|ForEach-Object{@{name=$_.Current.Name;type=$_.Current.ControlType.ProgrammaticName}})}|ConvertTo-Json -Depth 5|Set-Content -Encoding UTF8 -LiteralPath (Join-Path $m.run 'tile-diagnostic.json')
 if($tiles.Count -ne 1){throw 'Unique owned tile unavailable'}
 $r=$tiles[0].Current.BoundingRectangle
 [void][ExplorerOwned]::GetWindowThreadProcessId($h,[ref]$owner)
 if((Get-Process -Id $owner).Path -ine (Join-Path $env:WINDIR 'explorer.exe')){throw 'Target owner is not Explorer'}
 if(-not [ExplorerOwned]::Focus($h)){throw 'Cannot focus exact owned Explorer before double-click'}
 [EAInput]::DoubleClickItem($h,[int]$owner,[int](($r.Left+$r.Right)/2),[int](($r.Top+$r.Bottom)/2),[string]$m.label)
 for($i=0;$i -lt 30;$i++){Start-Sleep -Milliseconds 150;if([string]$window.Document.Folder.Self.Path -ieq $m.root){break}}
 if([string]$window.Document.Folder.Self.Path -ine $m.root){throw 'Actual tile double-click did not enter owned root'}
 Start-Sleep -Seconds 2
 $ui=[Windows.Automation.AutomationElement]::FromHandle($h)
 $treeNodes=@($ui.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::TreeItem)))
 $ours=@($treeNodes|Where-Object{$_.Current.Name -ceq $m.label})
 $pc=@($treeNodes|Where-Object{$_.Current.Name -in @('此电脑','This PC')})
 @{root=[string]$window.Document.Folder.Self.Path;uia=@($ui.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)|ForEach-Object{@{name=$_.Current.Name;type=$_.Current.ControlType.ProgrammaticName;class=$_.Current.ClassName}})}|ConvertTo-Json -Depth 5|Set-Content -Encoding UTF8 -LiteralPath (Join-Path $m.run 'navigation-diagnostic.json')
 $native=@([OwnedExplorerTree]::Read($h,$owner));$native|ConvertTo-Json -Depth 5|Set-Content -Encoding UTF8 -LiteralPath (Join-Path $m.run 'native-navigation.json')
 $ownedNative=@($native|Where-Object{$_.Name -ceq $m.label});$pcNative=@($native|Where-Object{$_.Name -in @('此电脑','This PC')})
 if($ownedNative.Count -ne 1 -or $pcNative.Count -ne 1){throw ('Native navigation nodes not unique ours='+$ownedNative.Count+' pc='+$pcNative.Count)}
 if($ownedNative[0].Parent -ne $pcNative[0].Parent){throw 'Cloud navigation is not sibling of This PC'}
 $headers=@($ui.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ClassNameProperty,'UIColumnHeader'))|ForEach-Object{$_.Current.Name})
 if($headers.Count -lt 2){throw ('Detailed columns unavailable: '+($headers -join ','))}
 if($m.requireStatus -and @($headers|Where-Object{$_ -in @('状态','可用性状态','同步状态','Status','Availability status','Sync status')}).Count -eq 0){throw ('Real cloud status column missing: '+($headers -join ','))}
 $pcNode=$pcNative[0]
 [void][ExplorerOwned]::Focus($h);[void][ExplorerOwned]::SetCursorPos($pcNode.X,$pcNode.Y);Start-Sleep -Milliseconds 100
 [ExplorerOwned]::mouse_event(2,0,0,0,[UIntPtr]::Zero);[ExplorerOwned]::mouse_event(4,0,0,0,[UIntPtr]::Zero)
 Start-Sleep -Milliseconds 600
 $r=@([OwnedExplorerTree]::Read($h,$owner)|Where-Object{$_.Name -ceq $m.label})[0]
 [void][ExplorerOwned]::Focus($h);[void][ExplorerOwned]::SetCursorPos($r.X,$r.Y);Start-Sleep -Milliseconds 100
 [ExplorerOwned]::mouse_event(2,0,0,0,[UIntPtr]::Zero);[ExplorerOwned]::mouse_event(4,0,0,0,[UIntPtr]::Zero)
 for($i=0;$i -lt 30;$i++){Start-Sleep -Milliseconds 150;if([string]$window.Document.Folder.Self.Path -ieq $m.root){break}}
 if([string]$window.Document.Folder.Self.Path -ine $m.root){throw 'Actual navigation click did not enter same owned root'}
 Start-Sleep -Seconds 2
 $ui=[Windows.Automation.AutomationElement]::FromHandle($h)
 $finalHeaders=@($ui.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ClassNameProperty,'UIColumnHeader'))|ForEach-Object{$_.Current.Name})
 if($m.requireStatus -and @($finalHeaders|Where-Object{$_ -in @('状态','可用性状态','同步状态','Status','Availability status','Sync status')}).Count -eq 0){throw ('Navigation lost status column: '+($finalHeaders -join ','))}
 $headers=$finalHeaders
 $a=[Windows.Forms.SystemInformation]::VirtualScreen;$b=New-Object Drawing.Bitmap($a.Width,$a.Height);$g=[Drawing.Graphics]::FromImage($b);try{$g.CopyFromScreen($a.Left,$a.Top,0,0,$a.Size);$b.Save((Join-Path $m.run 'view.png'),[Drawing.Imaging.ImageFormat]::Png)}finally{$g.Dispose();$b.Dispose()}
 @{actualTileDoubleClick=$true;actualNavigationClick=$true;siblingOfThisPC=$true;headers=$headers;root=$m.root;serverClosedLoopTested=$false}|ConvertTo-Json -Depth 5|Set-Content -Encoding UTF8 -LiteralPath (Join-Path $m.run 'result.json')
 Write-Output 'EXPLORER_GUI_PASS'
}catch{Write-Output ('FAIL-LINE '+$_.InvocationInfo.ScriptLineNumber+' '+$_.Exception.Message);throw}finally{[void][ExplorerOwned]::PostMessage($h,0x10,[IntPtr]::Zero,[IntPtr]::Zero)}
