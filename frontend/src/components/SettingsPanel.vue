<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { core } from '../services/core'
import type { CloudSettingsData } from '../services/core'

interface SettingsForm {
  deviceName: string
  receiveDir: string
  webdavRoot: string
  driveLetter: string
}

const form = ref<SettingsForm>({ deviceName: '', receiveDir: '', webdavRoot: '', driveLetter: 'Z:' })
const cloudForm = ref<CloudSettingsData>({ endpoint: '', region: '', accessKeyId: '', secretAccessKey: '', bucket: '', allowInsecureHttp: false })
const saving = ref(false)
const saved = ref(false)
const cloudSaved = ref(false)
const loadError = ref('')

const driveLetters = Array.from({ length: 23 }, (_, i) => String.fromCharCode(68 + i))

const load = async () => {
  try {
    const settings = await core.getSettings()
    form.value = { ...settings }
    const cloud = await core.getCloudSettings()
    cloudForm.value = { ...cloud }
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
    await core.saveSettings(form.value.deviceName, form.value.receiveDir, form.value.webdavRoot, form.value.driveLetter)
    saved.value = true
    setTimeout(() => { saved.value = false }, 2500)
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    saving.value = false
  }
}

const saveCloud = async () => {
  if (saving.value) return
  saving.value = true
  cloudSaved.value = false
  loadError.value = ''
  try {
    const c = cloudForm.value
    await core.saveCloudSettings(c.endpoint, c.region, c.accessKeyId, c.secretAccessKey, c.bucket, c.allowInsecureHttp)
    cloudSaved.value = true
    setTimeout(() => { cloudSaved.value = false }, 2500)
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
        <p>管理设备名称、文件目录与网络驱动器配置。</p>
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

      <!-- 网络驱动器 -->
      <div class="card settings-card">
        <div class="card-header">
          <h2>网络驱动器</h2>
        </div>
        <div class="settings-body">
          <div class="setting-row">
            <label class="setting-label">共享目录</label>
            <p class="setting-hint">通过 WebDAV 对外共享的根目录</p>
            <div class="setting-path">
              <span class="path-display">{{ form.webdavRoot || '未选择' }}</span>
              <button class="secondary-button compact" type="button" @click="pickShareDir">浏览…</button>
            </div>
          </div>
          <div class="setting-row">
            <label class="setting-label" for="driveLetter">映射盘符</label>
            <p class="setting-hint">共享目录在资源管理器中显示的盘符</p>
            <select id="driveLetter" v-model="form.driveLetter" class="setting-select">
              <option v-for="letter in driveLetters" :key="letter" :value="letter + ':'">{{ letter }}:</option>
            </select>
          </div>
        </div>
      </div>

      <!-- 网盘 (RustFS) -->
      <div class="card settings-card">
        <div class="card-header">
          <h2>网盘（RustFS）</h2>
        </div>
        <div class="settings-body">
          <div class="setting-row">
            <label class="setting-label" for="cloudEndpoint">服务端点</label>
            <p class="setting-hint">RustFS 服务地址，如 http://192.168.1.100:9000（留空则禁用网盘）</p>
            <input id="cloudEndpoint" v-model="cloudForm.endpoint" class="setting-input" type="text" placeholder="http://192.168.1.100:9000" />
          </div>
          <div class="setting-row">
            <label class="setting-label" for="cloudBucket">存储桶</label>
            <p class="setting-hint">文件存储的 Bucket 名称</p>
            <input id="cloudBucket" v-model="cloudForm.bucket" class="setting-input" type="text" placeholder="easyshare" />
          </div>
          <div class="setting-row">
            <label class="setting-label" for="cloudAccessKey">Access Key</label>
            <p class="setting-hint">RustFS 访问密钥 ID</p>
            <input id="cloudAccessKey" v-model="cloudForm.accessKeyId" class="setting-input" type="text" placeholder="Access Key ID" />
          </div>
          <div class="setting-row">
            <label class="setting-label" for="cloudSecretKey">Secret Key</label>
            <p class="setting-hint">RustFS 访问密钥</p>
            <input id="cloudSecretKey" v-model="cloudForm.secretAccessKey" class="setting-input" type="password" placeholder="Secret Access Key" />
          </div>
          <div class="setting-row">
            <label class="setting-label" for="cloudRegion">区域</label>
            <p class="setting-hint">可选，默认 us-east-1</p>
            <input id="cloudRegion" v-model="cloudForm.region" class="setting-input" type="text" placeholder="us-east-1" />
          </div>
          <div class="setting-row">
            <label class="setting-label">
              <input v-model="cloudForm.allowInsecureHttp" type="checkbox" style="margin-right:6px" />
              允许 HTTP（非加密）连接
            </label>
            <p class="setting-hint">仅在受信任的局域网环境中启用</p>
          </div>
        </div>
      </div>
    </div>

    <div class="settings-footer">
      <transition name="fade">
        <span v-if="saved" class="save-success">已保存</span>
      </transition>
      <transition name="fade">
        <span v-if="cloudSaved" class="save-success">网盘配置已保存</span>
      </transition>
      <button class="secondary-button" type="button" :disabled="saving" @click="saveCloud">
        保存网盘配置
      </button>
      <button class="primary-button" type="button" :disabled="saving" @click="save">
        {{ saving ? '保存中…' : '保存设置' }}
      </button>
    </div>
  </div>
</template>
