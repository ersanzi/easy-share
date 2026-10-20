<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import ActivityDrawer from './components/ActivityDrawer.vue'
import AdminPanel from './components/AdminPanel.vue'
import CloudPanel from './components/CloudPanel.vue'
import ConfirmDialog from './components/ConfirmDialog.vue'
import DevicePicker from './components/DevicePicker.vue'
import KnowledgePanel from './components/KnowledgePanel.vue'
import LoginView from './components/LoginView.vue'
import MeetingsPanel from './components/MeetingsPanel.vue'
import PeerList from './components/PeerList.vue'
import PluginHost from './components/PluginHost.vue'
import PluginMarket from './components/PluginMarket.vue'
import SettingsPanel from './components/SettingsPanel.vue'
import TransferList from './components/TransferList.vue'
import { RECYCLE_RETENTION_DAYS, type Peer, type TransferTask } from './types/core'
import { useEasyShare } from './composables/useEasyShare'
import { useConfirm } from './composables/useConfirm'
import { usePlugins } from './composables/usePlugins'
import {
  compareTasks,
  formatBytes,
  taskActionLabel,
  taskAmountLabel,
  taskKind,
  taskProgress,
  taskSection,
  taskStatusLabel,
  taskTimestamp,
} from './utils/tasks'
import { WindowMinimise, Quit, WindowToggleMaximise, WindowIsMaximised } from '../wailsjs/runtime/runtime'

// 内置视图 + 动态插件视图（plugin:{id}）
type BuiltinView = 'overview' | 'devices' | 'transfers' | 'cloud' | 'knowledge' | 'meetings' | 'admin' | 'plugins' | 'settings'
type View = BuiltinView | `plugin:${string}`

// ─── 三段式信息架构（对标 WPS / 钉钉：图标轨切产品域，上下文面板列域内入口）───
// 单一真相源仍是 `view`：rail 与面板都从它派生，点 rail 只是把 view 切到该域默认页。
// 这样各页面组件的路由契约一字不改，改的只有外壳与导航分组。
type SectionId = 'home' | 'drive' | 'knowledge' | 'apps' | 'settings'

const sectionOf = (v: View): SectionId => {
  if (v === 'cloud') return 'drive'
  if (v === 'knowledge' || v === 'meetings') return 'knowledge'
  if (v === 'plugins' || v.startsWith('plugin:')) return 'apps'
  if (v === 'settings') return 'settings'
  return 'home'
}
const sectionMeta: Record<SectionId, { label: string; title: string; subtitle: string; home: View }> = {
  home: { label: '总览', title: '总览', subtitle: '本机服务、局域网与任务', home: 'overview' },
  drive: { label: '网盘', title: '我的网盘', subtitle: '云端文件与本机挂载', home: 'cloud' },
  knowledge: { label: '知识', title: '知识与会议', subtitle: '问答、听记与纪要', home: 'knowledge' },
  apps: { label: '扩展', title: '插件中心', subtitle: '按需安装，随程序提供', home: 'plugins' },
  settings: { label: '设置', title: '设置', subtitle: '账号、服务与更新', home: 'settings' },
}
const railSection = computed(() => sectionOf(view.value))
const openSection = (id: SectionId) => { view.value = sectionMeta[id].home }

// 浏览器预览（docs/design/*/preview）用 ?scene= 指定初始视图；桌面端没有这个参数，恒为 overview。
// 只影响首屏落在哪一页，不绕过任何登录与数据流程。
const initialView = (() => {
  if (typeof window === 'undefined') return 'overview'
  const scene = new URLSearchParams(window.location.search).get('scene') ?? ''
  const views: View[] = ['overview', 'devices', 'transfers', 'cloud', 'knowledge', 'meetings', 'admin', 'plugins', 'settings']
  return (views.includes(scene as View) ? scene : 'overview') as View
})()

const app = useEasyShare()
const { confirmState, confirm, settle } = useConfirm()
const pluginSys = usePlugins()
const view = ref<View>(initialView)
const cloudRef = ref<InstanceType<typeof CloudPanel> | null>(null)
const activityOpen = ref(false)
const activityTriggerRef = ref<HTMLButtonElement | null>(null)
const closeActivityDrawer = () => {
  activityOpen.value = false
  activityTriggerRef.value?.focus()
}
// 回收站原本是网盘页里的一个按钮，藏在工具条后面；提到「网盘」域面板当一等入口。
// 组件与状态都没变，只是变得看得见、点得到。
const openRecycleBin = async () => {
  view.value = 'cloud'
  await nextTick()
  cloudRef.value?.openRecycle()
}
const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)

