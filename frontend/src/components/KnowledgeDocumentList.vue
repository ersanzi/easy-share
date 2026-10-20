<script setup lang="ts">
import { computed } from 'vue'
import type { KnowledgeDocumentSummary } from '../types/core'

const props = defineProps<{
  documents: KnowledgeDocumentSummary[]
  total: number
  query: string
  loading: boolean
  error: string
}>()
const emit = defineEmits<{
  'update:query': [value: string]
  refresh: []
  open: [doc: KnowledgeDocumentSummary]
  'clear-error': []
}>()

const hasQuery = computed(() => props.query.trim().length > 0)
const formatDate = (value?: string | null) => value ? value.replace('T', ' ').slice(0, 16) : ''

const documentMeta = (doc: KnowledgeDocumentSummary) => {
  const parts: string[] = []
  if (doc.chunks) parts.push(`${doc.chunks} 块`)
  const date = doc.processed_at || doc.updated_at
  if (date) parts.push(formatDate(date))
  if (doc.owner) parts.push(doc.owner)
  return parts.join(' · ')
}

const documentStatusLabel = (doc: KnowledgeDocumentSummary) => {
  if (doc.status === 'queued') return '排队中'
  if (doc.status === 'processing') return doc.progress > 0 ? `处理中 ${doc.progress}%` : '处理中'
  if (doc.status === 'failed') return doc.ready ? '更新失败' : '入库失败'
  return '已入库'
}

const documentStatusClass = (doc: KnowledgeDocumentSummary) => {
  if (doc.status === 'completed') return 'is-ready'
  if (doc.status === 'failed') return doc.ready ? 'is-warn' : 'is-failed'
  return 'is-busy'
}

const onQuery = (event: Event) => {
  emit('update:query', (event.target as HTMLInputElement).value)
}
</script>

<template>
  <div class="card knowledge-doc-card">
    <div class="knowledge-doc-toolbar">
      <div>
        <h2>已入库文档</h2>
        <p>共 {{ total }} 份；点一行打开原文。</p>
      </div>
      <div class="knowledge-doc-actions">
        <input
          :value="query"
          class="knowledge-doc-search"
          type="search"
          placeholder="搜索文件名"
          aria-label="搜索文档"
          @input="onQuery"
        />
        <button class="text-button" type="button" :disabled="loading" @click="emit('refresh')">
          {{ loading ? '正在刷新…' : '刷新' }}
        </button>
      </div>
    </div>

    <div v-if="error" class="alert" role="alert">
      <span class="alert-copy"><b>{{ error }}</b></span>
      <button type="button" aria-label="关闭" @click="emit('clear-error')">×</button>
    </div>

    <div v-if="loading && !documents.length" class="knowledge-loading">正在读取文档清单…</div>
    <div v-else-if="!documents.length" class="knowledge-doc-empty">
      <strong>{{ hasQuery ? '没有匹配的文档' : '知识库还没有文档' }}</strong>
      <p>{{ hasQuery ? '换个关键词试试。' : '管理员把文件放入知识库后，这里会出现。' }}</p>
    </div>
    <ul v-else class="knowledge-doc-list">
      <li v-for="doc in documents" :key="doc.file_id">
        <button
          class="knowledge-doc-row"
          type="button"
          :disabled="!doc.ready || !doc.read_version_id"
          :title="doc.ready ? '打开原文' : '文档还没有可读版本'"
          @click="emit('open', doc)"
        >
          <span class="knowledge-doc-main">
            <span class="knowledge-doc-title">{{ doc.filename }}</span>
            <span class="knowledge-doc-meta">{{ documentMeta(doc) }}</span>
            <span v-if="doc.status === 'failed' && doc.error_message" class="knowledge-doc-error">{{ doc.error_message }}</span>
            <span v-else-if="doc.status === 'failed' && doc.ready" class="knowledge-doc-error">最近一次更新失败，当前可读上一版。</span>
          </span>
          <span :class="['knowledge-doc-status', documentStatusClass(doc)]">{{ documentStatusLabel(doc) }}</span>
        </button>
      </li>
    </ul>
  </div>
</template>
