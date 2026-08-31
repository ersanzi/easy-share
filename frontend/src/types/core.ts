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