// 最大化/还原：按钮点击与拖拽区双击共用；切换后重新查询同步图标
const isMaximised = ref(false)
const syncMaximised = async () => { isMaximised.value = await WindowIsMaximised() }
const toggleMaximise = async () => {
  await WindowToggleMaximise()
  await syncMaximised()
}
onMounted(() => { void syncMaximised() })

const activeTaskCount = computed(() => app.snapshot.value.tasks
  .filter(task => taskSection(task) === 'active').length)
const failedTaskCount = computed(() => app.snapshot.value.tasks
  .filter(task => task.status === 'failed').length)
const activityLabel = computed(() => {
  if (!activeTaskCount.value && !failedTaskCount.value) return '活动：暂无进行中或失败任务'
  return `活动：${activeTaskCount.value} 个进行中，${failedTaskCount.value} 个失败`
})
const completedCloudUploads = computed(() => app.snapshot.value.tasks
  .filter(task => taskKind(task) === 'cloud_upload' && task.status === 'completed')
  .map(task => `${task.id}:${task.updatedAt ?? ''}`)
  .sort()
  .join('|'))

// ─── 工作台「设备中继台」（方向 A）───
// 首屏只回答两个问题：现在谁在线、正在交换什么。任务排序沿用任务中心的优先级，
// 因此这里看到的顺序和点进任务中心后一致，不会出现两套说法。
const relayPeers = computed(() => app.snapshot.value.peers.slice(0, 4))
const relayTasks = computed(() => app.snapshot.value.tasks
  .filter(task => taskSection(task) !== 'recent')
  .sort(compareTasks)
  .slice(0, 4))
const pendingTaskCount = computed(() => app.snapshot.value.tasks
  .filter(task => task.status === 'pending').length)
const relayCompleted = computed(() => app.snapshot.value.tasks
  .filter(task => task.status === 'completed')
  .sort((left, right) => taskTimestamp(right) - taskTimestamp(left))
  .slice(0, 3))
const relayServices = computed(() => {
  const status = app.snapshot.value.status
  return [
    { label: '设备发现', on: status.discovery },
    { label: '文件接收', on: status.receiver },
    { label: '网盘', on: status.cloudEnabled },
    { label: '共享', on: status.webdav },
  ]
})
const peerState = (peer: Peer) => {
  const busy = app.snapshot.value.tasks.find(task =>
    task.peer === peer.deviceName && ['running', 'accepted', 'queued'].includes(task.status))
  if (busy) return { label: busy.direction === 'receive' ? '正在接收' : '正在发送', tone: 'busy' }
  const failed = app.snapshot.value.tasks.find(task =>
    task.peer === peer.deviceName && task.status === 'failed')
  if (failed) return { label: '上次传输失败', tone: 'warn' }
  return { label: '在线', tone: 'online' }
}
const relayTaskTone = (task: TransferTask) => taskKind(task).startsWith('cloud') ? 'cloud' : 'lan'
const relayTaskMeta = (task: TransferTask) => {
  const parts = [taskActionLabel(task)]
  if (task.status === 'running' && task.speed > 0) parts.push(`${formatBytes(task.speed)}/s`)
  parts.push(taskAmountLabel(task))
  return parts.join(' · ')
}
const relayRecentLabel = (task: TransferTask) => {
  const minutes = Math.max(0, Math.round((Date.now() - taskTimestamp(task)) / 60000))
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.round(minutes / 60)
  return hours < 24 ? `${hours} 小时前` : `${Math.round(hours / 24)} 天前`
}


// 同步当前页面到 composable，供拖拽路由判断（网盘页拖入直接上传）
watch(view, value => { app.activeView.value = value })
// 管理页停在前台时若换成非管理员账号登录，必须退回首页，否则工作区会空白。
watch(() => app.currentUser.value.isAdmin, isAdmin => {
  if (!isAdmin && view.value === 'admin') view.value = 'overview'
})
// 云上传完成后刷新已挂载的网盘列表；未进入网盘页时由组件挂载自行加载。
watch(completedCloudUploads, (current, previous) => {
  if (previous !== undefined && current !== previous) cloudRef.value?.refresh()
})

