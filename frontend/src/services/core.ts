import {
  AcceptTransfer,
  AcceptTransferAs,
  AdminCreateUser,
  AdminAiConfigGet,
  AdminAiConfigSave,
  AdminCapacity,
  AdminDeleteUser,
  AdminGrantShared,
  AdminListSpaces,
  AdminListUsers,
  AdminRegisterEnabled,
  AdminResetPassword,
  AdminSetPersonalQuota,
  AdminSetRegisterEnabled,
  AdminSetSharedQuota,
  AdminSetUserStatus,
  AdminListDepts,
  AdminSharedMembers,
  AdminKnowledgeStats,
  AppVersion,
  ApplyUpdate,
  CheckUpdate,
  CfapiMountPath,
  ClearHistory,  CloudDelete,
  CloudDownload,
  CloudFreeUpSpace,
  CloudKeepOnDevice,
  CloudRename,
  CloudMove,
  CloudMkdir,
  CloudStar,
  CloudStars,
  CloudTouch,
  CloudRecents,
  CloudList,
  CloudPreview,
  CloudRecycleList,
  CloudRecyclePurge,
  CloudRecycleRestore,
  CloudShare,
  CloudShareLink,
  CloudShares,
  CloudRevokeShare,
  CloudVersions,
  CloudRestoreVersion,
  CloudUpload,
  CloudUploadFolder,
  CloudUploadPaths,
  CurrentUser,
  DeleteTask,
  GetLogDirectory,
  GetSettings,
  GetSnapshot,
  KnowledgeAsk,
  KnowledgeHealth,
  KnowledgeLogin,
  KnowledgeLogout,
  KnowledgeStatus,
  Login,
  Logout,
  MeetingArchiveToKnowledge,
  MeetingAsk,
  MeetingDetail,
  MeetingKeywordsRegenerate,
  MeetingHotwordRemove,
  MeetingHotwordsAdd,
  MeetingHotwordsGet,
  MeetingMindmapRegenerate,
  MeetingSummaryRegenerate,
  MeetingRemove,
  MeetingRetranscribe,
  MeetingTodoAdd,
  MeetingTodoRemove,
  MeetingTodoUpdate,
  MeetingsList,
  MeetingSpeakerRename,
  MeetingStreamChunk,
  MeetingStreamOpen,
  MeetingStreamStop,
  MeetingStreamWarm,
  MeetingVoiceprintRemove,
  MeetingVoiceprintsGet,
  MeetingLivePoints,
  McpConnectionGet,
  McpConnectionRegenerate,
  MySpaces,
  OpenAdminConsole,
  OpenCfapiFolder,
  OpenFile,
  OpenReceiveFolder,
  OpenUpdatesFolder,
  PluginInstallFromMarket,
  PluginInstallFromPath,
  PluginInvoke,
  PluginList,
  PluginMarketList,
  PluginPreviewFromMarket,
  PluginSetDisabled,
  PluginUninstall,
  ProcessDroppedFiles,
  RejectTransfer,
  ReportFrontendError,
  SaveSettings,
  SaveServiceConfig,
  SelectFile,
  SelectFiles,
  SelectReceiveDirectory,
  SelectShareDirectory,
  SendBatch,
  SendFile,
  ServiceEndpoints,
  ShutdownAll,
  StartDrive,
  StartUpdateDownload,
  StopDrive,
} from '../../wailsjs/go/main/App'
import type {
  AuthUser,
  Capacity,
  CloudFile,
  CloudPreview as CloudPreviewData,
  CoreSnapshot,
  DroppedFiles,
  AdminDept,
  AdminSharedMember,
  KnowledgeStats,
  KnowledgeAnswer,
  KnowledgeHealth as KnowledgeHealthData,
  KnowledgeStatus as KnowledgeStatusData,
  ManagedUserPage,
  AiCapabilityConfig,
  MarketItem,
  MeetingArchiveResult,  MeetingAskResult,
  MeetingChatTurn,
  MeetingLiveView,
  MeetingHotwords,
  MeetingVoiceprint,
  McpConnection,
  MeetingRecord,
  PluginInfo,
  RecycleEntry,
  ShareLink,
  VersionEntry,
  PluginInvokeResult,
  PluginPreview,
  Space,
  ServiceEndpoints as ServiceEndpointsData,
  UpdateCheckResult,
} from '../types/core'

export interface SettingsData {
  deviceName: string
  receiveDir: string
  webdavRoot: string
}

