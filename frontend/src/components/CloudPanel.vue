<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import type { CloudFile, CloudPreview as CloudPreviewData } from '../types/core'
import CloudPreviewView from './CloudPreview.vue'
import { core } from '../services/core'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'

interface UploadProgress {
  name: string
  size: number
  sent: number
  speed: number
  eta: number
  error?: string
}

defineProps<{ enabled: boolean }>()
defineEmits<{ upload: []; uploadFolder: []; download: [key: string]; delete: [key: string]; share: [key: string] }>()

const files = ref<CloudFile[]>([])
const loading = ref(false)
const shareUrl = ref('')
const shareKey = ref('')
const uploads = ref<UploadProgress[]>([])
const previewOpen = ref(false)
const previewLoading = ref(false)
const previewError = ref('')
const previewData = ref<CloudPreviewData | null>(null)

const loadFiles = async () => {
  loading.value = true
  try {
    files.value = await core.cloudList()
  } catch {
    files.value = []
  } finally {
    loading.value = false
  }
}

const formatSize = (value: number) => value < 1024
  ? `${value} B`
  : value < 1048576
    ? `${(value / 1024).toFixed(1)} KB`
    : value < 1073741824
      ? `${(value / 1048576).toFixed(1)} MB`
      : `${(value / 1073741824).toFixed(1)} GB`

const formatSpeed = (value: number) => `${formatSize(value)}/s`

const formatETA = (seconds: number) => {
  if (seconds <= 0) return '即将完成'
  if (seconds < 60) return `约 ${Math.ceil(seconds)} 秒`
  const m = Math.floor(seconds / 60)
  const s = Math.ceil(seconds % 60)
  return `约 ${m} 分 ${s} 秒`
}

