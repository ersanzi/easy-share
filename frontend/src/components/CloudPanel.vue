<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { CloudFile, CloudPreview as CloudPreviewData } from '../types/core'
import CloudPreviewView from './CloudPreview.vue'
import { buildDriveView, relativeDir } from '../utils/driveFolder'
import type { DriveSortDir, DriveSortKey } from '../utils/driveFolder'
import { core } from '../services/core'


defineProps<{ enabled: boolean }>()
defineEmits<{ upload: [targetDir: string]; uploadFolder: [targetDir: string]; download: [key: string]; delete: [key: string]; share: [key: string] }>()

const files = ref<CloudFile[]>([])
const loading = ref(false)
const shareUrl = ref('')
const shareKey = ref('')
const previewOpen = ref(false)
const previewLoading = ref(false)
const previewError = ref('')
const previewData = ref<CloudPreviewData | null>(null)

// 目录导航状态：currentPath 为相对根的目录（'' = 根）；搜索跨当前目录全部后代
const currentPath = ref('')
const searchQuery = ref('')
const sortKey = ref<DriveSortKey>('lastModified')
const sortDir = ref<DriveSortDir>('desc')

const view = computed(() =>
  buildDriveView(files.value, {
    path: currentPath.value,
    query: searchQuery.value,
    sortKey: sortKey.value,
    sortDir: sortDir.value,
  }),
)

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


const formatDate = (value: string) => {
  if (!value) return ''
  const d = new Date(value)
  return `${d.getMonth() + 1}/${d.getDate()} ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
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

const toggleSortDir = () => {
  sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
}
const onSortKeyChange = (event: Event) => {
  sortKey.value = (event.target as HTMLSelectElement).value as DriveSortKey
}
const onSearchInput = (event: Event) => {
  searchQuery.value = (event.target as HTMLInputElement).value
}
const enterFolder = (path: string) => {
  currentPath.value = path
  searchQuery.value = ''
}

onMounted(loadFiles)
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
        <span class="count-badge">{{ view.folderCount }} 个文件夹 · {{ view.fileCount }} 个文件</span>
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
        <button class="primary-button compact" type="button" @click="$emit('upload', currentPath)">
          <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
          上传文件
        </button>
        <button class="secondary-button compact" type="button" @click="$emit('uploadFolder', currentPath)">
          <svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
          上传文件夹
        </button>
        <div class="cloud-nav-tools">
          <div class="cloud-search">
            <svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
            <input
              :value="searchQuery"
              type="text"
              placeholder="在当前目录搜索（含子文件夹）"
              autocomplete="off"
              spellcheck="false"
              @input="onSearchInput"
            >
          </div>
          <select class="cloud-sort-key" :value="sortKey" title="排序字段" @change="onSortKeyChange">
            <option value="lastModified">按时间</option>
            <option value="name">按名称</option>
            <option value="size">按大小</option>
          </select>
          <button class="secondary-button compact" type="button" :title="sortDir === 'asc' ? '升序' : '降序'" @click="toggleSortDir">
            {{ sortDir === 'asc' ? '↑' : '↓' }}
          </button>
        </div>
      </div>

      <div class="cloud-crumbs">
        <template v-for="(crumb, index) in view.breadcrumbs" :key="crumb.path || '__root__'">
          <span v-if="index > 0" class="crumb-sep">/</span>
          <button
            class="crumb"
            type="button"
            :class="{ on: index === view.breadcrumbs.length - 1 }"
            @click="enterFolder(crumb.path)"
          >{{ crumb.name }}</button>
        </template>
        <span v-if="view.totalSize" class="crumb-size">{{ formatSize(view.totalSize) }}</span>
      </div>

      <div v-if="loading" class="empty-state compact-empty">
        <strong>加载中…</strong>
      </div>

      <div v-else-if="!view.folders.length && !view.files.length" class="empty-state compact-empty">
        <span class="empty-illustration cloud-empty">
          <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/></svg>
        </span>
        <strong>{{ view.searching ? '没有匹配的文件' : '此文件夹为空' }}</strong>
        <p>{{ view.searching ? '换个关键词，或回到上级目录再试。' : '点击上方"上传文件"将文件存储到云端。' }}</p>
      </div>

      <div v-else class="cloud-list">
        <article v-for="folder in view.folders" :key="folder.path" class="cloud-row folder-row" @click="enterFolder(folder.path)">
          <div class="cloud-icon folder-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
          </div>
          <div class="cloud-info">
            <strong>{{ folder.name }}</strong>
            <span>文件夹</span>
          </div>
          <div class="cloud-actions">
            <button class="secondary-button compact" type="button" title="打开" @click.stop="enterFolder(folder.path)">打开</button>
          </div>
        </article>

        <article v-for="file in view.files" :key="file.key" class="cloud-row">
          <div class="cloud-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
          </div>
          <div class="cloud-info">
            <strong>{{ file.name }}</strong>
            <span>{{ view.searching && relativeDir(file, currentPath) ? `${relativeDir(file, currentPath)}/ · ` : '' }}{{ formatSize(file.size) }} · {{ formatDate(file.lastModified) }}</span>
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
