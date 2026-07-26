<script setup lang="ts">
import { computed } from 'vue'
import type { TransferTask } from '../types/core'
import { core } from '../services/core'
import {
  compareTasks,
  formatBytes,
  formatTaskDuration,
  taskActionLabel,
  taskAmountLabel,
  taskDirectionPath,
  taskIsActive,
  taskIsTerminal,
  taskKind,
  taskKindLabel,
  taskPriority,
  taskProgress,
  taskStatusLabel,
  taskTimestamp,
} from '../utils/tasks'

const props = defineProps<{ tasks: TransferTask[] }>()
defineEmits<{
  accept: [id: string]
  acceptAs: [id: string]
  reject: [id: string]
  clear: []
  delete: [id: string]
}>()

interface BatchGroup {
  type: 'batch'
  id: string
  batchId: string
  tasks: TransferTask[]
  totalBytes: number
  transferredBytes: number
  completedCount: number
  failedCount: number
  activeCount: number
  priority: number
  timestamp: number
}

interface SingleTask {
  type: 'task'
  id: string
  task: TransferTask
  priority: number
  timestamp: number
}

type DisplayEntry = BatchGroup | SingleTask

const displayEntries = computed<DisplayEntry[]>(() => {
  const grouped = new Map<string, TransferTask[]>()
  const singles: TransferTask[] = []

  for (const task of props.tasks) {
    if (!task.batchId) {
      singles.push(task)
      continue
    }
    const items = grouped.get(task.batchId) ?? []
    items.push(task)
    grouped.set(task.batchId, items)
  }

  const entries: DisplayEntry[] = []
  for (const [batchId, batchTasks] of grouped) {
    if (batchTasks.length < 2) {
      singles.push(...batchTasks)
      continue
    }
    const tasks = [...batchTasks].sort(compareTasks)
    entries.push({
      type: 'batch',
      id: `batch:${batchId}`,
      batchId,
      tasks,
      totalBytes: tasks.reduce((sum, task) => sum + task.totalBytes, 0),
      transferredBytes: tasks.reduce((sum, task) => sum + task.transferredBytes, 0),
      completedCount: tasks.filter(task => task.status === 'completed').length,
      failedCount: tasks.filter(task => taskIsTerminal(task.status) && task.status !== 'completed').length,
      activeCount: tasks.filter(task => taskIsActive(task.status)).length,
      priority: Math.min(...tasks.map(taskPriority)),
      timestamp: Math.max(...tasks.map(taskTimestamp)),
    })
  }

  for (const task of singles) {
    entries.push({
      type: 'task',
      id: `task:${task.id}`,
      task,
      priority: taskPriority(task),
      timestamp: taskTimestamp(task),
    })
  }

  return entries.sort((left, right) =>
    left.priority - right.priority ||
    right.timestamp - left.timestamp ||
    left.id.localeCompare(right.id))
})

const hasHistory = computed(() => props.tasks.some(task => taskIsTerminal(task.status)))

const batchProgress = (group: BatchGroup) => group.totalBytes
  ? Math.max(0, Math.min(100, Math.round(group.transferredBytes / group.totalBytes * 100)))
  : group.completedCount === group.tasks.length ? 100 : 0

const batchLabel = (group: BatchGroup) => {
  const total = group.tasks.length
  if (group.completedCount === total) return `已完成 ${total} 个文件`
  if (group.failedCount > 0 && group.activeCount === 0 && group.completedCount + group.failedCount === total) {
    return `${group.completedCount} 完成 · ${group.failedCount} 未完成`
  }
  return `${group.completedCount}/${total} 完成`
}

const openFile = (task: TransferTask) => {
  if (task.localPath) void core.openFile(task.localPath)
}

const openFolder = () => {
  void core.openReceiveFolder()
}

const canConfirmReceive = (task: TransferTask) =>
  task.status === 'pending' && taskKind(task) === 'lan_receive'
</script>

