<script setup lang="ts">
import type { Peer } from '../types/core'

defineProps<{
  files: string[]
  dirs: string[]
  peers: Peer[]
  sending: boolean
}>()
defineEmits<{ pick: [id: string]; cancel: [] }>()

const fileName = (path: string) => path.replace(/\\/g, '/').split('/').pop() ?? path
</script>

<template>
  <div class="drop-overlay" role="dialog" aria-modal="true" aria-label="选择接收设备">
    <div class="drop-backdrop" @click="$emit('cancel')" />

    <section class="drop-card">
      <header class="drop-head">
        <div class="drop-title">
          <span class="drop-glyph" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
          </span>
          <div>
            <h2>发送到设备</h2>
            <p>已选择 {{ files.length + dirs.length }} 个项目，挑选一台在线设备开始传输。</p>
          </div>
        </div>
        <button class="drop-close" type="button" aria-label="取消" :disabled="sending" @click="$emit('cancel')">×</button>
      </header>

      <div class="drop-files">
        <article v-for="path in dirs" :key="path" class="drop-file-row">
          <span class="drop-file-icon drop-file-icon--folder" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
          </span>
          <span class="drop-file-name">{{ fileName(path) }}</span>
        </article>
        <article v-for="path in files" :key="path" class="drop-file-row">
          <span class="drop-file-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
          </span>
          <span class="drop-file-name">{{ fileName(path) }}</span>
        </article>
      </div>

      <div v-if="!peers.length" class="drop-empty">
        <span class="empty-illustration radar">
          <i /><i /><i />
          <svg viewBox="0 0 24 24"><rect x="5" y="5" width="14" height="11" rx="2"/><path d="M9 20h6M12 16v4"/></svg>
        </span>
        <strong>暂无在线设备</strong>
        <p>请确保对方已打开 EasyShare 并连接到同一局域网。</p>
      </div>

      <div v-else class="drop-peers">
        <button
          v-for="peer in peers"
          :key="peer.deviceId"
          class="drop-peer-row"
          type="button"
          :disabled="sending"
          @click="$emit('pick', peer.deviceId)"
        >
          <span class="device-avatar" aria-hidden="true">
            <svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="13" rx="2.5"/><path d="M8 21h8M12 17v4"/></svg>
            <span class="online-indicator" />
          </span>
          <span class="row-copy">
            <strong>{{ peer.deviceName }}</strong>
            <span>{{ peer.ip }}:{{ peer.transferPort }} · 在线</span>
          </span>
          <span class="drop-pick-hint">
            <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
            发送
          </span>
        </button>
      </div>

      <footer v-if="sending" class="drop-sending">
        <span class="drop-spinner" aria-hidden="true" />
        正在发送…
      </footer>
    </section>
  </div>
</template>
