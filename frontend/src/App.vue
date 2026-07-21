<script setup lang="ts">
import { ref, watch } from 'vue'
import CloudPanel from './components/CloudPanel.vue'
import DevicePicker from './components/DevicePicker.vue'
import DrivePanel from './components/DrivePanel.vue'
import PeerList from './components/PeerList.vue'
import SettingsPanel from './components/SettingsPanel.vue'
import StatusBar from './components/StatusBar.vue'
import TransferList from './components/TransferList.vue'
import { useEasyShare } from './composables/useEasyShare'
import { WindowMinimise, Quit } from '../wailsjs/runtime/runtime'

type View = 'overview' | 'devices' | 'transfers' | 'cloud' | 'settings'

const app = useEasyShare()
const view = ref<View>('overview')
const cloudRef = ref<InstanceType<typeof CloudPanel> | null>(null)

// 同步当前页面到 composable，供拖拽路由判断（网盘页拖入直接上传）
watch(view, value => { app.activeView.value = value })

const handleCloudUpload = async () => {
  await app.cloudUpload()
}
const handleCloudUploadFolder = async () => {
  await app.cloudUploadFolder()
}
const handleCloudDownload = async (key: string) => {
  await app.cloudDownload(key)
}
const handleCloudDelete = async (key: string) => {
  await app.cloudDelete(key)
  cloudRef.value?.refresh()
}
</script>

<template>
  <div class="app-shell">
    <!-- 拖拽发送：设备选择浮层 -->
    <DevicePicker
      v-if="app.droppedFiles.value.length || app.droppedDirs.value.length"
      :files="app.droppedFiles.value"
      :dirs="app.droppedDirs.value"
      :peers="app.snapshot.value.peers"
      :sending="app.dropSending.value"
      @pick="app.sendDropped"
      @cancel="app.cancelDrop"
    />

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
          <button :class="['nav-item', view === 'devices' ? 'active' : '']" type="button" @click="view = 'devices'">
            <svg viewBox="0 0 24 24"><rect x="3" y="5" width="14" height="10" rx="2"/><path d="M8 19h4M10 15v4M19 9h2v10h-6v-2"/></svg>
            附近设备
            <span>{{ app.snapshot.value.peers.length }}</span>
          </button>
          <button :class="['nav-item', view === 'transfers' ? 'active' : '']" type="button" @click="view = 'transfers'">
            <svg viewBox="0 0 24 24"><path d="M7 3v13m0 0-4-4m4 4 4-4M17 21V8m0 0-4 4m4-4 4 4"/></svg>
            传输任务
            <span>{{ app.snapshot.value.tasks.length }}</span>
          </button>
          <button :class="['nav-item', view === 'cloud' ? 'active' : '']" type="button" @click="view = 'cloud'">
            <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/><path d="M12 12v5m0-5-2 2m2-2 2 2"/></svg>
            网盘
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
            :disabled="app.shuttingDown.value || app.stopped.value"
            @click="app.shutdown"
          >
            <svg viewBox="0 0 24 24"><path d="M12 3v9M7.2 5.8a8 8 0 1 0 9.6 0"/></svg>
            {{ app.shuttingDown.value ? '正在退出…' : '退出服务' }}
          </button>
        </div>
      </aside>

      <section class="workspace">
        <!-- ═══ 设置页 ═══ -->
        <SettingsPanel v-if="view === 'settings'" />

        <!-- ═══ 服务已退出 ═══ -->
        <div v-else-if="app.stopped.value" class="stopped-view" role="status">
          <div class="stopped-icon">
            <svg viewBox="0 0 24 24"><path d="M12 3v9M7.2 5.8a8 8 0 1 0 9.6 0"/></svg>
          </div>
          <h1>EasyShare 服务已安全退出</h1>
          <p>共享空间与网盘已停止访问，后台 Core 均已停止。现在可以关闭此窗口。</p>
          <span>运行日志保存在 {{ app.logDirectory.value }}</span>
        </div>

        <!-- ═══ 概览页 ═══ -->
        <template v-else-if="view === 'overview'">
          <header class="workspace-header">
            <div>
              <span class="section-label">共享中心</span>
              <h1>你好，欢迎回来</h1>
              <p>在附近设备间传送文件，共享空间与网盘可在“此电脑”中直接访问。</p>
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

          <!-- 摘要卡片：点击跳转详情 -->
          <div class="summary-cards">
            <button class="summary-card" type="button" @click="view = 'devices'">
              <div class="summary-icon devices-icon">
                <svg viewBox="0 0 24 24"><rect x="3" y="5" width="14" height="10" rx="2"/><path d="M8 19h4M10 15v4M19 9h2v10h-6v-2"/></svg>
              </div>
              <div class="summary-body">
                <strong>{{ app.snapshot.value.peers.length }} 台设备</strong>
                <span>附近在线设备</span>
              </div>
              <svg class="summary-arrow" viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></svg>
            </button>

            <button class="summary-card" type="button" @click="view = 'transfers'">
              <div class="summary-icon transfers-icon">
                <svg viewBox="0 0 24 24"><path d="M7 3v13m0 0-4-4m4 4 4-4M17 21V8m0 0-4 4m4-4 4 4"/></svg>
              </div>
              <div class="summary-body">
                <strong>{{ app.snapshot.value.tasks.length }} 个任务</strong>
                <span>传输任务记录</span>
              </div>
              <svg class="summary-arrow" viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></svg>
            </button>

            <button class="summary-card" type="button" @click="view = 'cloud'">
              <div class="summary-icon cloud-icon">
                <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/></svg>
              </div>
              <div class="summary-body">
                <strong>网盘</strong>
                <span>{{ app.snapshot.value.status.cloudEnabled ? '云端文件存储' : '未连接' }}</span>
              </div>
              <svg class="summary-arrow" viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></svg>
            </button>
          </div>

          <!-- 共享空间留在概览 -->
          <DrivePanel
            :status="app.snapshot.value.status"
            @start="app.startDrive"
            @stop="app.stopDrive"
          />
        </template>

        <!-- ═══ 附近设备详情页 ═══ -->
        <template v-else-if="view === 'devices'">
          <header class="workspace-header">
            <div>
              <span class="section-label">局域网</span>
              <h1>附近设备</h1>
              <p>发现同一局域网内运行 EasyShare 的设备，点击即可发送文件。</p>
            </div>
          </header>
          <PeerList :peers="app.snapshot.value.peers" @send="app.send" />
        </template>

        <!-- ═══ 传输任务详情页 ═══ -->
        <template v-else-if="view === 'transfers'">
          <header class="workspace-header">
            <div>
              <span class="section-label">传输</span>
              <h1>传输任务</h1>
              <p>查看发送与接收的文件传输进度和历史记录。</p>
            </div>
          </header>
          <TransferList :tasks="app.snapshot.value.tasks" @accept="app.accept" @accept-as="app.acceptAs" @reject="app.reject" @clear="app.clearHistory" @delete="app.deleteTask" />
        </template>

        <!-- ═══ 网盘详情页 ═══ -->
        <template v-else-if="view === 'cloud'">
          <header class="workspace-header">
            <div>
              <span class="section-label">云端</span>
              <h1>网盘</h1>
              <p>将文件安全存储到云端，随时随地下载和分享。</p>
            </div>
          </header>
          <CloudPanel
            ref="cloudRef"
            :enabled="app.snapshot.value.status.cloudEnabled"
            @upload="handleCloudUpload"
            @upload-folder="handleCloudUploadFolder"
            @download="handleCloudDownload"
            @delete="handleCloudDelete"
          />
        </template>
      </section>
    </div>
  </div>
</template>

