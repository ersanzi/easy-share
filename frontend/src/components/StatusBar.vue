<script setup lang="ts">
import type { ServiceStatus } from '../types/core'

const props = defineProps<{ status: ServiceStatus; loading?: boolean }>()

const services: Array<{
  key: keyof ServiceStatus
  label: string
  detail: string
  icon: 'core' | 'discovery' | 'receiver' | 'drive'
}> = [
  { key: 'core', label: 'Core 服务', detail: '后台引擎', icon: 'core' },
  { key: 'discovery', label: '设备发现', detail: '局域网广播', icon: 'discovery' },
  { key: 'receiver', label: '文件接收', detail: '传输端口', icon: 'receiver' },
  { key: 'webdav', label: 'WebDAV', detail: '本地共享', icon: 'drive' },
]

const runningCount = () => services.filter(({ key }) => props.status[key]).length
</script>

<template>
  <section class="service-overview" aria-label="服务状态">
    <header class="overview-heading">
      <div>
        <h2>系统状态</h2>
        <p>{{ loading ? '正在读取服务状态…' : `${runningCount()} / ${services.length} 项服务正在运行` }}</p>
      </div>
      <span class="health-ring" :style="{ '--health': `${runningCount() / services.length * 360}deg` }">
        <b>{{ runningCount() }}</b>
      </span>
    </header>
    <div class="status-list">
      <div v-for="service in services" :key="service.key" class="status-item">
        <span :class="['status-icon', service.key]">
          <svg v-if="service.icon === 'core'" viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="4"/><path d="M9 9h6v6H9zM9 1v3m6-3v3M9 20v3m6-3v3M1 9h3m-3 6h3m16-6h3m-3 6h3"/></svg>
          <svg v-else-if="service.icon === 'discovery'" viewBox="0 0 24 24"><circle cx="12" cy="12" r="2"/><path d="M8.5 8.5a5 5 0 0 0 0 7M15.5 8.5a5 5 0 0 1 0 7M5.5 5.5a9 9 0 0 0 0 13M18.5 5.5a9 9 0 0 1 0 13"/></svg>
          <svg v-else-if="service.icon === 'receiver'" viewBox="0 0 24 24"><path d="M12 3v12m0 0-4-4m4 4 4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/></svg>
          <svg v-else viewBox="0 0 24 24"><path d="M4 7a3 3 0 0 1 3-3h10a3 3 0 0 1 3 3v10a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3Z"/><path d="M4 15h16M16 17.5h.01"/></svg>
        </span>
        <div><strong>{{ service.label }}</strong><small>{{ service.detail }}</small></div>
        <span :class="['state-text', status[service.key] ? 'online' : '']">
          {{ status[service.key] ? '运行中' : '未启动' }}
        </span>
      </div>
    </div>
  </section>
</template>