const formatDate = (value: string) => {
  if (!value) return ''
  const d = new Date(value)
  return `${d.getMonth() + 1}/${d.getDate()} ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
}

const percent = (u: UploadProgress) => u.size > 0 ? Math.min(100, Math.round(u.sent / u.size * 100)) : 0

const handleUploadEvent = (event: { name: string; size: number; sent: number; speed: number; eta: number; done: boolean; error?: string }) => {
  if (event.done && !event.error) {
    uploads.value = uploads.value.filter(u => u.name !== event.name)
    void loadFiles()
    return
  }
  const existing = uploads.value.find(u => u.name === event.name)
  if (existing) {
    existing.sent = event.sent
    existing.speed = event.speed
    existing.eta = event.eta
    existing.error = event.error
  } else {
    uploads.value.push({ name: event.name, size: event.size, sent: event.sent, speed: event.speed, eta: event.eta, error: event.error })
  }
}

const dismissUpload = (name: string) => {
  uploads.value = uploads.value.filter(u => u.name !== name)
}

const requestShare = async (key: string) => {
  try {
    shareUrl.value = await core.cloudShare(key, 24)
    shareKey.value = key
  } catch {
    shareUrl.value = ''
  }
}
const closePreview = () => {
  previewOpen.value = false
  previewLoading.value = false
  previewError.value = ''
  previewData.value = null
}

const openPreview = async (file: CloudFile) => {
  previewOpen.value = true
  previewLoading.value = true
  previewError.value = ''
  previewData.value = null
  try {
    const result = await core.cloudPreview(file.key)
    previewData.value = result
    if (result.kind === 'unsupported') {
      previewError.value = '此格式暂不支持在线预览，请下载后打开。'
    }
  } catch (error) {
    previewError.value = error instanceof Error ? error.message : '预览服务暂不可用，请稍后重试。'
  } finally {
    previewLoading.value = false
  }
}

const copyShare = () => {
  if (shareUrl.value) {
    navigator.clipboard.writeText(shareUrl.value)
    shareUrl.value = ''
    shareKey.value = ''
  }
}

onMounted(() => {
  loadFiles()
  EventsOn('cloud-upload-progress', handleUploadEvent)
})
onBeforeUnmount(() => {
  EventsOff('cloud-upload-progress')
})
defineExpose({ refresh: loadFiles })
</script>

<template>
  <section class="card cloud-panel">
    <header class="card-header">
      <div>
        <span class="section-label">云端</span>
        <h2>网盘</h2>
      </div>
      <div class="header-actions">
        <button class="text-button" type="button" @click="loadFiles">刷新</button>
        <span class="count-badge">{{ files.length }} 个文件</span>
      </div>
    </header>

    <div v-if="!enabled" class="empty-state">
      <span class="empty-illustration cloud-off">
        <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/><path d="M3 3l18 18"/></svg>
      </span>
      <strong>网盘未启用</strong>
      <p>云端服务暂不可用，请确认后台服务已正常启动。</p>
    </div>

    <template v-else>
      <div class="cloud-toolbar">
        <button class="primary-button compact" type="button" @click="$emit('upload')">
          <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
          上传文件
        </button>
        <button class="secondary-button compact" type="button" @click="$emit('uploadFolder')">
          <svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
          上传文件夹
        </button>
      </div>

      <!-- 上传进度区 -->
      <div v-if="uploads.length" class="upload-queue">
        <article v-for="u in uploads" :key="u.name" class="upload-row">
          <div class="upload-icon" :class="{ failed: !!u.error }" aria-hidden="true">
            <svg v-if="!u.error" viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
            <svg v-else viewBox="0 0 24 24"><path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/></svg>
          </div>
          <div class="upload-info">
            <div class="upload-heading">
              <strong>{{ u.name }}</strong>
              <span v-if="u.error" class="upload-error">上传失败</span>
              <span v-else class="upload-percent">{{ percent(u) }}%</span>
            </div>
            <div v-if="!u.error" class="upload-track"><i :style="{ width: percent(u) + '%' }" /></div>
            <div class="upload-meta">
              <span v-if="u.error">{{ u.error }}</span>
              <template v-else>
                <span>{{ formatSize(u.sent) }} / {{ formatSize(u.size) }}</span>
                <span v-if="u.speed > 0">{{ formatSpeed(u.speed) }}</span>
                <span v-if="u.eta > 0">{{ formatETA(u.eta) }}</span>
              </template>
            </div>
          </div>
          <button v-if="u.error" class="delete-btn" type="button" title="移除" @click="dismissUpload(u.name)">×</button>
        </article>
      </div>

      <div v-if="loading" class="empty-state compact-empty">
        <strong>加载中…</strong>
      </div>

      <div v-else-if="!files.length && !uploads.length" class="empty-state compact-empty">
        <span class="empty-illustration cloud-empty">
          <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/></svg>
        </span>
        <strong>网盘为空</strong>
        <p>点击上方"上传文件"将文件存储到云端。</p>
      </div>

      <div v-else-if="files.length" class="cloud-list">
        <article v-for="file in files" :key="file.key" class="cloud-row">
          <div class="cloud-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
          </div>
          <div class="cloud-info">
            <strong>{{ file.name }}</strong>
            <span>{{ formatSize(file.size) }} · {{ formatDate(file.lastModified) }}</span>
          </div>
          <div class="cloud-actions">
            <button v-if="file.previewKind !== 'unsupported'" class="secondary-button compact preview-action" type="button" title="预览" @click="openPreview(file)">预览</button>
            <button class="secondary-button compact" type="button" title="下载" @click="$emit('download', file.key)">下载</button>
            <button class="secondary-button compact" type="button" title="分享链接" @click="requestShare(file.key)">分享</button>
            <button class="secondary-button compact destructive-text" type="button" title="删除" @click="$emit('delete', file.key)">删除</button>
          </div>
        </article>
      </div>

      <div v-if="shareUrl" class="share-bar">
        <span class="share-label">{{ shareKey }} 分享链接（24h 有效）：</span>
        <code class="share-url">{{ shareUrl }}</code>
        <button class="primary-button compact" type="button" @click="copyShare">复制</button>
      </div>
    </template>
  </section>
  <CloudPreviewView v-if="previewOpen" :preview="previewData" :loading="previewLoading" :error="previewError" @close="closePreview" />
</template>
