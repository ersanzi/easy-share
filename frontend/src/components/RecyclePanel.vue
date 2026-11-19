<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import type { RecycleEntry } from '../types/core'
import { RECYCLE_RETENTION_DAYS } from '../types/core'
import { core } from '../services/core'
import ConfirmDialog from './ConfirmDialog.vue'

// 回收站视图（P1）：列表 / 还原 / 彻底删除。破坏性操作（彻底删除）有确认。

const emit = defineEmits<{ back: [] }>()

const entries = ref<RecycleEntry[]>([])
const loading = ref(false)
const error = ref('')
const pendingPurge = ref<RecycleEntry | null>(null)
let loadVersion = 0
let unmounted = false
let stopCloudChanged: (() => void) | undefined

const load = async () => {
  const version = ++loadVersion
  loading.value = true
  error.value = ''
  try {
    const list = await core.cloudRecycleList()
    if (!unmounted && version === loadVersion) entries.value = list
  } catch (e) {
    if (!unmounted && version === loadVersion) {
      error.value = e instanceof Error ? e.message : '回收站加载失败'
      entries.value = []
    }
  } finally {
    if (!unmounted && version === loadVersion) loading.value = false
  }
}

const fileName = (path: string) => path.replace(/\/+$/, '').split('/').pop() || path
const isDirectory = (entry: RecycleEntry) => entry.directory === true
  || (entry.directory === undefined && (entry.originPath || entry.path).endsWith('/'))

const entryType = (entry: RecycleEntry) => {
  if (isDirectory(entry)) return '文件夹'
  const name = fileName(entry.originPath || entry.path)
  const dot = name.lastIndexOf('.')
  const ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : ''
  if (/^(docx?|rtf|odt|pages)$/.test(ext)) return 'Word 文档'
  if (/^(xlsx?|csv|ods|numbers)$/.test(ext)) return 'Excel 表格'
  if (/^(pptx?|odp|key)$/.test(ext)) return '演示文稿'
  if (ext === 'pdf') return 'PDF 文档'
  if (/^(png|jpe?g|gif|webp|bmp|svg|ico|heic)$/.test(ext)) return '图片'
  if (/^(zip|rar|7z|tar|gz|bz2|xz)$/.test(ext)) return '压缩包'
  if (/^(mp3|wav|flac|aac|m4a|ogg)$/.test(ext)) return '音频'
  if (/^(mp4|mov|mkv|avi|webm|wmv)$/.test(ext)) return '视频'
  if (/^(md|txt|json|ya?ml|xml|html?|css|js|ts|jsx|tsx|go|py|java|sql|sh|log)$/.test(ext)) return '文本 / 代码'
  return '文件'
}

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

/**
 * 距离被彻底清除还剩几天。
 *
 * 控制面按 RECYCLE_RETENTION_DAYS 做清理，这里用同一个常量倒数——
 * 用户需要看到「我还有多久可以反悔」，而不是只知道「保留 30 天」这条规则。
 * 返回 null 表示没有删除时间（老数据），此时不显示倒计时而不是编一个天数。
 */
const remainDays = (deletedAt?: string) => {
  if (!deletedAt) return null
  const deleted = new Date(deletedAt).getTime()
  if (Number.isNaN(deleted)) return null
  const passed = Math.floor((Date.now() - deleted) / 86_400_000)
  return Math.max(0, RECYCLE_RETENTION_DAYS - passed)
}

// 一行的副标题：原路径 · 类型 · 文件大小 · 删除时间 · 倒计时。
// 倒计时集中在函数里拼，模板只管展示，避免同一函数在标签里被调用三次。
const rowMeta = (entry: RecycleEntry) => {
  const parts = [entry.originPath || entry.path, entryType(entry)]
  if (!isDirectory(entry)) parts.push(formatSize(entry.size))
  if (entry.deletedAt) parts.push(`删除于 ${formatDate(entry.deletedAt)}`)
  const days = remainDays(entry.deletedAt)
  if (days !== null) {
    // 最后三天换个说法：这时候「还能反悔」已经是个紧迫信息了
    parts.push(days <= 3 ? `仅剩 ${days} 天` : `还剩 ${days} 天`)
  }
  return parts.filter(Boolean).join(' · ')
}

const restore = async (entry: RecycleEntry) => {
  try {
    await core.cloudRecycleRestore(entry.fileId)
    // 根条目可能代表整个目录批次，必须重新核对服务端完整列表。
    await load()
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
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '彻底删除失败'
  }
}

onMounted(() => {
  stopCloudChanged = EventsOn('cloud:changed', () => { void load() })
  void load()
})
onUnmounted(() => {
  unmounted = true
  ++loadVersion
  // 只取消本组件的订阅，不移除其他入口的 cloud:changed 监听器。
  stopCloudChanged?.()
})
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
        <span class="count-badge">{{ entries.length }} 个项目</span>
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
      <p>删除的文件和文件夹会保留 {{ RECYCLE_RETENTION_DAYS }} 天，期间可随时还原。</p>
    </div>

    <div v-else class="cloud-list">
      <article v-for="entry in entries" :key="entry.fileId" class="cloud-row">
        <div class="cloud-icon" aria-hidden="true">
          <svg v-if="isDirectory(entry)" class="recycle-directory-icon" viewBox="0 0 24 24"><path d="M3 5h7l2 2h9v13H3z"/></svg>
          <svg v-else class="recycle-file-icon" viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
        </div>
        <div class="cloud-info">
          <strong>{{ fileName(entry.originPath || entry.path) }}</strong>
          <span>{{ rowMeta(entry) }}</span>
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