<template>
  <section class="card transfer-panel">
    <header class="card-header">
      <div>
        <span class="section-label">活动</span>
        <h2>全部任务</h2>
      </div>
      <div class="header-actions">
        <button class="text-button" type="button" @click="openFolder">打开接收文件夹</button>
        <button v-if="hasHistory" class="text-button" type="button" @click="$emit('clear')">清除历史</button>
        <span class="count-badge">{{ tasks.length }} 项</span>
      </div>
    </header>

    <div v-if="!tasks.length" class="empty-state compact-empty">
      <span class="empty-illustration file-stack">
        <svg viewBox="0 0 24 24"><path d="M7 3h7l4 4v14H7z"/><path d="M14 3v5h5M10 13h5M10 17h5"/></svg>
      </span>
      <strong>暂无任务</strong>
      <p>局域网传输与云端上传下载会统一显示在这里。</p>
    </div>

    <div v-else class="transfer-list">
      <template v-for="entry in displayEntries" :key="entry.id">
        <div v-if="entry.type === 'batch'" class="batch-group">
          <div class="batch-header">
            <div class="batch-info">
              <svg class="batch-icon" viewBox="0 0 24 24"><path d="M4 4h6l2 2h8v14H4z"/><path d="M4 10h16"/></svg>
              <div>
                <strong>{{ taskActionLabel(entry.tasks[0]) }}</strong>
                <span class="batch-summary">{{ batchLabel(entry) }} · {{ formatBytes(entry.totalBytes) }}</span>
              </div>
            </div>
            <span class="batch-percent">{{ batchProgress(entry) }}%</span>
          </div>
          <div class="batch-track" role="progressbar" :aria-valuenow="batchProgress(entry)" aria-valuemin="0" aria-valuemax="100">
            <i :style="{ width: `${batchProgress(entry)}%` }" />
          </div>
          <div class="batch-items">
            <article v-for="item in entry.tasks" :key="item.id" class="transfer-row compact" :data-task-id="item.id">
              <div class="file-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
                <span :class="['task-kind-marker', taskKind(item)]"><svg viewBox="0 0 16 16"><path :d="taskDirectionPath(item)"/></svg></span>
              </div>
              <div class="transfer-content">
                <div class="transfer-heading">
                  <div><strong>{{ item.fileName }}</strong><span>{{ taskAmountLabel(item) }}</span></div>
                  <span :class="['task-status', item.status]">{{ taskStatusLabel(item.status) }}</span>
                </div>
                <div class="transfer-meta">
                  <span>{{ taskProgress(item) }}%</span>
                  <span v-if="item.speed > 0 && item.status === 'running'" class="speed-active">{{ formatBytes(item.speed) }}/s</span>
                  <span v-if="formatTaskDuration(item)" class="duration-text">用时 {{ formatTaskDuration(item) }}</span>
                  <span v-if="item.error" class="inline-error">{{ item.error }}</span>
                  <div v-if="canConfirmReceive(item)" class="row-actions">
                    <button class="secondary-button compact" type="button" @click="$emit('reject', item.id)">拒绝</button>
                    <button class="secondary-button compact" type="button" @click="$emit('acceptAs', item.id)">另存</button>
                    <button class="primary-button compact" type="button" @click="$emit('accept', item.id)">接收</button>
                  </div>
                  <div v-if="item.status === 'completed' && item.localPath" class="row-actions">
                    <button class="secondary-button compact" type="button" @click="openFile(item)">打开</button>
                  </div>
                  <button v-if="taskIsTerminal(item.status)" class="delete-btn" type="button" aria-label="删除记录" @click="$emit('delete', item.id)">×</button>
                </div>
              </div>
            </article>
          </div>
        </div>

        <article v-else class="transfer-row" :data-task-id="entry.task.id">
          <div class="file-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
            <span :class="['task-kind-marker', taskKind(entry.task)]">
              <svg viewBox="0 0 16 16"><path :d="taskDirectionPath(entry.task)"/></svg>
            </span>
          </div>
          <div class="transfer-content">
            <div class="transfer-heading">
              <div>
                <strong>{{ entry.task.fileName }}</strong>
                <span>{{ taskActionLabel(entry.task) }} · {{ taskAmountLabel(entry.task) }}</span>
              </div>
              <span :class="['task-status', entry.task.status]">{{ taskStatusLabel(entry.task.status) }}</span>
            </div>
            <div class="progress-track" role="progressbar" :aria-valuenow="taskProgress(entry.task)" aria-valuemin="0" aria-valuemax="100">
              <i :style="{ width: `${taskProgress(entry.task)}%` }" />
            </div>
            <div class="transfer-meta">
              <span>{{ taskProgress(entry.task) }}%</span>
              <span v-if="entry.task.speed > 0 && entry.task.status === 'running'" class="speed-active">{{ formatBytes(entry.task.speed) }}/s</span>
              <span v-if="formatTaskDuration(entry.task)" class="duration-text">用时 {{ formatTaskDuration(entry.task) }}</span>
              <span v-if="entry.task.error" class="inline-error">{{ entry.task.error }}</span>
              <div v-if="canConfirmReceive(entry.task)" class="row-actions">
                <button class="secondary-button compact" type="button" @click="$emit('reject', entry.task.id)">拒绝</button>
                <button class="secondary-button compact" type="button" @click="$emit('acceptAs', entry.task.id)">另存</button>
                <button class="primary-button compact" type="button" @click="$emit('accept', entry.task.id)">接收</button>
              </div>
              <div v-if="entry.task.status === 'completed' && entry.task.localPath" class="row-actions">
                <button class="secondary-button compact" type="button" @click="openFile(entry.task)">打开文件</button>
              </div>
              <button v-if="taskIsTerminal(entry.task.status)" class="delete-btn" type="button" aria-label="删除记录" @click="$emit('delete', entry.task.id)">×</button>
            </div>
          </div>
        </article>
      </template>
    </div>
  </section>
</template>