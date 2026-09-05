export interface ServiceStatus {
  core: boolean
  discovery: boolean
  receiver: boolean
  webdav: boolean
  cloudEnabled: boolean
}

export interface Peer {
  deviceId: string
  deviceName: string
  ip: string
  transferPort: number
  lastSeen: string
}

export type TaskKind =
  | 'lan_send'
  | 'lan_receive'
  | 'cloud_upload'
  | 'cloud_download'
  | 'sync'
  | 'parse'

export type TaskStatus =
  | 'pending'
  | 'accepted'
  | 'running'
  | 'queued'
  | 'paused'
  | 'waiting_network'
  | 'completed'
  | 'rejected'
  | 'failed'
  | 'cancelled'

export type TaskDirection = 'send' | 'receive' | ''

export interface TransferTask {
  id: string
  // 旧 history.json 中没有 kind，前端按 direction 兼容推断。
  kind?: TaskKind
  fileName: string
  localPath?: string
  direction: TaskDirection
  peer: string
  batchId?: string
  totalBytes: number
  transferredBytes: number
  speed: number
  status: TaskStatus
  error?: string
  createdAt?: string
  updatedAt?: string
}

export type PreviewKind = 'unsupported' | 'image' | 'pdf' | 'text'

export interface CloudFile {
  key: string
  name: string
  size: number
  contentType: string
  lastModified: string
  previewKind: PreviewKind
  /** 控制面目录层的稳定文件 ID（2026-09-06 起）；旧控制面为 0 */
  fileId: number
}

export interface CloudPreview {
  key: string
  name: string
  kind: PreviewKind
  contentType: string
  size: number
  contentUrl?: string
  text?: string
  truncated?: boolean
}

export interface DroppedFiles {
  files: string[]
  dirs: string[]
}

// --- 知识问答（Core 作网关，令牌不进前端） ---
// 引用片段字段沿用知识服务线格式（snake_case）

export interface KnowledgeStatus {
  configured: boolean
  loggedIn: boolean
  serverUrl: string
  username: string
  role: string
}

/** 登录后下发的服务拓扑：registry=控制面登记，derived=同主机推导 */
export interface ServiceEndpoints {
  knowledgeUrl: string
  knowledgeUrlSource: 'registry' | 'derived'
  platformBaseUrl: string
}

export interface KnowledgeHealth {
  records: number
  llm: string
  watch_dirs: number
}

export interface KnowledgeSourceRef {
  doc_id?: string | null
  score?: number | null
  ingested_at?: string | null
}

export interface KnowledgeContext {
  doc_id?: string | null
  file_id?: string | null
  version_id?: string | null
  filename?: string | null
  score?: number | null
  ingested_at?: string | null
  text: string
  block_ids: string[]
}

export interface KnowledgeAnswer {
  answer: string
  sources: KnowledgeSourceRef[]
  contexts: KnowledgeContext[]
}

export interface CoreSnapshot {
  status: ServiceStatus
  peers: Peer[]
  tasks: TransferTask[]
}

// 账号控制面（RuoYi）登录态。token 只在桌面端进程保管，不下发前端。
export interface AuthUser {
  loggedIn: boolean
  userName: string
  nickName: string
  avatar: string
  // isAdmin 只控制「管理」入口显隐，不是鉴权：后台接口自己按 Sa-Token 角色拒绝。
  isAdmin: boolean
}

// 管理页的账号行。userId 是雪花 ID，必须按字符串透传——超出 JS 安全整数范围。
export interface ManagedUser {
  userId: string
  userName: string
  nickName: string
  deptName: string
  // "0" 正常、"1" 停用（沿用控制面口径，不转 bool）
  status: string
  createTime: string
  loginDate: string
}

export interface ManagedUserPage {
  total: number
  rows: ManagedUser[]
}

// 知识服务聚合统计（管理员汇总页；days 为观察窗口）
export interface KnowledgeStats {
  days: number
  total_queries: number
  recent_queries: number
  blind_spot_count: number
  generation: {
    total: number
    avg_faithfulness: number | null
    avg_unsupported_ratio: number | null
  }
  most_cited_docs: { file_id: string; count: number }[]
  documents: number
  llm: string
}