// ─── 插件视图（动态）───
// 侧边栏显示未禁用的插件；禁用/卸载当前插件时退回首页。
const enabledPlugins = computed(() => pluginSys.plugins.value.filter(p => !p.disabled))
const activePluginId = computed(() => {
  const id = pluginSys.parsePluginView(view.value)
  return id && pluginSys.plugins.value.some(p => p.id === id && !p.disabled) ? id : null
})
const activePlugin = computed(() =>
  enabledPlugins.value.find(p => p.id === activePluginId.value) ?? null)
watch(() => pluginSys.plugins.value.map(p => `${p.id}:${p.disabled}`).join('|'), () => {
  if (view.value.startsWith('plugin:') && !activePluginId.value) view.value = 'overview'
})
// 插件图标：emoji 直接显示；包内文件路径（含 / 或 .png 等）用 img。
const isIconPath = (icon: string) => !!icon && /[/.]/.test(icon)

const showAllTasks = () => {
  view.value = 'transfers'
  activityOpen.value = false
}

const handleCloudUpload = async (targetDir = '') => {
  await app.cloudUpload(targetDir)
}
const handleCloudUploadFolder = async (targetDir = '') => {
  await app.cloudUploadFolder(targetDir)
}
const handleCloudDownload = async (key: string) => {
  await app.cloudDownload(key)
}
const handleCloudDelete = async (key: string, subCount?: number, subDirCount?: number) => {
  // 目录 key 带尾斜杠：递归删时子文件逐个进回收站，目录标记本身直接删（目录没有
  // es_file 行，不进回收站）。文案必须分开讲，不能沿用文件的「可在回收站还原」
  // 让用户以为文件夹也能一键还原——还原其中任一文件时目录会按路径重新出现。
  const isDir = key.endsWith('/')
  const name = key.split('/').filter(Boolean).pop() ?? key
  const inner = subCount || subDirCount
    ? `及其中${subCount ? ` ${subCount} 个文件` : ''}${subCount && subDirCount ? '、' : ''}${subDirCount ? ` ${subDirCount} 个子文件夹` : ''}`
    : ''
  const confirmed = await confirm({
    title: isDir ? '删除云端文件夹' : '删除云端文件',
    message: isDir
      ? inner
        ? `确定删除文件夹「${name}」${inner}？文件删除后可在回收站还原，还原任一文件时该文件夹会重新出现；文件夹本身不进回收站。`
        : `确定删除空文件夹「${name}」？文件夹不进回收站，删除后不可还原。`
      : `确定删除「${name}」？删除后可在回收站还原，回收站保留 ${RECYCLE_RETENTION_DAYS} 天。`,
    confirmText: '删除',
    danger: true,
  })
  if (!confirmed) return
  await app.cloudDelete(key)
  cloudRef.value?.refresh()
}

const handleLogin = (username: string, password: string) => {
  void app.login(username, password)
}
// 头像显示：优先昵称首字，其次账号首字
const avatarText = computed(() => {
  const u = app.currentUser.value
  const name = u.nickName || u.userName || ''
  return name ? Array.from(name)[0] : '我'
})
</script>

