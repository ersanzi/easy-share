<script setup lang="ts">
import type { ServiceStatus } from '../types/core'

defineProps<{ status: ServiceStatus; mapping: boolean }>()
defineEmits<{
  start: []
  stop: []
  map: []
  unmap: []
}>()
</script>

<template>
  <section class="card drive-panel">
    <header class="card-header drive-header">
      <div>
        <span class="section-label">共享空间</span>
        <h2>网络驱动器</h2>
      </div>
      <span :class="['mapping-badge', status.driveMapped ? 'mapped' : '']">
        <i />{{ mapping ? '连接中' : status.driveMapped ? '已连接' : '未连接' }}
      </span>
    </header>

    <div :class="['drive-visual', status.driveMapped ? 'active' : '']">
      <div class="drive-glyph">
        <svg viewBox="0 0 64 64"><rect x="7" y="13" width="50" height="38" rx="10"/><path d="M7 39h50"/><circle cx="48" cy="45" r="2"/><path d="M17 25h22"/></svg>
      </div>
      <div class="drive-letter"><strong>Z:</strong><span>EasyShare</span></div>
      <span class="drive-capacity">本地 WebDAV</span>
    </div>

    <dl class="drive-details">
      <div><dt>服务地址</dt><dd>127.0.0.1:19080</dd></div>
      <div><dt>WebDAV</dt><dd :class="status.webdav ? 'success-text' : ''">{{ status.webdav ? '正在运行' : '未启动' }}</dd></div>
      <div><dt>资源管理器</dt><dd>{{ mapping ? '正在连接…' : status.driveMapped ? 'Z: 已连接' : '尚未连接' }}</dd></div>
    </dl>

    <div class="drive-actions">
      <button v-if="!status.driveMapped" class="primary-button wide" type="button" :disabled="mapping" @click="$emit('map')">
        <svg viewBox="0 0 24 24"><path d="M12 4v11m0 0-4-4m4 4 4-4M5 19h14"/></svg>
        {{ mapping ? '正在连接到资源管理器…' : '重新连接' }}
      </button>
      <button v-else class="secondary-button wide destructive-text" type="button" :disabled="mapping" @click="$emit('unmap')">
        <svg viewBox="0 0 24 24"><path d="M5 12h14M8 8l-4 4 4 4"/></svg>
        取消 Z: 盘映射
      </button>
      <button v-if="!status.webdav" class="text-button" type="button" :disabled="mapping" @click="$emit('start')">仅启动 WebDAV</button>
      <button v-else class="text-button" type="button" :disabled="mapping || status.driveMapped" :title="status.driveMapped ? '请先取消驱动器映射' : ''" @click="$emit('stop')">停止 WebDAV</button>
    </div>

    <p class="drive-note">
      <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 11v5m0-8h.01"/></svg>
      启动 EasyShare 时会自动连接；连接后可在“此电脑”中双击 Z: 进入。
    </p>
  </section>
</template>
