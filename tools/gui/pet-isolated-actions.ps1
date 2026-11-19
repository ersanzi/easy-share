param([Parameter(Mandatory=$true)][string]$Manifest)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
Add-Type -AssemblyName System.Drawing,System.Windows.Forms
Add-Type @'
using System;using System.Runtime.InteropServices;
public static class PetInput {
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int L,T,R,B; }
 [StructLayout(LayoutKind.Explicit,Size=40)] public struct Input { [FieldOffset(0)] public uint type;[FieldOffset(8)] public int dx;[FieldOffset(12)] public int dy;[FieldOffset(16)] public uint mouseData;[FieldOffset(20)] public uint flags;[FieldOffset(24)] public uint time;[FieldOffset(32)] public IntPtr extra; }
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h,out Rect r);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] public static extern uint GetDoubleClickTime();
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h,out uint p);
 [DllImport("user32.dll")] public static extern IntPtr WindowFromPoint(long point);
 [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")] public static extern uint SendInput(uint n,Input[] a,int size);
 [DllImport("user32.dll")] public static extern int GetWindowRgn(IntPtr h,IntPtr r);
 [DllImport("gdi32.dll")] public static extern IntPtr CreateRectRgn(int l,int t,int r,int b);
 [DllImport("gdi32.dll")] public static extern bool DeleteObject(IntPtr h);
 [DllImport("gdi32.dll")] public static extern int GetRgnBox(IntPtr h,out Rect r);
 public static void Button(bool down) {var a=new Input[]{new Input{type=0,flags=down?2u:4u}};if(SendInput(1,a,40)!=1)throw new Exception("SendInput rejected");}
 public static Rect Bounds(IntPtr h){Rect r;if(!GetWindowRect(h,out r))throw new Exception("Window bounds failed");return r;}
}
'@
$m=Get-Content -Encoding UTF8 -Raw -LiteralPath $Manifest|ConvertFrom-Json
$pet=[IntPtr][long]$m.pet;$panel=[IntPtr][long]$m.panel
foreach($h in @($pet,$panel)){[uint32]$owner=0;[void][PetInput]::GetWindowThreadProcessId($h,[ref]$owner);if($owner -ne [uint32]$m.pid){throw 'Not owned isolated windows'}}
function WaitVisible($h,$expected){for($i=0;$i -lt 30;$i++){if([PetInput]::IsWindowVisible($h) -eq $expected){return};Start-Sleep -Milliseconds 100};throw ('Visibility mismatch expected='+$expected)}
function WaitExpanded($h){$stable=0;$last='';for($i=0;$i -lt 80;$i++){$r=[PetInput]::Bounds($h);$clip=New-Object PetInput+Rect;$rgn=[PetInput]::CreateRectRgn(0,0,0,0);try{$kind=[PetInput]::GetWindowRgn($h,$rgn);[void][PetInput]::GetRgnBox($rgn,[ref]$clip)}finally{[void][PetInput]::DeleteObject($rgn)};$key=($r.L,$r.T,$r.R,$r.B,$clip.L,$clip.T,$clip.R,$clip.B -join ',');$full=[PetInput]::IsWindowVisible($h) -and $kind -gt 1 -and $clip.T -le 0 -and $clip.L -le 0 -and $clip.B -ge ($r.B-$r.T) -and $clip.R -ge ($r.R-$r.L);if($key -ne $last){Write-Output ('SLIDE_FRAME hwnd='+$h+' rect,region='+$key+' full='+$full)};if($full -and $key -eq $last){$stable++}else{$stable=0};if($stable -ge 8){Write-Output ('ANIMATION_SETTLED hwnd='+$h);return};$last=$key;Start-Sleep -Milliseconds 25};throw 'Panel native rect/region did not reach stable fully-expanded frame'}
function MoveCenter($h){$r=[PetInput]::Bounds($h);$x=[int](($r.L+$r.R)/2);$y=[int](($r.T+$r.B)/2);[void][PetInput]::SetCursorPos($x,$y);Start-Sleep -Milliseconds 350;Write-Output ('MOVE hwnd='+$h+' x='+$x+' y='+$y+' hit='+[PetInput]::WindowFromPoint(([long]$y -shl 32) -bor [uint32]$x))}
function DoubleClick($h){MoveCenter $h;[PetInput]::Button($true);[PetInput]::Button($false);Start-Sleep -Milliseconds 70;[PetInput]::Button($true);[PetInput]::Button($false);$wait=[Math]::Max(600,[int][PetInput]::GetDoubleClickTime()+101);Write-Output ('DOUBLE_DEADLINE_WAIT_MS='+$wait);Start-Sleep -Milliseconds $wait}
function Screenshot($name){foreach($item in @(@{h=$pet;label='pet'},@{h=$panel;label='panel'})){$h=$item.h;[uint32]$owner=0;[void][PetInput]::GetWindowThreadProcessId($h,[ref]$owner);if($owner -ne [uint32]$m.pid -or ![PetInput]::IsWindowVisible($h)){throw 'Screenshot window not visible/owned'};$r=[PetInput]::Bounds($h);$size=[Drawing.Size]::new($r.R-$r.L,$r.B-$r.T);$raw=[Drawing.Bitmap]::new($size.Width,$size.Height);$clean=[Drawing.Bitmap]::new($size.Width,$size.Height);$capture=[Drawing.Graphics]::FromImage($raw);$g=[Drawing.Graphics]::FromImage($clean);$rgn=[PetInput]::CreateRectRgn(0,0,0,0);$region=$null;try{if([PetInput]::GetWindowRgn($h,$rgn) -le 1){throw 'Screenshot owned region absent'};$capture.CopyFromScreen($r.L,$r.T,0,0,$size);$region=[Drawing.Region]::FromHrgn($rgn);$g.Clear([Drawing.Color]::Transparent);$g.SetClip($region,[Drawing.Drawing2D.CombineMode]::Replace);$g.DrawImageUnscaled($raw,0,0);$clean.Save((Join-Path $m.root ($name+'-'+$item.label+'.png')),[Drawing.Imaging.ImageFormat]::Png)}finally{if($region){$region.Dispose()};[void][PetInput]::DeleteObject($rgn);$g.Dispose();$capture.Dispose();$clean.Dispose();$raw.Dispose()}}}
[void][PetInput]::SetCursorPos(10,10);Start-Sleep -Milliseconds 500
Write-Output 'PHASE initial'
WaitVisible $pet $true;WaitVisible $panel $false
$rgn=[PetInput]::CreateRectRgn(0,0,0,0);try{if([PetInput]::GetWindowRgn($pet,$rgn) -le 1){throw 'Native rounded region absent'}}finally{[void][PetInput]::DeleteObject($rgn)}
Write-Output 'PHASE hover'
MoveCenter $pet;WaitVisible $panel $true;WaitExpanded $panel
MoveCenter $panel;WaitVisible $panel $true;WaitExpanded $panel
Screenshot 'hover'
[void][PetInput]::SetCursorPos(10,10);WaitVisible $panel $false;WaitVisible $pet $true
Write-Output 'PHASE pin'
DoubleClick $pet
[void][PetInput]::SetCursorPos(10,10);Start-Sleep -Milliseconds 650;WaitVisible $panel $true;WaitExpanded $panel
Write-Output 'PHASE pinned-drag'
$before=[PetInput]::Bounds($pet);MoveCenter $pet
[PetInput]::Button($true)
for($i=1;$i -le 15;$i++){[void][PetInput]::SetCursorPos([int](($before.L+$before.R)/2)-$i*10,[int](($before.T+$before.B)/2)-$i*3);Start-Sleep -Milliseconds 30}
[PetInput]::Button($false);Start-Sleep -Milliseconds 300
$after=[PetInput]::Bounds($pet)
if([Math]::Abs($after.L-$before.L) -lt 60){throw 'Pinned pet did not move with real mouse drag'}
[void][PetInput]::SetCursorPos(10,10);WaitVisible $panel $true;WaitExpanded $panel
Screenshot 'pinned-dragged'
DoubleClick $pet
[void][PetInput]::SetCursorPos(10,10);WaitVisible $panel $false;WaitVisible $pet $true
@{method='real cursor and SendInput; isolated own HWND';nativeRoundedRegion=$true;hover=$true;crossPanel=$true;pinned=$true;pinnedDrag=$true;unpin=$true;before=@{x=$before.L;y=$before.T};after=@{x=$after.L;y=$after.T};serverUploadTested=$false}|ConvertTo-Json -Depth 5|Set-Content -Encoding UTF8 -LiteralPath (Join-Path $m.root 'result.json')
Write-Output 'PET_GUI_PASS'