export const core = {
  snapshot: () => GetSnapshot() as Promise<CoreSnapshot>,
  selectFile: SelectFile,
  selectFiles: SelectFiles,
  processDroppedFiles: (paths: string[]) => ProcessDroppedFiles(paths) as Promise<DroppedFiles>,
  send: SendFile,
  sendBatch: SendBatch,
  accept: AcceptTransfer,
  acceptAs: AcceptTransferAs,
  reject: RejectTransfer,
  startDrive: StartDrive,
  stopDrive: StopDrive,
  shutdown: ShutdownAll,
  logDirectory: GetLogDirectory,
  reportError: ReportFrontendError,
  getSettings: () => GetSettings() as Promise<SettingsData>,
  saveSettings: SaveSettings,
  selectReceiveDirectory: SelectReceiveDirectory,
  selectShareDirectory: SelectShareDirectory,
  clearHistory: ClearHistory,
  deleteTask: DeleteTask,
  openFile: OpenFile,
  openReceiveFolder: OpenReceiveFolder,
  // CfApi 占位符目录（切片 5）：空串 = 未挂载（旧系统/非 Windows/未登录），前端隐藏入口
  cfapiMountPath: () => CfapiMountPath() as Promise<string>,
  openCfapiFolder: OpenCfapiFolder,
  cloudList: () => CloudList() as Promise<CloudFile[]>,
  cloudPreview: (key: string) => CloudPreview(key) as Promise<CloudPreviewData>,
  cloudUpload: (targetDir: string) => CloudUpload(targetDir) as Promise<string>,
  cloudUploadFolder: (targetDir: string) => CloudUploadFolder(targetDir) as Promise<string>,
  cloudUploadPaths: CloudUploadPaths,
  cloudDownload: CloudDownload,
  cloudDelete: CloudDelete,
  // 长效可撤销的分享；cloudShare 是 10 分钟预签名，只用于「马上下一份」
  cloudShareLink: (key: string, expiresHours: number) =>
    CloudShareLink(key, expiresHours) as Promise<ShareLink>,
  cloudShares: () => CloudShares() as Promise<ShareLink[]>,
  cloudRevokeShare: CloudRevokeShare,
  // 版本历史：只有被覆盖过的文件才有历史，空数组是正常结果
  cloudVersions: (key: string) => CloudVersions(key) as Promise<VersionEntry[]>,
  cloudRestoreVersion: CloudRestoreVersion,
  // 改名只动同目录末段（Go 侧 CloudRename 走控制面 /move，拒绝路径分隔符）
  cloudRename: CloudRename,
  // 移动只换目录、名字不变（同样走 /move，但拒绝 .. 逃逸）
  cloudMove: CloudMove,
  // 新建文件夹：targetDir='' 表示当前根；重复创建同名幂等成功
  cloudMkdir: CloudMkdir,
  // 本地保留两个开关：作用于「本地挂载」的占位符，只在 CfapiMountPath 非空时可点
  // （Go 侧按云端路径映射到同步根下的本地文件，越界路径一律拒绝）
  cloudKeepOnDevice: CloudKeepOnDevice,
  cloudFreeUpSpace: CloudFreeUpSpace,
  // 星标：用户 × 路径的标记；最近访问是埋点，失败可忽略
  cloudStar: CloudStar,
  cloudStars: CloudStars,
  cloudTouch: CloudTouch,
  cloudRecents: CloudRecents,
  cloudShare: CloudShare,
  // 回收站（P1）：列表/还原/彻底删除；还原冲突时控制面返回可读错误
  cloudRecycleList: () => CloudRecycleList() as Promise<RecycleEntry[]>,
  cloudRecycleRestore: CloudRecycleRestore,
  cloudRecyclePurge: CloudRecyclePurge,
  knowledgeStatus: () => KnowledgeStatus() as Promise<KnowledgeStatusData>,
  knowledgeLogin: (serverUrl: string, username: string, password: string) =>
    KnowledgeLogin(serverUrl, username, password) as Promise<KnowledgeStatusData>,
  knowledgeLogout: KnowledgeLogout,
  knowledgeHealth: () => KnowledgeHealth() as Promise<KnowledgeHealthData>,
  knowledgeAsk: (question: string) => KnowledgeAsk(question) as Promise<KnowledgeAnswer>,
  // 会议听记：小请求走绑定；上传/音频回放走同源 /kapi 网关（大文件不进绑定 JSON 通道）
  meetingsList: (limit = 50) => MeetingsList(limit) as Promise<MeetingRecord[]>,
  meetingDetail: (id: string) => MeetingDetail(id) as Promise<MeetingRecord>,
  meetingRemove: MeetingRemove,
  meetingSpeakerRename: (id: string, speaker: string, name: string) =>
    MeetingSpeakerRename(id, speaker, name) as Promise<MeetingRecord>,
  meetingArchive: (id: string) => MeetingArchiveToKnowledge(id) as Promise<MeetingArchiveResult>,
  meetingMindmapRegenerate: (id: string) => MeetingMindmapRegenerate(id) as Promise<MeetingRecord>,
  meetingSummaryRegenerate: (id: string, templateId = '') =>
    MeetingSummaryRegenerate(id, templateId) as Promise<MeetingRecord>,
  meetingAsk: (id: string, question: string, history: MeetingChatTurn[]) =>
    MeetingAsk(id, question, history) as Promise<MeetingAskResult>,
  // 区块级补跑与待办实体（对标钉钉分任务状态 + MinutesTodoI）
  meetingKeywordsRegenerate: (id: string) => MeetingKeywordsRegenerate(id) as Promise<MeetingRecord>,
  meetingTodoAdd: (id: string, task: string, ownerHint = '', dueHint = '') =>
    MeetingTodoAdd(id, task, ownerHint, dueHint) as Promise<MeetingRecord>,
  meetingTodoUpdate: (id: string, todoId: string, task: string, ownerHint: string, dueHint: string, done: boolean) =>
    MeetingTodoUpdate(id, todoId, task, ownerHint, dueHint, done) as Promise<MeetingRecord>,
  meetingTodoRemove: (id: string, todoId: string) => MeetingTodoRemove(id, todoId) as Promise<MeetingRecord>,
  // 实时上屏：开会话 → 边录边推 600ms 分片 → 收尾；终态仍由整段离线重跑给出
  meetingStreamWarm: () => MeetingStreamWarm() as Promise<void>,
  meetingStreamOpen: (title: string, templateId = '') => MeetingStreamOpen(title, templateId) as Promise<MeetingRecord>,
  meetingStreamChunk: (id: string, pcmBase64: string) => MeetingStreamChunk(id, pcmBase64) as Promise<MeetingLiveView>,
  meetingStreamStop: (id: string) => MeetingStreamStop(id) as Promise<MeetingLiveView>,
  meetingLivePoints: (id: string) => MeetingLivePoints(id) as Promise<string[]>,
  // 个性化热词与重新转写（加了热词后让老记录重跑）
  meetingHotwordsGet: () => MeetingHotwordsGet() as Promise<MeetingHotwords>,
  meetingHotwordsAdd: (words: string[]) => MeetingHotwordsAdd(words) as Promise<MeetingHotwords>,
  meetingHotwordRemove: (word: string) => MeetingHotwordRemove(word) as Promise<MeetingHotwords>,
  meetingRetranscribe: (id: string) => MeetingRetranscribe(id) as Promise<MeetingRecord>,
  // 声纹元数据查看与删除（生物特征数据必须可删）
  meetingVoiceprints: () => MeetingVoiceprintsGet() as Promise<MeetingVoiceprint[]>,
  meetingVoiceprintRemove: (name: string) => MeetingVoiceprintRemove(name) as Promise<MeetingVoiceprint[]>,
  mcpInfo: () => McpConnectionGet() as Promise<McpConnection>,
  mcpRegenerate: () => McpConnectionRegenerate() as Promise<McpConnection>,
  meetingUpload: async (blob: Blob, title: string, templateId = '', meetingId = ''): Promise<MeetingRecord> => {
    const form = new FormData()
    form.append('audio', blob, 'meeting.wav')
    form.append('title', title)
    if (templateId) form.append('template_id', templateId)
    // 复用实时会话先建的那条记录，避免一场会落两条听记
    if (meetingId) form.append('meeting_id', meetingId)
    const response = await fetch('/kapi/meetings/transcribe', { method: 'POST', body: form })
    if (!response.ok) throw new Error((await response.text()) || `上传失败（${response.status}）`)
    return response.json()
  },
  meetingAudioObjectUrl: async (id: string): Promise<string> => {
    const response = await fetch(`/kapi/meetings/${encodeURIComponent(id)}/audio`)
    if (!response.ok) throw new Error(`音频加载失败（${response.status}）`)
    return URL.createObjectURL(await response.blob())
  },
  // 租户服务发现：登录后下发服务地址（知识页免手填），管理员可登记
  serviceEndpoints: () => ServiceEndpoints() as Promise<ServiceEndpointsData>,
  saveServiceConfig: (knowledgeUrl: string) => SaveServiceConfig(knowledgeUrl) as Promise<void>,
  // 账号控制面（P1）
  login: (username: string, password: string) => Login(username, password) as Promise<AuthUser>,
  logout: Logout,
  currentUser: () => CurrentUser() as Promise<AuthUser>,
  // 管理页（P3）：客户端内自绘，直接调控制面。鉴权在控制面，非管理员一律被拒。
  adminListUsers: (pageNum: number, pageSize: number) =>
    AdminListUsers(pageNum, pageSize) as Promise<ManagedUserPage>,
  adminCreateUser: (userName: string, nickName: string, password: string) =>
    AdminCreateUser({ userName, nickName, password } as never),
  adminSetUserStatus: AdminSetUserStatus,
  adminResetPassword: AdminResetPassword,
  adminDeleteUser: AdminDeleteUser,
  adminRegisterEnabled: () => AdminRegisterEnabled() as Promise<boolean>,
  adminSetRegisterEnabled: AdminSetRegisterEnabled,
  // 空间与配额（P3）：管理页在一处设定共享容量与逐账号个人配额
  mySpaces: () => MySpaces() as Promise<Space[]>,
  adminListSpaces: () => AdminListSpaces() as Promise<Space[]>,
  adminCapacity: () => AdminCapacity() as Promise<Capacity>,
  // AI 能力配置（控制面权威；保存后控制面推送知识服务热生效）
  adminAiConfigGet: () => AdminAiConfigGet() as Promise<AiCapabilityConfig>,
  adminAiConfigSave: (llmBaseUrl: string, llmModel: string, llmApiKey: string, asrEnabled: boolean | null, asrSpeakerEnabled: boolean | null) =>
    AdminAiConfigSave(llmBaseUrl, llmModel, llmApiKey, asrEnabled, asrSpeakerEnabled) as Promise<AiCapabilityConfig>,
  adminSharedMembers: () => AdminSharedMembers() as Promise<AdminSharedMember[]>,
  adminKnowledgeStats: (days: number) => AdminKnowledgeStats(days) as Promise<KnowledgeStats>,
  adminListDepts: () => AdminListDepts() as Promise<AdminDept[]>,
  adminGrantShared: (memberType: string, memberId: string, permission: string) =>
    AdminGrantShared(memberType, memberId, permission) as Promise<void>,
  adminSetPersonalQuota: AdminSetPersonalQuota,
  adminSetSharedQuota: AdminSetSharedQuota,
  // RuoYi 自带后台：仅本产品不复刻的运维动作（菜单/字典/定时任务）用得到
  openAdminConsole: OpenAdminConsole,
  // 在线升级（appupdate.go）：检查 → 下载（进度经 update:progress 事件）→ 应用
  appVersion: () => AppVersion() as Promise<string>,
  checkUpdate: () => CheckUpdate() as Promise<UpdateCheckResult>,
  startUpdateDownload: StartUpdateDownload,
  applyUpdate: ApplyUpdate,
  openUpdatesFolder: OpenUpdatesFolder,
  // 插件系统（appplugin.go）：能力调用走动态通道，不随能力增减改绑定
  pluginList: () => PluginList() as Promise<PluginInfo[]>,
  pluginInvoke: (pluginId: string, api: string, args: unknown) =>
    PluginInvoke(pluginId, api, JSON.stringify(args ?? {})) as Promise<PluginInvokeResult>,
  pluginInstallFromPath: (path: string) => PluginInstallFromPath(path) as Promise<PluginInfo>,
  pluginSetDisabled: PluginSetDisabled,
  pluginUninstall: PluginUninstall,
  // 插件商城（RuoYi /easyshare/plugins）：列表已按本地版本回填 updateAvailable
  pluginMarketList: () => PluginMarketList() as Promise<MarketItem[]>,
  // 商城安装两段式：先预览（返回需确认的权限），用户同意后带 acceptedPermissions 安装
  pluginPreviewFromMarket: (assetId: string, sha256: string, sizeBytes: number) =>
    PluginPreviewFromMarket(assetId, sha256, sizeBytes) as Promise<PluginPreview>,
  pluginInstallFromMarket: (assetId: string, sha256: string, sizeBytes: number, acceptedPermissions: string[]) =>
    PluginInstallFromMarket(assetId, sha256, sizeBytes, acceptedPermissions) as Promise<PluginInfo>,
}
