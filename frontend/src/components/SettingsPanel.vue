<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { core } from '../services/core'

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

onMounted(load)
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
