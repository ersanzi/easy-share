<script setup lang="ts">
import { ref } from 'vue'
import DrivePanel from './components/DrivePanel.vue'
import PeerList from './components/PeerList.vue'
import SettingsPanel from './components/SettingsPanel.vue'
import StatusBar from './components/StatusBar.vue'
import TransferList from './components/TransferList.vue'
import { useEasyShare } from './composables/useEasyShare'
import { WindowMinimise, Quit } from '../wailsjs/runtime/runtime'

const app = useEasyShare()
const view = ref<'overview' | 'settings'>('overview')
</script>

<template>
  <div class="app-shell">
    <!-- 窗口控制条：拖拽区域 + 最小化/关闭 -->
    <div class="window-chrome">
      <div class="drag-region" />
      <div class="window-controls">
        <button class="win-btn" type="button" aria-label="最小化" @click="WindowMinimise()">
          <svg viewBox="0 0 24 24"><path d="M5 12h14"/></svg>
        </button>
        <button class="win-btn close" type="button" aria-label="关闭到托盘" @click="Quit()">
          <svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6L6 18"/></svg>
        </button>
      </div>
    </div>

    <div class="app-body">
      <aside class="sidebar">
        <div class="app-identity">
          <div class="app-icon" aria-hidden="true">
            <svg viewBox="0 0 32 32"><path d="M8.5 12a6.5 6.5 0 0 1 12.6-2.2A5.5 5.5 0 1 1 22.5 20H8a5 5 0 0 1 .5-8Z"/><path d="m12 15 4-4 4 4M16 11v11"/></svg>
          </div>
          <div><strong>EasyShare</strong><span>局域网文件空间</span></div>
        </div>

        <nav class="sidebar-nav" aria-label="主导航">
          <button :class="['nav-item', view === 'overview' ? 'active' : '']" type="button" @click="view = 'overview'">
            <svg viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7" rx="2"/><rect x="14" y="3" width="7" height="7" rx="2"/><rect x="3" y="14" width="7" height="7" rx="2"/><rect x="14" y="14" width="7" height="7" rx="2"/></svg>
            概览
          </button>
          <button :class="['nav-item', view === 'overview' ? 'active' : '']" type="button" @click="view = 'overview'">
            <svg viewBox="0 0 24 24"><rect x="3" y="5" width="14" height="10" rx="2"/><path d="M8 19h4M10 15v4M19 9h2v10h-6v-2"/></svg>
            附近设备
            <span>{{ app.snapshot.value.peers.length }}</span>
          </button>
          <button :class="['nav-item', view === 'overview' ? 'active' : '']" type="button" @click="view = 'overview'">
            <svg viewBox="0 0 24 24"><path d="M7 3v13m0 0-4-4m4 4 4-4M17 21V8m0 0-4 4m4-4 4 4"/></svg>
            传输任务
            <span>{{ app.snapshot.value.tasks.length }}</span>
          </button>
          <button :class="['nav-item', view === 'overview' ? 'active' : '']" type="button" @click="view = 'overview'">
            <svg viewBox="0 0 24 24"><path d="M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5v11a2.5 2.5 0 0 1-2.5 2.5h-11A2.5 2.5 0 0 1 4 17.5Z"/><path d="M4 15h16M16.5 17.5h.01"/></svg>
            网络驱动器
          </button>
          <button :class="['nav-item', view === 'settings' ? 'active' : '']" type="button" @click="view = 'settings'">
            <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>
            设置
          </button>
        </nav>

        <div class="sidebar-footer">
          <div class="connection-status">
            <span :class="['connection-dot', app.snapshot.value.status.core && !app.stopped.value ? 'online' : '']" />
            <div>
              <strong>{{ app.stopped.value ? '服务已退出' : app.snapshot.value.status.core ? '本机连接正常' : '正在连接 Core' }}</strong>
              <span>仅在本地网络中可见</span>
            </div>
          </div>
          <button
            class="shutdown-btn"
            type="button"
            :disabled="app.mapping.value || app.shuttingDown.value || app.stopped.value"
            @click="app.shutdown"
          >
            <svg viewBox="0 0 24 24"><path d="M12 3v9M7.2 5.8a8 8 0 1 0 9.6 0"/></svg>
            {{ app.shuttingDown.value ? '正在退出…' : '退出服务' }}
          </button>
        </div>
      </aside>

      <section class="workspace">
        <SettingsPanel v-if="view === 'settings'" />

        <template v-else>
          <div v-if="app.stopped.value" class="stopped-view" role="status">
            <div class="stopped-icon">
              <svg viewBox="0 0 24 24"><path d="M12 3v9M7.2 5.8a8 8 0 1 0 9.6 0"/></svg>
            </div>
            <h1>EasyShare 服务已安全退出</h1>
            <p>网络驱动器已取消映射，WebDAV 与后台 Core 均已停止。现在可以关闭此窗口。</p>
            <span>运行日志保存在 {{ app.logDirectory.value }}</span>
          </div>

          <template v-else>
            <header class="workspace-header">
              <div>
                <span class="section-label">共享中心</span>
                <h1>你好，欢迎回来</h1>
                <p>在附近设备间传送文件，或把共享空间挂载到资源管理器。</p>
              </div>
              <div :class="['availability', app.snapshot.value.status.core ? 'online' : '']">
                <span />
                {{ app.snapshot.value.status.core ? '可被发现' : '正在启动' }}
              </div>
            </header>

            <div v-if="app.error.value" class="alert" role="alert">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 9v4m0 4h.01M10 3.8 2.6 17a2 2 0 0 0 1.8 3h15.2a2 2 0 0 0 1.8-3L14 3.8a2.3 2.3 0 0 0-4 0Z"/></svg>
              <span class="alert-copy">
                <b>{{ app.error.value }}</b>
                <small>详细运行日志：{{ app.logDirectory.value }}</small>
              </span>
              <button type="button" aria-label="关闭错误提示" @click="app.clearError">×</button>
            </div>

            <StatusBar :status="app.snapshot.value.status" :loading="app.loading.value" />

            <div class="dashboard-grid">
              <div class="main-column">
                <PeerList id="devices" :peers="app.snapshot.value.peers" @send="app.send" />
                <TransferList id="transfers" :tasks="app.snapshot.value.tasks" @accept="app.accept" @accept-as="app.acceptAs" @reject="app.reject" @clear="app.clearHistory" @delete="app.deleteTask" />
              </div>
              <DrivePanel
                id="drive"
                :status="app.snapshot.value.status"
                :mapping="app.mapping.value"
                @start="app.startDrive"
                @stop="app.stopDrive"
                @map="app.mapDrive"
                @unmap="app.unmapDrive"
              />
            </div>
          </template>
        </template>
      </section>
    </div>
  </div>
</template>

