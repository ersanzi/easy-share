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