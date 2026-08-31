<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { core } from '../services/core'
import type { UpdateCheckResult, UpdateProgress } from '../types/core'
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'

interface SettingsForm {
  deviceName: string
  receiveDir: string
  webdavRoot: string
}

const form = ref<SettingsForm>({ deviceName: '', receiveDir: '', webdavRoot: '' })
const saving = ref(false)
const saved = ref(false)
const loadError = ref('')

const load = async () => {
  try {
    const settings = await core.getSettings()
    form.value = { ...settings }
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  }
}

const pickReceiveDir = async () => {
  const path = await core.selectReceiveDirectory()
  if (path) form.value.receiveDir = path
}

const pickShareDir = async () => {
  const path = await core.selectShareDirectory()
  if (path) form.value.webdavRoot = path
}

const save = async () => {
  if (saving.value) return
  saving.value = true
  saved.value = false
  loadError.value = ''
  try {
    await core.saveSettings(form.value.deviceName, form.value.receiveDir, form.value.webdavRoot)
    saved.value = true
    setTimeout(() => { saved.value = false }, 2500)
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    saving.value = false
  }
}

// ─── 在线升级 ───
// 状态机：check →（有新版本）download（进度事件）→ downloaded → apply
const appVersion = ref('')
const checking = ref(false)
const checkResult = ref<UpdateCheckResult | null>(null)
const updateError = ref('')
const downloadState = ref<'idle' | 'downloading' | 'downloaded'>('idle')
const progress = ref<UpdateProgress | null>(null)
const applying = ref(false)
const applyError = ref('')

const onProgress = (payload: UpdateProgress) => { progress.value = payload }
const onDownloaded = () => { downloadState.value = 'downloaded' }
const onUpdateError = (payload: { message?: string }) => {
  downloadState.value = 'idle'
  updateError.value = payload?.message || '下载失败'
}

const check = async () => {
  if (checking.value) return
  checking.value = true
  updateError.value = ''
  applyError.value = ''
  checkResult.value = null
  downloadState.value = 'idle'
  progress.value = null
  try {
    checkResult.value = await core.checkUpdate()
  } catch (e) {
    updateError.value = e instanceof Error ? e.message : String(e)
  } finally {
    checking.value = false
  }
}

const download = async () => {
  if (downloadState.value === 'downloading') return
  downloadState.value = 'downloading'
  progress.value = null
  updateError.value = ''
  try {
    await core.startUpdateDownload()
  } catch (e) {
    downloadState.value = 'idle'
    updateError.value = e instanceof Error ? e.message : String(e)
  }
}

const apply = async () => {
  if (applying.value) return
  applying.value = true
  applyError.value = ''
  try {
    await core.applyUpdate()
  } catch (e) {
    applyError.value = e instanceof Error ? e.message : String(e)
  } finally {
    applying.value = false
  }
}

const openUpdatesFolder = () => { void core.openUpdatesFolder() }

const progressPercent = computed(() => {
  const p = progress.value
  if (!p || p.total <= 0) return null
  return Math.min(100, Math.round((p.received / p.total) * 100))
})