// 共享空间授权主体（部门级权限：user=按账号、dept=按部门）
export interface AdminSharedMember {
  memberType: string
  memberId: string
  permission: string
  name: string
}

// 管理页部门下拉条目（控制面只读投影 sys_dept）
export interface AdminDept {
  deptId: string
  deptName: string
}

// 容量总览。逐空间配额看不出「承诺总量是否超过物理磁盘」，这个视图专为此存在。
export interface Capacity {
  // enabled 为 false 表示控制面未配置容量探测路径，池上限不生效
  enabled: boolean
  // usableBytes/poolBytes 未启用时为 -1
  usableBytes: number
  poolBytes: number
  reservedBytes: number
  // committedBytes 是各空间配额之和，不含「不限」的空间
  committedBytes: number
  usedBytes: number
  // unlimitedCount > 0 时 committedBytes 是不完整的
  unlimitedCount: number
}

export type SpaceType = 'personal' | 'shared'

// 共享空间权限。个人空间的持有者是 'owner'，不来自成员表。
export type SpacePermission = 'owner' | 'write' | 'read'

// 一个空间的配额与实时用量。
// spaceId/ownerId 是雪花 ID，按字符串透传——超出 JS 安全整数范围。
export interface Space {
  spaceId: string
  spaceType: SpaceType
  ownerId: string
  spaceName: string
  // quotaBytes：0 未分配（显示「待开空间」）、-1 不限
  quotaBytes: number
  // usedBytes 由控制面实时聚合对象存储得出，不是库里的镜像值
  usedBytes: number
  status: string
  permission: SpacePermission | null
}

// 配额哨兵值，与 Go/Java 侧保持一致。
export const QUOTA_UNSET = 0
export const QUOTA_UNLIMITED = -1
// ─── 在线升级（appupdate.go / internal/update）───

// 一个可下载资产（安装包/DMG/zip）。URL 不在清单里，下载前现取。
export interface UpdateAssetInfo {
  id: string
  kind: string
  filename: string
  size: number
  sha256: string
}

// 一次升级检查的结果。hasUpdate 由 Go 侧 semver 比较得出。
export interface UpdateCheckResult {
  currentVersion: string
  latestVersion: string
  hasUpdate: boolean
  notes: string
  publishedAt: string
  asset?: UpdateAssetInfo
  // 是否 NSIS 安装版（Windows）；绿色版与 macOS 为 false
  installedMode: boolean
  // 能否「重启并更新」：Windows 安装版且有 installer 资产
  canAutoInstall: boolean
}

// update:progress 事件载荷（200ms 节流，Go 侧已算好速度）
export interface UpdateProgress {
  received: number
  total: number
  speed: number
}

// ─── 插件系统（appplugin.go / internal/plugin）───

// 一个已安装插件的信息（含内置/禁用状态与授权权限）。
export interface PluginInfo {
  id: string
  name: string
  version: string
  description: string
  icon: string
  entry: string
  builtin: boolean
  disabled: boolean
  permissions: string[]
}

// PluginInvoke 统一返回：ok=false 时 error 给出原因（未授权/未知能力等）。
export interface PluginInvokeResult {
  ok: boolean
  data?: unknown
  error?: string
}

// ─── 插件商城（/easyshare/plugins，MarketItem/MarketAsset）───

// 商城里一个插件包资产。
export interface MarketAsset {
  id: string
  filename: string
  sizeBytes: number
  sha256: string
}

// 商城里一个插件的最新清单（updateAvailable 由 Go 侧按本地版本回填）。
export interface MarketItem {
  id: string
  name: string
  description: string
  icon: string
  author: string
  version: string
  notes: string
  publishedAt: string
  asset?: MarketAsset
  updateAvailable?: boolean
}

// 商城安装预览（第一步）：返回需用户确认的权限，同意后带集合走安装。
export interface PluginPreview {
  id: string
  name: string
  version: string
  installedVersion: string
  isUpdate: boolean
  newPermissions: string[]
}

// 启动检查发现的可更新插件（plugin:updates-available 事件载荷，插件中心红点用）。
export interface PluginUpdateNotice {
  id: string
  name: string
  version: string
}
