import type { TaskKind, TaskStatus, TransferTask } from '../types/core'

export type TaskSection = 'active' | 'attention' | 'recent'

const activeStatuses = new Set<TaskStatus>(['accepted', 'running', 'queued'])
const attentionStatuses = new Set<TaskStatus>(['pending', 'paused', 'waiting_network', 'failed'])
const terminalStatuses = new Set<TaskStatus>(['completed', 'rejected', 'failed', 'cancelled'])

// 旧任务没有 kind 时只能按方向推断为局域网任务；不猜测云端或知识任务。
export const taskKind = (task: TransferTask): TaskKind => {
  if (task.kind) return task.kind
  return task.direction === 'receive' ? 'lan_receive' : 'lan_send'
}

export const taskKindLabel = (task: TransferTask) => ({
  lan_send: '局域网发送',
  lan_receive: '局域网接收',
  cloud_upload: '云端上传',
  cloud_download: '云端下载',
  sync: '文件同步',
  parse: '文档处理',
}[taskKind(task)])

export const taskActionLabel = (task: TransferTask) => {
  const peer = task.peer?.trim() || '附近设备'
  switch (taskKind(task)) {
    case 'lan_send': return `发送到 ${peer}`
    case 'lan_receive': return `接收自 ${peer}`
    case 'cloud_upload': return '上传到网盘'
    case 'cloud_download': return '从网盘下载'
    case 'sync': return '同步文件'
    case 'parse': return '处理文档'
  }
}

export const taskStatusLabel = (status: TaskStatus | string) => ({
  pending: '等待确认',
  accepted: '准备中',
  running: '进行中',
  queued: '排队中',
  paused: '已暂停',
  waiting_network: '等待网络',
  completed: '已完成',
  rejected: '已拒绝',
  failed: '失败',
  cancelled: '已取消',
}[status] ?? status)

export const taskIsTerminal = (status: TaskStatus) => terminalStatuses.has(status)
export const taskIsActive = (status: TaskStatus) => activeStatuses.has(status)

export const taskSection = (task: TransferTask): TaskSection => {
  if (activeStatuses.has(task.status)) return 'active'
  if (attentionStatuses.has(task.status)) return 'attention'
  return 'recent'
}

export const taskPriority = (task: TransferTask) => ({ active: 0, attention: 1, recent: 2 }[taskSection(task)])

export const taskTimestamp = (task: TransferTask) => {
  const value = task.updatedAt || task.createdAt
  if (!value) return 0
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : 0
}

export const compareTasks = (left: TransferTask, right: TransferTask) => {
  const priority = taskPriority(left) - taskPriority(right)
  if (priority !== 0) return priority
  const timestamp = taskTimestamp(right) - taskTimestamp(left)
  if (timestamp !== 0) return timestamp
  return left.id.localeCompare(right.id)
}

export const taskProgress = (task: TransferTask) => {
  if (task.status === 'completed') return 100
  if (!Number.isFinite(task.totalBytes) || task.totalBytes <= 0) return 0
  const transferred = Number.isFinite(task.transferredBytes) ? task.transferredBytes : 0
  return Math.max(0, Math.min(100, Math.round(transferred / task.totalBytes * 100)))
}

export const formatBytes = (value: number) => {
  const bytes = Number.isFinite(value) ? Math.max(0, value) : 0
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1073741824) return `${(bytes / 1048576).toFixed(1)} MB`
  return `${(bytes / 1073741824).toFixed(1)} GB`
}

export const taskAmountLabel = (task: TransferTask) => {
  if (taskKind(task) === 'cloud_upload' && task.fileName.endsWith('/')) {
    return `${task.totalBytes} 个文件`
  }
  return formatBytes(task.totalBytes)
}

export const formatTaskDuration = (task: TransferTask) => {
  if (!taskIsTerminal(task.status) || !task.createdAt || !task.updatedAt) return ''
  const milliseconds = Date.parse(task.updatedAt) - Date.parse(task.createdAt)
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return ''
  if (milliseconds < 1000) return '<1s'
  const seconds = Math.floor(milliseconds / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return `${minutes}m${seconds % 60}s`
}

export const taskDirectionPath = (task: TransferTask) => {
  switch (taskKind(task)) {
    case 'lan_send':
    case 'cloud_upload':
      return 'M8 12V3m0 0L5 6m3-3 3 3'
    case 'lan_receive':
    case 'cloud_download':
      return 'M8 3v9m0 0-3-3m3 3 3-3'
    case 'sync':
      return 'M4 6h8m0 0-2-2m2 2-2 2M12 10H4m0 0 2-2m-2 2 2 2'
    case 'parse':
      return 'M5 3h6v10H5zM7 6h2M7 9h2'
  }
}