const formatBytes = (bytes?: number) => {
  if (!bytes || bytes <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${value.toFixed(value >= 100 || unit === 0 ? 0 : 1)} ${units[unit]}`
}

const formatSpeed = (bytesPerSecond?: number) => {
  if (!bytesPerSecond || bytesPerSecond <= 0) return ''
  return `${formatBytes(bytesPerSecond)}/s`
}

onMounted(async () => {
  load()
  try { appVersion.value = await core.appVersion() } catch { appVersion.value = '' }
  EventsOn('update:progress', onProgress)
  EventsOn('update:downloaded', onDownloaded)
  EventsOn('update:error', onUpdateError)
})
onBeforeUnmount(() => {
  EventsOff('update:progress')
  EventsOff('update:downloaded')
  EventsOff('update:error')
})
</script>

<template>
  <div class="settings-panel">
    <header class="workspace-header">
      <div>
        <span class="section-label">偏好</span>
        <h1>设置</h1>
        <p>管理设备名称、文件目录与共享空间配置。</p>
      </div>
    </header>

    <div v-if="loadError" class="alert" role="alert">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 9v4m0 4h.01M10 3.8 2.6 17a2 2 0 0 0 1.8 3h15.2a2 2 0 0 0 1.8-3L14 3.8a2.3 2.3 0 0 0-4 0Z"/></svg>
      <span class="alert-copy"><b>{{ loadError }}</b></span>
      <button type="button" aria-label="关闭" @click="loadError = ''">×</button>
    </div>

    <div class="settings-grid">
      <!-- 通用设置 -->
      <div class="card settings-card">
        <div class="card-header">
          <h2>通用</h2>
        </div>
        <div class="settings-body">
          <div class="setting-row">
            <label class="setting-label" for="deviceName">设备名称</label>
            <p class="setting-hint">局域网中其他设备看到的名称</p>
            <input
              id="deviceName"
              v-model="form.deviceName"
              class="setting-input"
              type="text"
              maxlength="32"
              placeholder="我的电脑"
            />
          </div>
          <div class="setting-row">
            <label class="setting-label">接收目录</label>
            <p class="setting-hint">他人发送的文件保存到此目录</p>
            <div class="setting-path">
              <span class="path-display">{{ form.receiveDir || '未选择' }}</span>
              <button class="secondary-button compact" type="button" @click="pickReceiveDir">浏览…</button>
            </div>
          </div>
        </div>
      </div>

      <!-- 共享空间 -->
      <div class="card settings-card">
        <div class="card-header">
          <h2>共享空间</h2>
        </div>
        <div class="settings-body">
          <div class="setting-row">
            <label class="setting-label">共享目录</label>
            <p class="setting-hint">在“此电脑”的 EasyShare 共享中显示的根目录</p>
            <div class="setting-path">
              <span class="path-display">{{ form.webdavRoot || '未选择' }}</span>
              <button class="secondary-button compact" type="button" @click="pickShareDir">浏览…</button>
            </div>
          </div>
        </div>
      </div>

      <!-- 关于与更新 -->
      <div class="card settings-card">
        <div class="card-header">
          <h2>关于与更新</h2>
        </div>
        <div class="settings-body">
          <div class="setting-row">
            <label class="setting-label">当前版本</label>
            <p class="setting-hint">EasyShare {{ appVersion || '…' }}</p>
            <div class="update-actions">
              <button class="secondary-button compact" type="button" :disabled="checking" @click="check">
                {{ checking ? '检查中…' : '检查更新' }}
              </button>
            </div>
          </div>

          <div v-if="updateError" class="setting-row">
            <p class="update-error">{{ updateError }}</p>
          </div>

          <div v-if="checkResult && !checkResult.hasUpdate" class="setting-row">
            <p class="setting-hint">已是最新版本（{{ checkResult.latestVersion }}）</p>
          </div>

          <template v-if="checkResult?.hasUpdate">
            <div class="setting-row">
              <label class="setting-label">新版本 {{ checkResult.latestVersion }}</label>
              <p class="setting-hint">
                发布于 {{ checkResult.publishedAt || '—' }}
                <template v-if="checkResult.asset"> · 安装包约 {{ formatBytes(checkResult.asset.size) }}</template>
              </p>
              <pre v-if="checkResult.notes" class="update-notes">{{ checkResult.notes }}</pre>
            </div>
            <div class="setting-row">
              <div v-if="downloadState === 'idle'" class="update-actions">
                <button class="primary-button compact" type="button" @click="download">下载更新</button>
              </div>
              <div v-else-if="downloadState === 'downloading'" class="update-progress">
                <div class="progress-track">
                  <div class="progress-fill" :style="{ width: `${progressPercent ?? 0}%` }"></div>
                </div>
                <span class="setting-hint">
                  {{ progressPercent !== null ? `${progressPercent}%` : '下载中…' }}{{ formatSpeed(progress?.speed) ? ` · ${formatSpeed(progress?.speed)}` : '' }}
                </span>
              </div>
              <div v-else class="update-actions">
                <button v-if="checkResult.canAutoInstall" class="primary-button compact" type="button" :disabled="applying" @click="apply">
                  {{ applying ? '正在重启…' : '重启并更新' }}
                </button>
                <button v-else class="primary-button compact" type="button" :disabled="applying" @click="apply">
                  前往下载
                </button>
                <button class="secondary-button compact" type="button" @click="openUpdatesFolder">打开下载目录</button>
              </div>
              <p v-if="downloadState === 'downloaded' && !checkResult.canAutoInstall" class="setting-hint">
                {{ checkResult.installedMode ? '当前版本不支持自动安装，可从下载目录手动运行安装包' : 'macOS / 绿色版请手动安装：下载目录中双击安装包，或从浏览器下载后替换' }}
              </p>
              <p v-if="applyError" class="update-error">{{ applyError }}</p>
            </div>
          </template>
        </div>
      </div>
    </div>

    <div class="settings-footer">
      <transition name="fade">
        <span v-if="saved" class="save-success">已保存</span>
      </transition>
      <button class="primary-button" type="button" :disabled="saving" @click="save">
        {{ saving ? '保存中…' : '保存设置' }}
      </button>
    </div>
  </div>
</template>
