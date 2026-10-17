<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { RecycleEntry } from '../types/core'
import { core } from '../services/core'
import ConfirmDialog from './ConfirmDialog.vue'

// 回收站视图（P1）：列表 / 还原 / 彻底删除。破坏性操作（彻底删除）有确认。

const emit = defineEmits<{ back: [] }>()

const entries = ref<RecycleEntry[]>([])
const loading = ref(false)
const error = ref('')
const pendingPurge = ref<RecycleEntry | null>(null)

const load = async () => {
  loading.value = true
  error.value = ''
  try {
    entries.value = await core.cloudRecycleList()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '回收站加载失败'
    entries.value = []
  } finally {
    loading.value = false
  }
}

const fileName = (path: string) => path.split('/').pop() ?? path

const formatSize = (value: number) => value < 1024
  ? `${value} B`
  : value < 1048576
    ? `${(value / 1024).toFixed(1)} KB`
    : value < 1073741824
      ? `${(value / 1048576).toFixed(1)} MB`
      : `${(value / 1073741824).toFixed(1)} GB`

const formatDate = (value?: string) => {
  if (!value) return ''
  const d = new Date(value)
  return `${d.getMonth() + 1}/${d.getDate()} ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
}

const restore = async (entry: RecycleEntry) => {
  try {
    await core.cloudRecycleRestore(entry.fileId)
    entries.value = entries.value.filter(item => item.fileId !== entry.fileId)
  } catch (e) {
    // 还原冲突（原位置有同名文件）等错误直接透出控制面的可读说明
    error.value = e instanceof Error ? e.message : '还原失败'
  }
}

const askPurge = (entry: RecycleEntry) => {
  pendingPurge.value = entry
}

const confirmPurge = async () => {
  const entry = pendingPurge.value
  pendingPurge.value = null
  if (!entry) return
  try {
    await core.cloudRecyclePurge(entry.fileId)
    entries.value = entries.value.filter(item => item.fileId !== entry.fileId)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '彻底删除失败'
  }
}

onMounted(load)
</script>

<template>
  <section class="card cloud-panel recycle-panel">
    <header class="card-header">
      <div>
        <span class="section-label">云端</span>
        <h2>回收站</h2>
      </div>
      <div class="header-actions">
        <button class="text-button" type="button" @click="load">刷新</button>
        <button class="text-button" type="button" @click="emit('back')">返回网盘</button>
        <span class="count-badge">{{ entries.length }} 个文件</span>
      </div>
    </header>

    <div v-if="loading" class="empty-state compact-empty">
      <strong>加载中…</strong>
    </div>

    <div v-else-if="!entries.length" class="empty-state compact-empty">
      <span class="empty-illustration cloud-empty">
        <svg viewBox="0 0 24 24"><path d="M3 6h18M8 6V4h8v2m-9 0 1 14h8l1-14"/></svg>
      </span>
      <strong>回收站为空</strong>
      <p>删除的文件会保留在这里，可随时还原。</p>
    </div>

    <div v-else class="cloud-list">
      <article v-for="entry in entries" :key="entry.fileId" class="cloud-row">
        <div class="cloud-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
        </div>
        <div class="cloud-info">
          <strong>{{ fileName(entry.originPath || entry.path) }}</strong>
          <span>{{ entry.originPath }} · {{ formatSize(entry.size) }} · 删除于 {{ formatDate(entry.deletedAt) }}</span>
        </div>
        <div class="cloud-actions">
          <button class="secondary-button compact" type="button" title="还原到原位置" @click="restore(entry)">还原</button>
          <button class="secondary-button compact destructive-text" type="button" title="彻底删除，不可还原" @click="askPurge(entry)">彻底删除</button>
        </div>
      </article>
    </div>

    <div v-if="error" class="share-bar">
      <span class="share-label">{{ error }}</span>
      <button class="primary-button compact" type="button" @click="error = ''">知道了</button>
    </div>
  </section>

  <ConfirmDialog
    :open="pendingPurge !== null"
    title="彻底删除"
    :message="`彻底删除「${pendingPurge ? fileName(pendingPurge.originPath || pendingPurge.path) : ''}」？该操作不可还原。`"
    confirm-text="彻底删除"
    danger
    @confirm="confirmPurge"
    @update:open="value => { if (!value) pendingPurge = null }"
  />
</template>
