<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import type { CloudPreview } from '../types/core'

defineProps<{
  preview: CloudPreview | null
  loading: boolean
  error: string
}>()
const emit = defineEmits<{ close: [] }>()

const close = () => emit('close')
const onKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') close()
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="preview-overlay" role="dialog" aria-modal="true" :aria-label="preview?.name || '文件预览'" @click.self="close">
    <article class="preview-dialog">
      <header class="preview-header">
        <div class="preview-title">
          <span class="preview-glyph" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z"/><circle cx="12" cy="12" r="2.5"/></svg>
          </span>
          <div>
            <h2>{{ preview?.name || '正在准备预览' }}</h2>
            <p v-if="preview">{{ preview.contentType }} · {{ preview.size.toLocaleString() }} B</p>
            <p v-else>EasyShare 安全预览</p>
          </div>
        </div>
        <button class="preview-close" type="button" aria-label="关闭预览" @click="close">×</button>
      </header>

      <div class="preview-body" :class="preview ? `preview-body--${preview.kind}` : ''">
        <div v-if="loading" class="preview-status">
          <span class="preview-spinner" aria-hidden="true" />
          <strong>正在加载预览…</strong>
        </div>
        <div v-else-if="error" class="preview-status preview-status--error">
          <strong>无法预览此文件</strong>
          <p>{{ error }}</p>
        </div>
        <template v-else-if="preview">
          <img v-if="preview.kind === 'image'" class="preview-image" :src="preview.contentUrl" :alt="preview.name" referrerpolicy="no-referrer">
          <iframe v-else-if="preview.kind === 'pdf'" class="preview-pdf" :src="preview.contentUrl" :title="`${preview.name} PDF 预览`" referrerpolicy="no-referrer" />
          <pre v-else-if="preview.kind === 'text'" class="preview-text">{{ preview.text }}</pre>
          <div v-else class="preview-status">
            <strong>此格式暂不支持在线预览</strong>
            <p>可下载到本机后使用系统应用打开。</p>
          </div>
        </template>
      </div>

      <footer v-if="preview?.truncated" class="preview-footer">
        仅显示前 1 MiB 内容，可下载查看完整文件。
      </footer>
    </article>
  </div>
</template>