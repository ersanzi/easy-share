<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, watch } from 'vue'
import type { KnowledgeDocumentBlock, KnowledgeDocumentView } from '../types/core'

const props = defineProps<{
  view: KnowledgeDocumentView | null
  loading: boolean
  error: string
  focusBlockIds?: string[]
  downloading?: boolean
  notice?: string
}>()
const emit = defineEmits<{ close: []; download: [] }>()

const focusSet = computed(() => new Set(props.focusBlockIds ?? []))
const outline = computed(() =>
  (props.view?.document.blocks ?? []).filter(block => block.type === 'heading' && block.text?.trim()))
const filename = computed(() => props.view?.manifest.filename || props.view?.document.filename || '文档')
const meta = computed(() => {
  const manifest = props.view?.manifest
  if (!manifest) return ''
  const parts: string[] = []
  if (manifest.blocks) parts.push(`${manifest.blocks} 个内容块`)
  if (manifest.source_bytes) parts.push(formatBytes(manifest.source_bytes))
  if (manifest.processed_at) parts.push(`入库于 ${formatDate(manifest.processed_at)}`)
  return parts.join(' · ')
})

const formatBytes = (value: number) => {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}
const formatDate = (value: string) => value.replace('T', ' ').slice(0, 16)
const blockAnchor = (id: string) => `knowledge-block-${id.replace(/[^a-zA-Z0-9_-]/g, '-')}`

const sourceLabel = (block: KnowledgeDocumentBlock) => {
  const source = block.source
  if (!source) return ''
  if (source.page) return `第 ${source.page} 页`
  if (source.sheet) return `工作表 ${source.sheet}`
  if (source.slide) return `第 ${source.slide} 页`
  if (source.paragraph) return `第 ${source.paragraph} 段`
  return ''
}

const scrollToBlock = (id: string) => {
  // 组件测试环境没有 scrollIntoView，产物里必须有；可选调用避免测试直接炸
  document.getElementById(blockAnchor(id))?.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
}
const scrollToFocus = async () => {
  await nextTick()
  const first = (props.focusBlockIds ?? [])[0]
  if (first) scrollToBlock(first)
}
watch(() => [props.view, props.focusBlockIds], scrollToFocus, { immediate: true })

const close = () => emit('close')
const onKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') close()
}
onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="reader-overlay" role="dialog" aria-modal="true" :aria-label="filename" @click.self="close">
    <article class="reader-dialog">
      <header class="reader-header">
        <div class="reader-title">
          <span class="reader-kicker">知识原文</span>
          <h2>{{ filename }}</h2>
          <p v-if="meta">{{ meta }}</p>
        </div>
        <div class="reader-actions">
          <button class="secondary-button compact" type="button" :disabled="!view || downloading" @click="emit('download')">
            {{ downloading ? '正在保存…' : '下载原件' }}
          </button>
          <button class="reader-close" type="button" aria-label="关闭" @click="close">×</button>
        </div>
      </header>

      <div :class="['reader-body', { 'reader-body--single': loading || error || !view || !outline.length }]">
        <div v-if="loading" class="reader-status">正在读取原文…</div>
        <div v-else-if="error" class="reader-status reader-status--error">{{ error }}</div>
        <template v-else-if="view">
          <nav v-if="outline.length" class="reader-outline" aria-label="文档目录">
            <span class="reader-outline-title">目录</span>
            <button v-for="heading in outline" :key="heading.id" type="button" @click="scrollToBlock(heading.id)">
              {{ heading.text }}
            </button>
          </nav>
          <div class="reader-content">
            <template v-for="block in view.document.blocks" :key="block.id">
              <h2
                v-if="block.type === 'heading'"
                :id="blockAnchor(block.id)"
                :class="['reader-block', 'reader-heading', `reader-level-${block.level ?? 2}`, { 'is-cited': focusSet.has(block.id) }]"
              >{{ block.text }}</h2>
              <div
                v-else-if="block.type === 'table'"
                :id="blockAnchor(block.id)"
                :class="['reader-block', 'reader-table-wrap', { 'is-cited': focusSet.has(block.id) }]"
              >
                <table class="reader-table">
                  <caption v-if="sourceLabel(block)" class="reader-table-caption">{{ sourceLabel(block) }}</caption>
                  <thead v-if="block.rows?.length">
                    <tr><th v-for="(cell, index) in block.rows[0]" :key="index">{{ cell }}</th></tr>
                  </thead>
                  <tbody>
                    <tr v-for="(row, rowIndex) in block.rows?.slice(1) ?? []" :key="rowIndex">
                      <td v-for="(cell, cellIndex) in row" :key="cellIndex">{{ cell }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p
                v-else-if="block.text"
                :id="blockAnchor(block.id)"
                :class="['reader-block', 'reader-paragraph', { 'is-cited': focusSet.has(block.id) }]"
              >
                <span v-if="sourceLabel(block)" class="reader-source">{{ sourceLabel(block) }}</span>{{ block.text }}
              </p>
            </template>
            <p v-if="!view.document.blocks?.length" class="reader-status">这份文档没有可展示的正文。</p>
          </div>
        </template>
      </div>

      <footer v-if="notice" class="reader-footer">{{ notice }}</footer>
    </article>
  </div>
</template>
