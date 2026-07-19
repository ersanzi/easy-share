<script setup lang="ts">
import type { TransferTask } from '../types/core'

defineProps<{ tasks: TransferTask[] }>()
defineEmits<{ accept: [id: string]; acceptAs: [id: string]; reject: [id: string]; clear: []; delete: [id: string] }>()

const size = (value: number) => value < 1024
  ? `${value} B`
  : value < 1048576
    ? `${(value / 1024).toFixed(1)} KB`
    : value < 1073741824
      ? `${(value / 1048576).toFixed(1)} MB`
      : `${(value / 1073741824).toFixed(1)} GB`
const progress = (task: TransferTask) => task.totalBytes
  ? Math.min(100, Math.round(task.transferredBytes / task.totalBytes * 100))
  : 0
const statusLabel = (status: string) => ({
  pending: '等待确认', transferring: '传输中', completed: '已完成', failed: '失败', rejected: '已拒绝',
}[status] ?? status)
const isTerminal = (status: string) => ['completed', 'failed', 'rejected'].includes(status)
const duration = (task: TransferTask) => {
  if (!isTerminal(task.status) || !task.createdAt || !task.updatedAt) return ''
  const ms = new Date(task.updatedAt).getTime() - new Date(task.createdAt).getTime()
  if (ms < 1000) return '<1s'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  return `${m}m${s % 60}s`
}
</script>

<template>
  <section class="card transfer-panel">
    <header class="card-header">
      <div>
        <span class="section-label">活动</span>
        <h2>文件传输</h2>
      </div>
      <div class="header-actions">
        <button v-if="tasks.length" class="text-button" type="button" @click="$emit('clear')">清除记录</button>
        <span class="count-badge">{{ tasks.length }} 项</span>
      </div>
    </header>

    <div v-if="!tasks.length" class="empty-state compact-empty">
      <span class="empty-illustration file-stack">
        <svg viewBox="0 0 24 24"><path d="M7 3h7l4 4v14H7z"/><path d="M14 3v5h5M10 13h5M10 17h5"/></svg>
      </span>
      <strong>暂无传输任务</strong>
      <p>发送或接收的文件会显示在这里。</p>
    </div>

    <div v-else class="transfer-list">
      <article v-for="item in tasks" :key="item.id" class="transfer-row">
        <div class="file-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
          <span :class="item.direction"><svg viewBox="0 0 16 16"><path :d="item.direction === 'send' ? 'M8 12V3m0 0L5 6m3-3 3 3' : 'M8 3v9m0 0-3-3m3 3 3-3'"/></svg></span>
        </div>
        <div class="transfer-content">
          <div class="transfer-heading">
            <div><strong>{{ item.fileName }}</strong><span>{{ item.direction === 'send' ? '发送到' : '来自' }} {{ item.peer }} · {{ size(item.totalBytes) }}</span></div>
            <span :class="['task-status', item.status]">{{ statusLabel(item.status) }}</span>
          </div>
          <div class="progress-track" role="progressbar" :aria-valuenow="progress(item)" aria-valuemin="0" aria-valuemax="100">
            <i :style="{ width: `${progress(item)}%` }" />
          </div>
          <div class="transfer-meta">
            <span>{{ progress(item) }}%</span>
            <span v-if="item.speed" :class="{ 'speed-active': item.status === 'transferring' }">{{ size(item.speed) }}/s</span>
            <span v-if="duration(item)" class="duration-text">用时 {{ duration(item) }}</span>
            <span v-if="item.error" class="inline-error">{{ item.error }}</span>
            <div v-if="item.status === 'pending' && item.direction === 'receive'" class="row-actions">
              <button class="secondary-button compact" type="button" @click="$emit('reject', item.id)">拒绝</button>
              <button class="secondary-button compact" type="button" @click="$emit('acceptAs', item.id)">另存</button>
              <button class="primary-button compact" type="button" @click="$emit('accept', item.id)">接收</button>
            </div>
            <button v-if="isTerminal(item.status)" class="delete-btn" type="button" aria-label="删除记录" @click="$emit('delete', item.id)">×</button>
          </div>
        </div>
      </article>
    </div>
  </section>
</template>
