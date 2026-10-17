// layout_check.c —— 用真实 C 编译器取 CfApi 结构体布局的**基准值**。
//
// 为什么要有这个文件：internal/cfapi 是 FFI 边界，字段顺序或填充错一格，
// 内核会读到垃圾指针，表现是随机崩溃而非干净报错。Go 侧的断言
// （layout_windows_test.go）不允许手算期望值——期望值必须从这里取得。
//
// 本文件刻意**不 include Windows SDK 头**：一是避免依赖 Windows Kits 安装，
// 二是让「我们以为的结构」显式写在代码里，改错了一眼能看出来。
// 类型定义与 cfapi.h / winbase.h 逐字段对应（本机 SDK 10.0.26100.0）。
//
// 复现：
//   D:/Develop/mingw64/bin/gcc.exe layout_check.c -o x && ./x
//
// 注：本机 MinGW 无 32 位 multilib，故只能取 x86_64 的基准；32 位布局需另测。
#include <stdio.h>
#include <stddef.h>

typedef unsigned int   ULONG;
typedef unsigned int   DWORD;
typedef unsigned int   NTSTATUS;
typedef long long      LARGE_INTEGER;
typedef long long      USN;
typedef long           HRESULT;
typedef const wchar_t* LPCWSTR;
typedef const void*    LPCVOID;
typedef void*          LPVOID;

typedef struct _GUID {
  unsigned int   Data1;
  unsigned short Data2;
  unsigned short Data3;
  unsigned char  Data4[8];
} GUID;

/* winbase.h */
typedef struct _FILE_BASIC_INFO {
  LARGE_INTEGER CreationTime;
  LARGE_INTEGER LastAccessTime;
  LARGE_INTEGER LastWriteTime;
  LARGE_INTEGER ChangeTime;
  DWORD         FileAttributes;
} FILE_BASIC_INFO;

/* cfapi.h */
typedef struct CF_FS_METADATA {
  FILE_BASIC_INFO BasicInfo;
  LARGE_INTEGER   FileSize;
} CF_FS_METADATA;

typedef struct CF_SYNC_REGISTRATION {
  ULONG   StructSize;
  LPCWSTR ProviderName;
  LPCWSTR ProviderVersion;
  LPCVOID SyncRootIdentity;
  DWORD   SyncRootIdentityLength;
  LPCVOID FileIdentity;
  DWORD   FileIdentityLength;
  GUID    ProviderId;
} CF_SYNC_REGISTRATION;

typedef struct CF_HYDRATION_POLICY { unsigned short Primary, Modifier; } CF_HYDRATION_POLICY;
typedef struct CF_POPULATION_POLICY { unsigned short Primary, Modifier; } CF_POPULATION_POLICY;

typedef struct CF_SYNC_POLICIES {
  ULONG               StructSize;
  CF_HYDRATION_POLICY Hydration;
  CF_POPULATION_POLICY Population;
  DWORD               InSync;
  DWORD               HardLink;
  DWORD               PlaceholderManagement;
} CF_SYNC_POLICIES;

typedef struct CF_PLACEHOLDER_CREATE_INFO {
  LPCWSTR               RelativeFileName;
  CF_FS_METADATA        FsMetadata;
  LPCVOID               FileIdentity;
  DWORD                 FileIdentityLength;
  DWORD                 Flags;
  HRESULT               Result;
  USN                   CreateUsn;
} CF_PLACEHOLDER_CREATE_INFO;

/* 回调与操作（切片 5 第二阶段） */
typedef LARGE_INTEGER CF_CONNECTION_KEY;
typedef LARGE_INTEGER CF_TRANSFER_KEY;
typedef LARGE_INTEGER CF_REQUEST_KEY;

typedef struct CF_CALLBACK_INFO {
  DWORD             StructSize;
  CF_CONNECTION_KEY ConnectionKey;
  LPVOID            CallbackContext;
  LPCWSTR           VolumeGuidName;
  LPCWSTR           VolumeDosName;
  DWORD             VolumeSerialNumber;
  LARGE_INTEGER     SyncRootFileId;
  LPCVOID           SyncRootIdentity;
  DWORD             SyncRootIdentityLength;
  LARGE_INTEGER     FileId;
  LARGE_INTEGER     FileSize;
  LPCVOID           FileIdentity;
  DWORD             FileIdentityLength;
  LPCWSTR           NormalizedPath;
  CF_TRANSFER_KEY   TransferKey;
  unsigned char     PriorityHint;
  void*             CorrelationVector;
  void*             ProcessInfo;
  CF_REQUEST_KEY    RequestKey;
} CF_CALLBACK_INFO;

/* cfapi.h 的 CF_PROCESS_INFO（本机 SDK 10.0.26100.0，字段逐字照抄）。

   ⚠️ 2026-10-16 记录一次真实翻车：这份声明**第一版是凭记忆写的**，
   臆造了 `Flags` 与 `ProcessName` 两个字段、把 `ProcessId` 放到末尾，
   算出来 size=32 / ProcessId off=24。真机跑出来的进程名是字面量 "UNKNOWN"、
   PID 是 39891xxxxx 这种垃圾值。
   对照真实头文件后才看清：**根本没有 Flags / ProcessName**，
   PID 在 offset 4，进程名是 offset 8 的 ImagePath（全路径）。
   教训与本文件开头那句一致，只是这次犯错的是本文件自己：
   **布局不能凭记忆写，必须核对头文件或让编译器算。** */