<template>
  <!-- 窗口控制条：拖拽区域 + 最小化/最大化/关闭（macOS 用原生红绿灯）。
       登录页与主界面共用——登录前快捷操作区不渲染，避免暴露活动/账号入口。 -->
  <div class="window-chrome">
    <!-- 顶栏左端补产品名：原来整条 1900px 只有右端两个胶囊，中间全空，
         看着像没画完。产品标识从侧栏顶部移到这里，侧栏顶部让给图标轨 -->
    <div class="chrome-brand">EasyShare</div>
    <div class="drag-region" @dblclick="toggleMaximise" />
    <div v-if="app.currentUser.value.loggedIn" class="window-quick-actions">
      <button
        ref="activityTriggerRef"
        class="activity-trigger"
        type="button"
        :aria-label="activityLabel"
        :aria-expanded="activityOpen"
        aria-controls="activity-title"
        @click="activityOpen = !activityOpen"
      >
        <svg viewBox="0 0 24 24"><path d="M5 7h14v12H5z"/><path d="M8 4h8M9 11h6M9 15h4"/></svg>
        <span>活动</span>
        <b v-if="activeTaskCount" class="activity-badge">{{ activeTaskCount }}</b>
        <b v-if="failedTaskCount" class="activity-badge failed">! {{ failedTaskCount }}</b>
      </button>

      <!-- 当前账号：头像（点击进设置）+ 昵称 + 登出 -->
      <div class="account-chip" :title="app.currentUser.value.nickName || app.currentUser.value.userName">
        <button class="account-avatar" type="button" aria-label="账号设置" title="账号设置" @click="view = 'settings'">{{ avatarText }}</button>
        <span class="account-name">{{ app.currentUser.value.nickName || app.currentUser.value.userName }}</span>
        <button class="account-logout" type="button" aria-label="退出登录" title="退出登录" @click="app.logout">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5M21 12H9"/></svg>
        </button>
      </div>
    </div>
    <div v-if="!isMac" class="window-controls">
      <button class="win-btn" type="button" aria-label="最小化" @click="WindowMinimise()">
        <svg viewBox="0 0 24 24"><path d="M5 12h14"/></svg>
      </button>
      <button class="win-btn" type="button" :aria-label="isMaximised ? '还原' : '最大化'" :title="isMaximised ? '还原' : '最大化'" @click="toggleMaximise">
        <svg v-if="isMaximised" viewBox="0 0 24 24"><path d="M9 9h9v9H9z"/><path d="M6 15H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h9a1 1 0 0 1 1 1v1"/></svg>
        <svg v-else viewBox="0 0 24 24"><rect x="5" y="5" width="14" height="14" rx="1"/></svg>
      </button>
      <button class="win-btn close" type="button" aria-label="关闭到托盘" @click="Quit()">
        <svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6L6 18"/></svg>
      </button>
    </div>
  </div>

  <!-- 登录门禁：未登录只显示登录页（P1，见 ADR-0007） -->
  <LoginView
    v-if="!app.currentUser.value.loggedIn"
    :loading="app.loggingIn.value"
    :error="app.error.value"
    @submit="handleLogin"
  />
  <div v-else :class="['app-shell', isMac ? 'is-mac' : '']">
    <!-- 全局确认对话框：useConfirm 驱动，替换原生 window.confirm -->
    <ConfirmDialog
      v-model:open="confirmState.open"
      :title="confirmState.title"
      :message="confirmState.message"
      :confirm-text="confirmState.confirmText"
      :danger="confirmState.danger"
      @confirm="settle(true)"
      @cancel="settle(false)"
    />
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

    <ActivityDrawer
      v-if="activityOpen"
      :tasks="app.snapshot.value.tasks"
      @close="closeActivityDrawer"
      @view-all="showAllTasks"
    />

    <div class="app-body">
      <!-- ① 图标轨（对标 WPS 左轨）：只放产品域，图标 + 10px 标签，选中抬起成白底胶囊。
             原来 9 个内置视图 + N 个插件实例全挤在一个 220px 列表里，是「不像专业软件」的主因 -->
      <nav class="nav-rail" aria-label="产品切换">
        <div class="rail-brand" title="EasyShare 统一文件空间">
          <svg viewBox="0 0 32 32"><path d="M8.5 12a6.5 6.5 0 0 1 12.6-2.2A5.5 5.5 0 1 1 22.5 20H8a5 5 0 0 1 .5-8Z"/><path d="m12 15 4-4 4 4M16 11v11"/></svg>
        </div>

        <button :class="['rail-item', railSection === 'home' ? 'active' : '']" type="button" aria-label="总览" :aria-current="railSection === 'home' ? 'page' : undefined" @click="openSection('home')">
          <svg viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7" rx="2"/><rect x="14" y="3" width="7" height="7" rx="2"/><rect x="3" y="14" width="7" height="7" rx="2"/><rect x="14" y="14" width="7" height="7" rx="2"/></svg>
          <span>总览</span>
        </button>
        <button :class="['rail-item', railSection === 'drive' ? 'active' : '']" type="button" aria-label="网盘" :aria-current="railSection === 'drive' ? 'page' : undefined" @click="openSection('drive')">
          <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/><path d="M12 12v5m0-5-2 2m2-2 2 2"/></svg>
          <span>网盘</span>
        </button>
        <button :class="['rail-item', railSection === 'knowledge' ? 'active' : '']" type="button" aria-label="知识" :aria-current="railSection === 'knowledge' ? 'page' : undefined" @click="openSection('knowledge')">
          <svg viewBox="0 0 24 24"><path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.7.5 1 1.3 1 2.1h5c0-.8.3-1.6 1-2.1A6 6 0 0 0 12 3z"/></svg>
          <span>知识</span>
        </button>
        <button :class="['rail-item', railSection === 'apps' ? 'active' : '']" type="button" aria-label="扩展" :aria-current="railSection === 'apps' ? 'page' : undefined" @click="openSection('apps')">
          <svg viewBox="0 0 24 24"><path d="M11.6 4.2a2 2 0 0 1 2.8 0l.4.4.6-.1a2 2 0 0 1 2.1 2.1l-.1.6.4.4a2 2 0 0 1 0 2.8l-.4.4.1.6a2 2 0 0 1-2.1 2.1l-.6-.1-.4.4a2 2 0 0 1-2.8 0l-.4-.4-.6.1a2 2 0 0 1-2.1-2.1l.1-.6-.4-.4a2 2 0 0 1 0-2.8l.4-.4-.1-.6a2 2 0 0 1 2.1-2.1l.6.1Z"/><path d="M9.5 14.5 5 19"/></svg>
          <span>扩展</span>
          <i v-if="pluginSys.updateNotices.value.length" class="rail-dot" :title="`有 ${pluginSys.updateNotices.value.length} 个插件可更新`" />
        </button>

        <span class="rail-spacer" />

        <button :class="['rail-item', railSection === 'settings' ? 'active' : '']" type="button" aria-label="设置" :aria-current="railSection === 'settings' ? 'page' : undefined" @click="openSection('settings')">
          <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a6 6 0 0 1-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9c.6.24 1.14.78 1.51 1h.09a2 2 0 0 1 0 4h-.09c-.37.22-.91.76-1.51 1Z"/></svg>
          <span>设置</span>
          <i v-if="app.updateAvailable.value" class="rail-dot" :title="`发现新版本 ${app.updateAvailable.value}`" />
        </button>
      </nav>

      <!-- ② 上下文面板：只列当前域内的入口。插件实例不再和主导航平级 -->
      <aside class="context-panel">
        <header class="panel-head">
          <strong>{{ sectionMeta[railSection].title }}</strong>
          <span>{{ sectionMeta[railSection].subtitle }}</span>
        </header>

        <nav class="panel-nav" aria-label="域内导航">
          <template v-if="railSection === 'home'">
            <button :class="['nav-item', view === 'overview' ? 'active' : '']" :aria-current="view === 'overview' ? 'page' : undefined" type="button" @click="view = 'overview'">
              <svg viewBox="0 0 24 24"><path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/></svg>
              工作台
            </button>
            <button :class="['nav-item', view === 'devices' ? 'active' : '']" :aria-current="view === 'devices' ? 'page' : undefined" type="button" @click="view = 'devices'">
              <svg viewBox="0 0 24 24"><rect x="3" y="5" width="14" height="10" rx="2"/><path d="M8 19h4M10 15v4M19 9h2v10h-6v-2"/></svg>
              附近设备
              <span class="nav-count">{{ app.snapshot.value.peers.length }}</span>
            </button>
            <button :class="['nav-item', view === 'transfers' ? 'active' : '']" :aria-current="view === 'transfers' ? 'page' : undefined" type="button" @click="view = 'transfers'">
              <svg viewBox="0 0 24 24"><path d="M7 3v13m0 0-4-4m4 4 4-4M17 21V8m0 0-4 4m4-4 4 4"/></svg>
              任务中心
              <span class="nav-count">{{ app.snapshot.value.tasks.length }}</span>
            </button>
            <button v-if="app.currentUser.value.isAdmin" :class="['nav-item', view === 'admin' ? 'active' : '']" :aria-current="view === 'admin' ? 'page' : undefined" type="button" @click="view = 'admin'">
              <svg viewBox="0 0 24 24"><path d="M12 3 4 6v5c0 4.6 3.2 8.8 8 10 4.8-1.2 8-5.4 8-10V6l-8-3Z"/><path d="m9 12 2 2 4-4"/></svg>
              管理后台
            </button>
          </template>

          <template v-else-if="railSection === 'drive'">
            <button :class="['nav-item', view === 'cloud' ? 'active' : '']" :aria-current="view === 'cloud' ? 'page' : undefined" type="button" @click="view = 'cloud'">
              <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/></svg>
              我的网盘
            </button>
            <button class="nav-item" type="button" @click="openRecycleBin">
              <svg viewBox="0 0 24 24"><path d="M4 7h16M9 7V5h6v2M6 7l1 13h10l1-13M10 11v6M14 11v6"/></svg>
              回收站
            </button>
          </template>

          <template v-else-if="railSection === 'knowledge'">
            <button :class="['nav-item', view === 'knowledge' ? 'active' : '']" :aria-current="view === 'knowledge' ? 'page' : undefined" type="button" @click="view = 'knowledge'">
              <svg viewBox="0 0 24 24"><path d="M12 4.5C10.5 3.5 8.5 3 6 3v15c2.5 0 4.5.5 6 1.5 1.5-1 3.5-1.5 6-1.5V3c-2.5 0-4.5.5-6 1.5Z"/><path d="M12 4.5v15"/></svg>
              知识问答
            </button>
            <button :class="['nav-item', view === 'meetings' ? 'active' : '']" :aria-current="view === 'meetings' ? 'page' : undefined" type="button" @click="view = 'meetings'">
              <svg viewBox="0 0 24 24"><path d="M12 15a3 3 0 0 0 3-3V6a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3z"/><path d="M19 11a7 7 0 0 1-14 0M12 18v3M8 21h8"/></svg>
              会议听记
            </button>
          </template>

          <template v-else-if="railSection === 'apps'">
            <button :class="['nav-item', view === 'plugins' ? 'active' : '']" :aria-current="view === 'plugins' ? 'page' : undefined" type="button" @click="view = 'plugins'">
              <svg viewBox="0 0 24 24"><path d="M4 5h7v7H4zM13 5h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z"/></svg>
              插件中心
              <span v-if="pluginSys.updateNotices.value.length" class="nav-update-dot" :title="`有 ${pluginSys.updateNotices.value.length} 个插件可更新`" />
            </button>
            <button
              v-for="p in enabledPlugins"
              :key="p.id"
              :class="['nav-item', view === `plugin:${p.id}` ? 'active' : '']" :aria-current="activePluginId === p.id ? 'page' : undefined"
              type="button"
              :title="p.description"
              @click="view = `plugin:${p.id}` as View"
            >
              <span v-if="isIconPath(p.icon)" class="nav-plugin-icon"><img :src="`/plugins/${p.id}/${p.icon}`" alt=""></span>
              <span v-else class="nav-plugin-icon">{{ p.icon || '🧩' }}</span>
              {{ p.name }}
              <span v-if="p.builtin" class="nav-plugin-builtin" title="内置插件">内</span>
            </button>
          </template>

          <template v-else>
            <button class="nav-item active" type="button" aria-current="page">
              <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a6 6 0 0 1-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9c.6.24 1.14.78 1.51 1h.09a2 2 0 0 1 0 4h-.09c-.37.22-.91.76-1.51 1Z"/></svg>
              账号与服务
            </button>
          </template>
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

      <section :class="['workspace', activePlugin ? 'is-plugin' : '']">
        <!-- ═══ 设置页 ═══ -->
        <SettingsPanel v-if="view === 'settings'" @logout="app.logout" />

        <!-- ═══ 管理页（仅管理员）═══ -->
        <AdminPanel v-else-if="view === 'admin' && app.currentUser.value.isAdmin" />

        <!-- ═══ 服务已退出 ═══ -->
        <div v-else-if="app.stopped.value" class="stopped-view" role="status">
          <div class="stopped-icon">
            <svg viewBox="0 0 24 24"><path d="M12 3v9M7.2 5.8a8 8 0 1 0 9.6 0"/></svg>
          </div>
          <h1>EasyShare 服务已安全退出</h1>
          <p>共享空间与网盘已停止访问，后台 Core 均已停止。现在可以关闭此窗口。</p>
          <span>运行日志保存在 {{ app.logDirectory.value }}</span>
        </div>

        <!-- ═══ 首页 ═══ -->
        <template v-else-if="view === 'overview'">
          <div class="relay-page">
            <header class="relay-topbar">
              <div class="relay-heading">
                <h1>工作台</h1>
                <span>这台电脑此刻在跟谁交换什么</span>
              </div>
              <div class="relay-statusline" aria-label="服务状态">
                <span v-for="service in relayServices" :key="service.label" :class="{ off: !service.on }">
                  <i />{{ service.label }}
                </span>
              </div>
              <button class="relay-primary" type="button" @click="view = 'devices'">
                <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
                发送文件
              </button>
            </header>

            <div v-if="app.error.value" class="alert" role="alert">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 9v4m0 4h.01M10 3.8 2.6 17a2 2 0 0 0 1.8 3h15.2a2 2 0 0 0 1.8-3L14 3.8a2.3 2.3 0 0 0-4 0Z"/></svg>
              <span class="alert-copy">
                <b>{{ app.error.value }}</b>
                <small>详细运行日志：{{ app.logDirectory.value }}</small>
              </span>
              <button type="button" aria-label="关闭错误提示" @click="app.clearError">×</button>
            </div>

            <div class="relay-grid">
              <section class="relay-column">
                <header class="relay-column-head">
                  <h2>附近设备</h2>
                  <span>{{ app.snapshot.value.peers.length }} 台在线</span>
                  <button class="relay-link" type="button" @click="view = 'devices'">全部设备</button>
                </header>
                <div v-if="!relayPeers.length" class="relay-empty">
                  <span class="relay-empty-glyph" aria-hidden="true">
                    <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="2"/><path d="M8.5 8.5a5 5 0 0 0 0 7M15.5 8.5a5 5 0 0 1 0 7M5.5 5.5a9 9 0 0 0 0 13M18.5 5.5a9 9 0 0 1 0 13"/></svg>
                  </span>
                  <strong>还没有发现设备</strong>
                  <span>确保对方也打开了 EasyShare，并连在同一个局域网。</span>
                </div>
                <article v-for="peer in relayPeers" :key="peer.deviceId" class="relay-device">
                  <span class="relay-device-glyph" aria-hidden="true">
                    <svg viewBox="0 0 24 24"><rect x="3" y="5" width="14" height="10" rx="2"/><path d="M8 19h4M10 15v4M19 9h2v10h-6v-2"/></svg>
                  </span>
                  <div class="relay-device-copy">
                    <strong>{{ peer.deviceName }}</strong>
                    <span>{{ peer.ip }}:{{ peer.transferPort }}</span>
                  </div>
                  <span :class="['relay-device-state', peerState(peer).tone]">{{ peerState(peer).label }}</span>
                  <button
                    class="relay-send-btn"
                    type="button"
                    :aria-label="`发送文件到 ${peer.deviceName}`"
                    :title="`发送文件到 ${peer.deviceName}`"
                    @click="app.send(peer.deviceId)"
                  >
                    <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
                  </button>
                </article>
              </section>

              <section class="relay-column relay-column-tasks">
                <header class="relay-column-head">
                  <h2>正在传输</h2>
                  <span v-if="relayTasks.length">{{ activeTaskCount }} 个进行中<template v-if="pendingTaskCount"> · {{ pendingTaskCount }} 个待确认</template></span>
                  <span v-else>没有进行中的任务</span>
                  <button class="relay-link" type="button" @click="view = 'transfers'">任务中心</button>
                </header>
                <div v-if="!relayTasks.length" class="relay-empty">
                  <span class="relay-empty-glyph" aria-hidden="true">
                    <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6M9 13h6M9 17h4"/></svg>
                  </span>
                  <strong>当前没有传输</strong>
                  <span>把文件拖到窗口，或从附近设备里选一台发送。</span>
                </div>
                <article v-for="task in relayTasks" :key="task.id" :class="['relay-task', relayTaskTone(task)]">
                  <div class="relay-task-head">
                    <strong>{{ task.fileName }}</strong>
                    <span v-if="task.status === 'running'" class="relay-task-pct">{{ taskProgress(task) }}%</span>
                    <span v-else-if="task.status === 'pending'" class="relay-task-pct pending">待确认</span>
                    <span v-else-if="task.status === 'failed'" class="relay-task-pct failed">失败</span>
                    <span v-else class="relay-task-pct">{{ taskStatusLabel(task.status) }}</span>
                  </div>
                  <div class="relay-task-meta">
                    <span>{{ relayTaskMeta(task) }}</span>
                    <span v-if="task.error" class="relay-task-error">{{ task.error }}</span>
                  </div>
                  <div
                    v-if="['running', 'accepted', 'queued'].includes(task.status)"
                    class="relay-track"
                    role="progressbar"
                    :aria-valuenow="taskProgress(task)"
                    aria-valuemin="0"
                    aria-valuemax="100"
                  >
                    <i :style="{ width: `${taskProgress(task)}%` }" />
                  </div>
                  <div v-if="task.status === 'pending' && taskKind(task) === 'lan_receive'" class="relay-task-actions">
                    <button class="relay-mini" type="button" @click="app.reject(task.id)">拒绝</button>
                    <button class="relay-mini" type="button" @click="app.acceptAs(task.id)">另存</button>
                    <button class="relay-mini primary" type="button" @click="app.accept(task.id)">接收</button>
                  </div>
                  <div v-else-if="task.status === 'failed'" class="relay-task-actions">
                    <button class="relay-mini" type="button" @click="view = 'transfers'">查看任务</button>
                  </div>
                </article>
              </section>
            </div>

            <div class="relay-foot">
              <section class="relay-foot-block">
                <header class="relay-foot-head">
                  <h3>刚刚完成</h3>
                  <button class="relay-link" type="button" @click="view = 'transfers'">全部任务</button>
                </header>
                <div v-if="!relayCompleted.length" class="relay-foot-empty">还没有完成记录</div>
                <button v-for="task in relayCompleted" :key="task.id" class="relay-done" type="button" @click="view = 'transfers'">
                  <span class="relay-done-icon done" aria-hidden="true">
                    <svg viewBox="0 0 24 24"><path d="m5 12 4 4L19 6"/></svg>
                  </span>
                  <strong>{{ task.fileName }}</strong>
                  <span class="relay-done-time">{{ relayRecentLabel(task) }}</span>
                </button>
              </section>

              <section class="relay-foot-block">
                <header class="relay-foot-head"><h3>共享与网盘</h3></header>
                <button class="relay-done" type="button" @click="view = 'settings'">
                  <span class="relay-done-icon share" aria-hidden="true">
                    <svg viewBox="0 0 24 24"><path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1.5 1.5"/><path d="M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1.5-1.5"/></svg>
                  </span>
                  <strong>EasyShare 共享</strong>
                  <span class="relay-done-time">{{ app.snapshot.value.status.webdav ? '此电脑可见' : '未启动' }}</span>
                </button>
                <button class="relay-done" type="button" @click="view = 'cloud'">
                  <span class="relay-done-icon cloud" aria-hidden="true">
                    <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/></svg>
                  </span>
                  <strong>个人网盘</strong>
                  <span class="relay-done-time">{{ app.snapshot.value.status.cloudEnabled ? '已连接' : '未连接' }}</span>
                </button>
              </section>
            </div>

            <div class="relay-drive-strip">
              <span class="relay-drive-glyph" aria-hidden="true">
                <svg viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/></svg>
              </span>
              <div class="relay-drive-copy">
                <strong>EasyShare 共享</strong>
                <span>此电脑 → EasyShare 共享 · 无需映射盘符</span>
              </div>
              <span :class="['relay-drive-state', app.snapshot.value.status.webdav ? 'on' : 'off']">
                <i />{{ app.snapshot.value.status.webdav ? 'WebDAV 运行中' : 'WebDAV 未启动' }}
              </span>
              <button v-if="app.snapshot.value.status.webdav" class="relay-strip-btn" type="button" @click="app.stopDrive">停止共享</button>
              <button v-else class="relay-strip-btn primary" type="button" @click="app.startDrive">启动共享</button>
            </div>
          </div>
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

        <!-- ═══ 统一任务中心详情页 ═══ -->
        <template v-else-if="view === 'transfers'">
          <header class="workspace-header">
            <div>
              <span class="section-label">活动</span>
              <h1>任务中心</h1>
              <p>统一查看局域网传输、云端上传下载和后续同步处理任务。</p>
            </div>
          </header>
          <TransferList :tasks="app.snapshot.value.tasks" @accept="app.accept" @accept-as="app.acceptAs" @reject="app.reject" @clear="app.clearHistory" @delete="app.deleteTask" />
        </template>

        <!-- ═══ 知识问答页 ═══ -->
        <KnowledgePanel v-else-if="view === 'knowledge'" />

        <!-- ═══ 会议听记页（录音 → 转写 → 发言人 → 纪要）═══ -->
        <MeetingsPanel v-else-if="view === 'meetings'" @goto-knowledge="view = 'knowledge'" />

        <!-- ═══ 插件中心（商城 + 已装管理）═══ -->
        <PluginMarket v-else-if="view === 'plugins'" />

        <!-- ═══ 插件页（沙箱 iframe 宿主）═══ -->
        <template v-else-if="activePlugin">
          <header class="workspace-header">
            <div>
              <span class="section-label">插件</span>
              <h1>{{ activePlugin.name }}</h1>
              <p>{{ activePlugin.description }}</p>
            </div>
            <div class="plugin-version">v{{ activePlugin.version }}</div>
          </header>
          <PluginHost :info="activePlugin" />
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

