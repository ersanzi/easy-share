<script setup lang="ts">
import type { ServiceStatus } from '../types/core'

defineProps<{ status: ServiceStatus }>()
defineEmits<{
  start: []
  stop: []
}>()
</script>

<template>
  <section class="card drive-panel">
    <header class="card-header drive-header">
      <div>
        <span class="section-label">共享空间</span>
        <h2>EasyShare 共享</h2>
      </div>
      <span :class="['mapping-badge', status.webdav ? 'mapped' : '']">
        <i />{{ status.webdav ? '运行中' : '未启动' }}
      </span>
    </header>

    <div :class="['drive-visual', status.webdav ? 'active' : '']">
      <div class="drive-glyph">
        <svg viewBox="0 0 64 64"><path d="M10 20a4 4 0 0 1 4-4h12l5 6h23a4 4 0 0 1 4 4v20a4 4 0 0 1-4 4H14a4 4 0 0 1-4-4V20Z"/></svg>
      </div>
      <div class="drive-letter"><strong>共享</strong><span>EasyShare</span></div>
      <span class="drive-capacity">此电脑内直接访问</span>
    </div>

    <dl class="drive-details">
      <div><dt>访问方式</dt><dd>此电脑 → EasyShare 共享</dd></div>
      <div><dt>WebDAV</dt><dd :class="status.webdav ? 'success-text' : ''">{{ status.webdav ? '正在运行' : '未启动' }}</dd></div>
    </dl>

    <div class="drive-actions">
      <button v-if="!status.webdav" class="primary-button wide" type="button" @click="$emit('start')">
        <svg viewBox="0 0 24 24"><path d="M8 5.5v13l11-6.5-11-6.5Z"/></svg>
        启动共享
      </button>
      <button v-else class="secondary-button wide destructive-text" type="button" @click="$emit('stop')">
        <svg viewBox="0 0 24 24"><rect x="7" y="7" width="10" height="10" rx="1.5"/></svg>
        停止共享
      </button>
    </div>

    <p class="drive-note">
      <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 11v5m0-8h.01"/></svg>
      共享目录随 EasyShare 自动启动；打开“此电脑”双击“EasyShare 共享”即可浏览文件，无需映射盘符。
    </p>
  </section>
</template>
