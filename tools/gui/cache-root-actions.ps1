param([Parameter(Mandatory=$true)][string]$RunDir,[Parameter(Mandatory=$true)][ValidateSet('Enter','NewEmpty','NewContent','Edit','Copy','Drag','Export','Close')][string]$Action)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Windows.Forms,System.Drawing
Add-Type -Path (Join-Path $PSScriptRoot 'easyshare-gui-input.cs') -ReferencedAssemblies @('System.Windows.Forms','System.Drawing','UIAutomationClient','UIAutomationTypes','WindowsBase')
Add-Type -Path (Join-Path $PSScriptRoot 'easyshare-gui-owned-menu.cs')
Add-Type @'
using System;using System.Runtime.InteropServices;
public static class CacheGUIWin {
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h,int c);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h,out uint p);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern IntPtr SendMessage(IntPtr h,uint m,IntPtr w,string text);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
 [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] static extern bool AttachThreadInput(uint a,uint b,bool attach);
 [DllImport("user32.dll")] static extern bool BringWindowToTop(IntPtr h);
 public static bool Focus(IntPtr h) {
  uint p;uint current=GetCurrentThreadId();uint target=GetWindowThreadProcessId(h,out p);uint foreground=GetWindowThreadProcessId(GetForegroundWindow(),out p);
  bool a=false,b=false;
  try {
   if(current!=foreground&&foreground!=0)a=AttachThreadInput(current,foreground,true);
   if(current!=target&&target!=foreground)b=AttachThreadInput(current,target,true);
   BringWindowToTop(h);SetForegroundWindow(h);return GetForegroundWindow()==h;
  } finally {if(b)AttachThreadInput(current,target,false);if(a)AttachThreadInput(current,foreground,false);}
 }
}
'@
[void][EAInput]::SetProcessDPIAware()
$m=Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $RunDir 'gui-manifest.json')|ConvertFrom-Json
$repository=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$workspace=Join-Path $repository '.rd-test'
$RunDir=[IO.Path]::GetFullPath($RunDir)
$root=[IO.Path]::GetFullPath([string]$m.root)
$runParent=Split-Path -Parent $RunDir
$runName=Split-Path -Leaf $RunDir
if($runParent -ine $workspace -or $runName -notmatch '^live-cache-gui-[0-9]+$' -or $root -ine (Join-Path $RunDir 'root') -or $m.nonce -cnotmatch '^[a-f0-9]{32}$' -or $m.clsid -in @('{8327AC46-BCC2-45D2-A46F-0737E2CC6557}','{E5A1F2B3-C4D5-6E7F-8A9B-0C1D2E3F4A5B}')){throw 'Not an isolated GUI acceptance manifest'}
foreach($parent in @($workspace,$RunDir)){if((Get-Item -LiteralPath $parent).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Reparse acceptance workspace'}}
$explorerPath=Join-Path $env:WINDIR 'explorer.exe'
$sh=New-Object -ComObject Shell.Application
$h=[IntPtr]::Zero;$pidExpected=0
function Save($name,$value){$value|ConvertTo-Json -Depth 12|Set-Content -Encoding UTF8 -LiteralPath (Join-Path $RunDir ($name+'.json'))}
function GetReceipt {
 $script:entry=Get-Content -Encoding UTF8 -Raw -LiteralPath (Join-Path $RunDir 'entry.json')|ConvertFrom-Json
 $script:h=[IntPtr][long]$entry.hwnd;$script:pidExpected=[int]$entry.pid
 [EAInput]::Identity($h,$pidExpected)
 $p=Get-Process -Id $pidExpected
 if($p.Path -ine $explorerPath -or $p.StartTime.ToUniversalTime().ToString('o') -cne $entry.start){throw 'Explorer process identity changed'}
}
function RootWindow {
 GetReceipt
 $wins=@($sh.Windows()|Where-Object {[long]$_.HWND -eq $h.ToInt64()})
 if($wins.Count -ne 1 -or [string]$wins[0].Document.Folder.Self.Path -ine $root){throw 'Dedicated window is not exact isolated root'}
 $script:w=$wins[0]
 $bag=Get-Item -LiteralPath ('Registry::HKEY_CURRENT_USER\Software\Classes\CLSID\'+$m.clsid+'\Instance\InitPropertyBag')
 if([string]$bag.GetValue('TargetFolderPath') -ine $root){throw 'Isolated namespace target changed'}
 [void][CacheGUIWin]::ShowWindow($h,3);[void][CacheGUIWin]::Focus($h);Start-Sleep -Milliseconds 200
 if([CacheGUIWin]::GetForegroundWindow() -ne $h){throw 'Dedicated Explorer foreground unavailable'}
 $script:ui=[System.Windows.Automation.AutomationElement]::FromHandle($h)
}
function BackgroundPoint {
 RootWindow
 $panes=@($ui.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ClassNameProperty,'UIItemsView')))
 if($panes.Count -ne 1){throw 'Unique root file pane unavailable'}
 $r=$panes[0].Current.BoundingRectangle
 for($y=[int]$r.Bottom-50;$y -gt $r.Top+50;$y-=30){for($x=[int]$r.Right-80;$x -gt $r.Left+150;$x-=35){try{[EAInput]::Background($h,$pidExpected,$x,$y);return @{x=$x;y=$y}}catch{}}}
 throw 'Verified root background unavailable'
}
function Hash($p){(Get-FileHash -Algorithm SHA256 -LiteralPath $p).Hash.ToLowerInvariant()}
function WaitFile($p,$hash){for($i=0;$i -lt 50;$i++){if((Test-Path -LiteralPath $p) -and (Hash $p) -ceq $hash){return};Start-Sleep -Milliseconds 150};throw 'Expected test content not observed'}
function SelectItem($window,$handle,$owner,$item){
 [EAInput]::Identity($handle,$owner);[void][CacheGUIWin]::Focus($handle)
 if([CacheGUIWin]::GetForegroundWindow() -ne $handle){throw 'Selection foreground unavailable'}
 $window.Document.SelectItem($item,29);Start-Sleep -Milliseconds 150
 $sel=@($window.Document.SelectedItems());$focus=[System.Windows.Automation.AutomationElement]::FocusedElement
 if($sel.Count -ne 1 -or $sel[0].Path -ine $item.Path -or $focus.Current.ProcessId -ne $owner -or $focus.Current.Name -cne $item.Name){throw 'Exact item selection not proved'}
}
function OpenDedicated($path){
 $before=@($sh.Windows()|ForEach-Object {[long]$_.HWND})
 Start-Process -FilePath $explorerPath -ArgumentList '/n,',('"'+$path+'"')
 for($i=0;$i -lt 55;$i++){
  Start-Sleep -Milliseconds 180
  $wins=@($sh.Windows()|Where-Object {[long]$_.HWND -notin $before -and [string]$_.Document.Folder.Self.Path -ieq $path})
  if($wins.Count -gt 1){throw 'Multiple new windows; refuse adoption'}
  if($wins.Count -eq 1){return $wins[0]}
 }
 throw 'No uniquely new Explorer window'
}
try {
 if($Action -eq 'Enter'){
  $pc='::{20D04FE0-3AEA-1069-A2D8-08002B30309D}'
  $window=OpenDedicated $pc;$h=[IntPtr][long]$window.HWND;[uint32]$owner=0;[void][CacheGUIWin]::GetWindowThreadProcessId($h,[ref]$owner);$pidExpected=[int]$owner
  $p=Get-Process -Id $pidExpected
  if($p.Path -ine $explorerPath){throw 'Dedicated process is not Explorer'}
  Save 'entry' @{hwnd=$h.ToInt64();pid=$pidExpected;start=$p.StartTime.ToUniversalTime().ToString('o');before=$pc;method='new dedicated This PC window; navigation pending';root=$root}
  [void][CacheGUIWin]::ShowWindow($h,3);[void][CacheGUIWin]::Focus($h);Start-Sleep -Milliseconds 350
  $ui=[System.Windows.Automation.AutomationElement]::FromHandle($h)
  $tiles=@($ui.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::ListItem))|Where-Object {$_.Current.Name -ceq $m.label})
  if($tiles.Count -ne 1){throw 'Isolated This PC tile not unique'}
  $item=@($window.Document.Folder.Items()|Where-Object {$_.Name -ceq $m.label})
  Save 'tile-candidates' @($item|ForEach-Object {@{Name=$_.Name;Path=$_.Path}})
  $bag=Get-Item -LiteralPath ('Registry::HKEY_CURRENT_USER\Software\Classes\CLSID\'+$m.clsid+'\Instance\InitPropertyBag')
  if($item.Count -ne 1 -or [string]$bag.GetValue('TargetFolderPath') -ine $root -or ([string]$item[0].Path -ine $root -and [string]$item[0].Path -notlike ('*'+$m.clsid))){throw 'Tile identity not tied to exact registered test root'}
  $pattern=$null;if($tiles[0].TryGetCurrentPattern([System.Windows.Automation.ScrollItemPattern]::Pattern,[ref]$pattern)){$pattern.ScrollIntoView()}
  $r=$tiles[0].Current.BoundingRectangle;$x=[int](($r.Left+$r.Right)/2);$y=[int](($r.Top+$r.Bottom)/2)
  $receipt=@{hwnd=$h.ToInt64();pid=$pidExpected;start=$p.StartTime.ToUniversalTime().ToString('o');before=$pc;tilePath=[string]$item[0].Path;method='actual This PC tile double-click';root=$root}
  Save 'entry-pending' $receipt
  [void][CacheGUIWin]::Focus($h)
  [EAInput]::DoubleClickItem($h,$pidExpected,$x,$y,[string]$m.label)
  for($i=0;$i -lt 60;$i++){Start-Sleep -Milliseconds 150;if([string]$window.Document.Folder.Self.Path -ieq $root){break}}
  if([string]$window.Document.Folder.Self.Path -ine $root){throw 'Tile did not navigate to isolated server root'}
  $receipt.after=[string]$window.Document.Folder.Self.Path;Save 'entry' $receipt
 }elseif($Action -eq 'Close'){
  GetReceipt
  [void][CacheGUIWin]::PostMessage($h,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)
  Save 'closed' @{hwnd=$h.ToInt64();pid=$pidExpected;method='WM_CLOSE dedicated test window only'}
 }elseif($Action -eq 'NewEmpty'){
  RootWindow;$name=[string]$m.names.new;$dest=Join-Path $root $name
  if(Test-Path -LiteralPath $dest){throw 'New target already exists'}
  $before=@($w.Document.Folder.Items()|ForEach-Object {$_.Path})
  $point=BackgroundPoint
  [EAInput]::RightClick($h,$pidExpected,$point.x,$point.y);Start-Sleep -Milliseconds 350
  $menu=@([EAOwnedMenu]::Snapshot($h,[uint32]$pidExpected));Save 'root-new-menu' $menu
  $new=@($menu|Where-Object {$_.Normalized -in @('新建','New') -and $_.Submenu -and ($_.State -band 3) -eq 0})
  if($new.Count -ne 1){throw 'Root New submenu unavailable'}
  [void][EAOwnedMenu]::Click($h,[uint32]$pidExpected,$new[0].Normalized,$true)
  $submenu=@([EAOwnedMenu]::Snapshot($h,[uint32]$pidExpected));Save 'root-new-submenu' $submenu
  $text=@($submenu|Where-Object {$_.Normalized -in @('文本文档','Text Document') -and -not $_.Submenu -and ($_.State -band 3) -eq 0})
  if($text.Count -ne 1){throw 'Text Document command unavailable'}
  [void][EAOwnedMenu]::Click($h,[uint32]$pidExpected,$text[0].Normalized,$false);Start-Sleep -Milliseconds 300
  $delta=@($w.Document.Folder.Items()|Where-Object {$_.Path -notin $before})
  if($delta.Count -ne 1 -or $delta[0].IsFolder -or [IO.Path]::GetDirectoryName($delta[0].Path) -ine $root -or [IO.Path]::GetExtension($delta[0].Path) -ine '.txt'){throw 'Unique new root text not proved'}
  $focus=[System.Windows.Automation.AutomationElement]::FocusedElement
  if($focus.Current.ProcessId -ne $pidExpected -or -not $focus.Current.HasKeyboardFocus){throw 'Rename focus not proved'}
  $old=[IO.Path]::GetFileName($delta[0].Path);$stem=[IO.Path]::GetFileNameWithoutExtension($old)
  $vp=$null;$has=$focus.TryGetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern,[ref]$vp);$value=if($has){$vp.Current.Value}else{$focus.Current.Name}
  if($value -ceq $old){$newName=$name}elseif($value -ceq $stem){$newName=[IO.Path]::GetFileNameWithoutExtension($name)}else{throw 'Rename expected value differs'}
  if($has){$vp.SetValue($newName);[EAInput]::Press($h,$pidExpected,0x0d)}else{[EAInput]::RenameText($h,$pidExpected,$value,$newName)}
  WaitFile $dest 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
  Save 'new-empty' @{root=$root;path=$name;bytes=0;createdByIO=$false;method='root background New Text Document and verified rename'}
 }elseif($Action -in @('NewContent','Edit')){
  RootWindow;$name=[string]$m.names.new;$dest=Join-Path $root $name
  $phase=if($Action -eq 'Edit'){'edit'}else{'new'}
  $expected=if($Action -eq 'Edit'){$m.sha256.new}else{'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'}
  if((Hash $dest) -cne $expected){throw 'Editor input baseline changed'}
  $item=@($w.Document.Folder.Items()|Where-Object {$_.Path -ieq $dest -and -not $_.IsFolder});if($item.Count -ne 1){throw 'Root text missing'}
  SelectItem $w $h $pidExpected $item[0]
  $before=@(Get-Process -Name notepad -ErrorAction SilentlyContinue|ForEach-Object {$_.Id})
  [void]$item[0].InvokeVerb('edit');Start-Sleep -Seconds 2
  $np=@(Get-Process -Name notepad -ErrorAction SilentlyContinue|Where-Object {$_.Id -notin $before -and $_.MainWindowTitle -like ('*'+$name+'*')})
  if($np.Count -ne 1){throw 'Unique newly opened test Notepad unavailable'}
  $nh=[IntPtr]$np[0].MainWindowHandle;$eu=[System.Windows.Automation.AutomationElement]::FromHandle($nh)
  $edits=@($eu.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ClassNameProperty,'Edit')))
  if($edits.Count -ne 1 -or $edits[0].Current.ProcessId -ne $np[0].Id){throw 'Notepad text control not unique'}
  [void][CacheGUIWin]::SendMessage([IntPtr]$edits[0].Current.NativeWindowHandle,0x000c,[IntPtr]::Zero,[string]$m.contents.$phase)
  [void][CacheGUIWin]::Focus($nh);[EAInput]::Shortcut($nh,$np[0].Id,0x53)
  WaitFile $dest ([string]$m.sha256.$phase)
  Save ('editor-'+$phase) @{root=$root;path=$name;editorPID=$np[0].Id;editorHWND=$nh.ToInt64();method='root edit verb > fresh Notepad > real CtrlS';sha256=(Hash $dest)}
  [void][CacheGUIWin]::PostMessage($nh,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)
  RootWindow
 }elseif($Action -eq 'Drag'){
  RootWindow;$name=[string]$m.names.drag;$dest=Join-Path $root $name;$source=Join-Path $m.sourceDirectory $name
  if((Test-Path -LiteralPath $dest) -or (Hash $source) -cne $m.sha256.drag){throw 'Drag target/source changed'}
  $point=BackgroundPoint;$sx=$point.x-100;$sy=$point.y
  foreach($dx in @(-25,0,25)){foreach($dy in @(-20,0,20)){[EAInput]::Background($h,$pidExpected,($sx+$dx),($sy+$dy))}}
  $effect=[EAInput]::Drag($h,$pidExpected,$source,$sx,$sy,$point.x,$point.y)
  Save 'drag' @{root=$root;path=$name;method='real SendInput OLE drag/drop';effect=$effect;diagnostic=[EAInput]::LastDragDiagnostic;copyCommandUsed=$false}
  if($effect -ne 'Copy'){throw 'Real OLE drop not accepted'}
  WaitFile $dest ([string]$m.sha256.drag);RootWindow
 }elseif($Action -eq 'Copy'){
  RootWindow;$name=[string]$m.names.copy;$dest=Join-Path $root $name;$source=Join-Path $m.sourceDirectory $name
  if((Test-Path -LiteralPath $dest) -or (Hash $source) -cne $m.sha256.copy){throw 'Copy target/source changed'}
  $sw=OpenDedicated ([string]$m.sourceDirectory);$sourceH=[IntPtr][long]$sw.HWND;[uint32]$sp=0;[void][CacheGUIWin]::GetWindowThreadProcessId($sourceH,[ref]$sp)
  if((Get-Process -Id $sp).Path -ine $explorerPath){throw 'Source is not Explorer'}
  $items=@($sw.Document.Folder.Items()|Where-Object {$_.Path -ieq $source});if($items.Count -ne 1){throw 'Copy source item not exact'}
  SelectItem $sw $sourceH $sp $items[0];[EAInput]::Shortcut($sourceH,$sp,0x43);Start-Sleep -Milliseconds 300
  $drops=@([System.Windows.Forms.Clipboard]::GetFileDropList());if($drops.Count -ne 1 -or $drops[0] -ine $source){throw 'Clipboard is not exact Explorer copy source'}
  $point=BackgroundPoint;[EAInput]::LeftClickBackground($h,$pidExpected,$point.x,$point.y)
  if(@([System.Windows.Forms.Clipboard]::GetFileDropList())[0] -ine $source){throw 'Clipboard changed before paste'}
  [EAInput]::Shortcut($h,$pidExpected,0x56);WaitFile $dest ([string]$m.sha256.copy)
  Save 'copy' @{root=$root;path=$name;method='Explorer source CtrlC then root CtrlV';copyItemUsed=$false;sha256=(Hash $dest)}
  [void][CacheGUIWin]::PostMessage($sourceH,0x0010,[IntPtr]::Zero,[IntPtr]::Zero);RootWindow
 }elseif($Action -eq 'Export'){
  RootWindow;$name=[string]$m.names.new;$source=Join-Path $root $name;$dest=Join-Path $m.exportDirectory $name
  if(Test-Path -LiteralPath $dest){throw 'Export destination already exists'}
  $items=@($w.Document.Folder.Items()|Where-Object {$_.Path -ieq $source});if($items.Count -ne 1){throw 'Export source missing'}
  SelectItem $w $h $pidExpected $items[0];[EAInput]::Shortcut($h,$pidExpected,0x43);Start-Sleep -Milliseconds 300
  $drops=@([System.Windows.Forms.Clipboard]::GetFileDropList());if($drops.Count -ne 1 -or $drops[0] -ine $source){throw 'Export clipboard is not exact source'}
  $ew=OpenDedicated ([string]$m.exportDirectory);$eh=[IntPtr][long]$ew.HWND;[uint32]$ep=0;[void][CacheGUIWin]::GetWindowThreadProcessId($eh,[ref]$ep)
  if((Get-Process -Id $ep).Path -ine $explorerPath){throw 'Export target is not Explorer'}
  [void][CacheGUIWin]::ShowWindow($eh,3);[void][CacheGUIWin]::Focus($eh);Start-Sleep -Milliseconds 200
  $eui=[System.Windows.Automation.AutomationElement]::FromHandle($eh)
  $pane=@($eui.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ClassNameProperty,'UIItemsView')))
  if($pane.Count -ne 1){throw 'Export file pane not unique'}
  $r=$pane[0].Current.BoundingRectangle;$x=[int]$r.Right-80;$y=[int]$r.Bottom-80
  [EAInput]::LeftClickBackground($eh,$ep,$x,$y)
  if(@([System.Windows.Forms.Clipboard]::GetFileDropList())[0] -ine $source){throw 'Export clipboard changed'}
  [EAInput]::Shortcut($eh,$ep,0x56);WaitFile $dest ([string]$m.sha256.edit)
  Save 'export' @{source=$source;destination=$dest;method='real root CtrlC > external Explorer CtrlV';sha256=(Hash $dest);copyItemUsed=$false}
  [void][CacheGUIWin]::PostMessage($eh,0x0010,[IntPtr]::Zero,[IntPtr]::Zero);RootWindow
 }
 Write-Output ('GUI_ROOT_PHASE_OK '+$Action)
}catch{
 Save ('failure-'+$Action) @{status='notAccepted';reason=$_.Exception.Message;line=$_.InvocationInfo.ScriptLineNumber;root=$root;hwnd=$h.ToInt64();pid=$pidExpected}
 Write-Output $_.Exception.Message;exit 1
}