typedef struct CF_PROCESS_INFO {
    DWORD StructSize;       /* off  0 */
    DWORD ProcessId;        /* off  4 */
    LPCWSTR ImagePath;      /* off  8  完整镜像路径（进程名要从这里取 basename） */
    LPCWSTR PackageName;    /* off 16 */
    LPCWSTR ApplicationId;  /* off 24 */
    LPCWSTR CommandLine;    /* off 32 */
    DWORD SessionId;        /* off 40 */
} CF_PROCESS_INFO;          /* size 48 */

typedef struct CF_CALLBACK_PARAMETERS_FETCH {
  ULONG         ParamSize;
  DWORD         Flags;
  LARGE_INTEGER RequiredFileOffset;
  LARGE_INTEGER RequiredLength;
  LARGE_INTEGER OptionalFileOffset;
  LARGE_INTEGER OptionalLength;
  LARGE_INTEGER LastDehydrationTime;
  DWORD         LastDehydrationReason;
} CF_CALLBACK_PARAMETERS_FETCH;

typedef struct CF_OPERATION_INFO {
  ULONG             StructSize;
  DWORD             Type;
  CF_CONNECTION_KEY ConnectionKey;
  CF_TRANSFER_KEY   TransferKey;
  void*             CorrelationVector;
  void*             SyncStatus;
  CF_REQUEST_KEY    RequestKey;
} CF_OPERATION_INFO;

typedef struct CF_OPERATION_PARAMETERS {
  ULONG   ParamSize;
  DWORD   _pad;
  struct {
    DWORD   Flags;
    DWORD   CompletionStatus;
    LPVOID  Buffer;
    LARGE_INTEGER Offset;
    LARGE_INTEGER Length;
  } TransferData;
} CF_OPERATION_PARAMETERS;

#define SHOW(T) printf("%-30s size=%2zu\n", #T, sizeof(T))
#define OFF(T, F) printf("    %-26s off=%2zu\n", #T "." #F, offsetof(T, F))

int main(void) {
  printf("ptr=%zu\n", sizeof(void*));
  SHOW(FILE_BASIC_INFO);
  OFF(FILE_BASIC_INFO, FileAttributes);
  SHOW(CF_FS_METADATA);
  OFF(CF_FS_METADATA, FileSize);
  SHOW(CF_SYNC_REGISTRATION);
  OFF(CF_SYNC_REGISTRATION, ProviderName);
  OFF(CF_SYNC_REGISTRATION, ProviderId);
  SHOW(CF_SYNC_POLICIES);
  OFF(CF_SYNC_POLICIES, Hydration);
  OFF(CF_SYNC_POLICIES, InSync);
  SHOW(CF_PLACEHOLDER_CREATE_INFO);
  OFF(CF_PLACEHOLDER_CREATE_INFO, FsMetadata);
  OFF(CF_PLACEHOLDER_CREATE_INFO, FileIdentity);
  OFF(CF_PLACEHOLDER_CREATE_INFO, Flags);
  OFF(CF_PLACEHOLDER_CREATE_INFO, Result);
  OFF(CF_PLACEHOLDER_CREATE_INFO, CreateUsn);
  printf("--- 回调 / 操作 ---\n");
  SHOW(CF_CALLBACK_INFO);
  OFF(CF_CALLBACK_INFO, CallbackContext);
  OFF(CF_CALLBACK_INFO, SyncRootIdentity);
  OFF(CF_CALLBACK_INFO, FileId);
  OFF(CF_CALLBACK_INFO, FileIdentity);
  OFF(CF_CALLBACK_INFO, NormalizedPath);
  OFF(CF_CALLBACK_INFO, TransferKey);
  OFF(CF_CALLBACK_INFO, ProcessInfo);
  SHOW(CF_PROCESS_INFO);
  OFF(CF_PROCESS_INFO, ProcessId);
  OFF(CF_PROCESS_INFO, ImagePath);
  OFF(CF_PROCESS_INFO, PackageName);
  OFF(CF_PROCESS_INFO, ApplicationId);
  OFF(CF_PROCESS_INFO, CommandLine);
  OFF(CF_PROCESS_INFO, SessionId);
  OFF(CF_CALLBACK_INFO, RequestKey);
  SHOW(CF_CALLBACK_PARAMETERS_FETCH);
  OFF(CF_CALLBACK_PARAMETERS_FETCH, RequiredFileOffset);
  OFF(CF_CALLBACK_PARAMETERS_FETCH, RequiredLength);
  SHOW(CF_OPERATION_INFO);
  OFF(CF_OPERATION_INFO, ConnectionKey);
  OFF(CF_OPERATION_INFO, TransferKey);
  OFF(CF_OPERATION_INFO, RequestKey);
  SHOW(CF_OPERATION_PARAMETERS);
  OFF(CF_OPERATION_PARAMETERS, TransferData);
  return 0;
}
