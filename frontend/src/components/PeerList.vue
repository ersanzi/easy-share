<script setup lang="ts">
import type { Peer } from '../types/core'

defineProps<{ peers: Peer[] }>()
defineEmits<{ send: [id: string] }>()
</script>

<template>
  <section class="card peer-panel">
    <header class="card-header">
      <div>
        <span class="section-label">附近</span>
        <h2>在线设备</h2>
      </div>
      <span class="count-badge">{{ peers.length }} 台</span>
    </header>

    <div v-if="!peers.length" class="empty-state">
      <span class="empty-illustration radar">
        <i /><i /><i />
        <svg viewBox="0 0 24 24"><rect x="5" y="5" width="14" height="11" rx="2"/><path d="M9 20h6M12 16v4"/></svg>
      </span>
      <strong>正在寻找附近设备</strong>
      <p>请确保其他设备已打开 EasyShare，并连接到同一局域网。</p>
    </div>

    <div v-else class="peer-list">
      <article v-for="peer in peers" :key="peer.deviceId" class="peer-row">
        <div class="device-avatar" aria-hidden="true">
          <svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="13" rx="2.5"/><path d="M8 21h8M12 17v4"/></svg>
          <span class="online-indicator" />
        </div>
        <div class="row-copy">
          <strong>{{ peer.deviceName }}</strong>
          <span>{{ peer.ip }}:{{ peer.transferPort }} · 在线</span>
        </div>
        <button class="primary-button compact" type="button" @click="$emit('send', peer.deviceId)">
          <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
          发送
        </button>
      </article>
    </div>
  </section>
</template>
