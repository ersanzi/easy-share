param([Parameter(Mandatory=$true)][string]$Root,[Parameter(Mandatory=$true)][string]$Identity,[Parameter(Mandatory=$true)][string]$Provider,[string]$Name='EasyShare 状态测试',[switch]$Remove)
$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$repository=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$Root=[IO.Path]::GetFullPath($Root)
$workspace=Join-Path $repository '.rd-test'
if(-not $Root.StartsWith($workspace+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)){throw 'Only isolated work root allowed'}
if((Get-Item -LiteralPath $Root).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Root cannot be a reparse before Shell registration'}
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null=[Windows.Storage.StorageFolder,Windows.Storage,ContentType=WindowsRuntime]
$null=[Windows.Storage.Provider.StorageProviderSyncRootInfo,Windows.Storage.Provider,ContentType=WindowsRuntime]
$null=[Windows.Storage.Provider.StorageProviderSyncRootManager,Windows.Storage.Provider,ContentType=WindowsRuntime]
$null=[Windows.Security.Cryptography.CryptographicBuffer,Windows.Security.Cryptography,ContentType=WindowsRuntime]
$sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$id='EasyShareTest!'+$sid+'!'+$Identity
if($Remove){[Windows.Storage.Provider.StorageProviderSyncRootManager]::Unregister($id);Write-Output 'SHELL_ROOT_REMOVED';exit}
$operation=[Windows.Storage.StorageFolder]::GetFolderFromPathAsync($Root)
$convert=@([System.WindowsRuntimeSystemExtensions].GetMethods()|Where-Object{$_.Name -eq 'AsTask' -and $_.IsGenericMethod -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'})[0]
$task=$convert.MakeGenericMethod([Windows.Storage.StorageFolder]).Invoke($null,@($operation));$task.Wait();$folder=$task.Result
$info=New-Object Windows.Storage.Provider.StorageProviderSyncRootInfo
$info.Id=$id;$info.Path=$folder;$info.DisplayNameResource=$Name;$info.IconResource='%SystemRoot%\System32\imageres.dll,-1040'
$info.HydrationPolicy=[Windows.Storage.Provider.StorageProviderHydrationPolicy]::Progressive
$info.PopulationPolicy=[Windows.Storage.Provider.StorageProviderPopulationPolicy]::AlwaysFull
$info.InSyncPolicy=[Windows.Storage.Provider.StorageProviderInSyncPolicy]::Default
$info.HardlinkPolicy=[Windows.Storage.Provider.StorageProviderHardlinkPolicy]::None
$info.Version='1.0';$info.AllowPinning=$true;$info.ShowSiblingsAsGroup=$false
$info.ProviderId=[Guid]$Provider
$bytes=[Text.Encoding]::UTF8.GetBytes($Identity)
Add-Type @'
using System;using System.Reflection;using System.Runtime.InteropServices;
public static class ShellContextBridge {
 [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int SetContext(IntPtr self,IntPtr buffer);
 public static void Assign(object info,object buffer){IntPtr unk=Marshal.GetIUnknownForObject(info), target=IntPtr.Zero, value=Marshal.GetIUnknownForObject(buffer);try{Guid iid=new Guid("7C1305C4-99F9-41AC-8904-AB055D654926");Marshal.ThrowExceptionForHR(Marshal.QueryInterface(unk,ref iid,out target));IntPtr fn=Marshal.ReadIntPtr(Marshal.ReadIntPtr(target),9*IntPtr.Size);var setter=(SetContext)Marshal.GetDelegateForFunctionPointer(fn,typeof(SetContext));Marshal.ThrowExceptionForHR(setter(target,value));}finally{if(target!=IntPtr.Zero)Marshal.Release(target);Marshal.Release(unk);Marshal.Release(value);}}
}
'@
[ShellContextBridge]::Assign($info,[Windows.Security.Cryptography.CryptographicBuffer]::CreateFromByteArray($bytes))
[Windows.Storage.Provider.StorageProviderSyncRootManager]::Register($info)
$registered=[Windows.Storage.Provider.StorageProviderSyncRootManager]::GetSyncRootInformationForId($id)
@{id=$id;path=$registered.Path.Path;name=$registered.DisplayNameResource;provider=$registered.ProviderId}|ConvertTo-Json -Compress
