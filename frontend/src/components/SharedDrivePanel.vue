<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useConfirm } from '../composables/useConfirm'
import { core } from '../services/core'
import type { DriveFileEntry } from '../types/core'
import FileMemberEditor from './FileMemberEditor.vue'
const props = defineProps<{ operatorId: string; knowledgeEnabled?: boolean }>()
const files = ref<DriveFileEntry[]>([])
const loading = ref(false)
const error = ref('')
const notice = ref('')
const directory = ref('')
const query = ref('')
const memberFile = ref<DriveFileEntry | null>(null)
const renameFile = ref<DriveFileEntry | null>(null)
const renameName = ref('')
const { confirm } = useConfirm()
let loadRevision = 0
let accountRevision = 0
let disposed = false
const validIdentity = (file: DriveFileEntry) => /^[1-9]\d*$/.test(file.fileId) && /^[1-9]\d*$/.test(file.versionId)
const busy = ref(false)
const menu = ref<{ file: DriveFileEntry; x: number; y: number } | null>(null)
const labels = { manage: '可管理', edit: '可编辑', download: '可查看下载', read: '仅可查看' }
const rank = (file: DriveFileEntry) => ({ manage:4, edit:3, download:2, read:1 }[file.effectiveRole ?? 'read'] ?? 0)
const load = async () => {
  const request = ++loadRevision
  loading.value = true; error.value = ''
  try { const result = await core.cloudListInSpace('shared'); if (!disposed && request === loadRevision) files.value = result }
  catch (e) { if (!disposed && request === loadRevision) error.value = String(e) }
  finally { if (!disposed && request === loadRevision) loading.value = false }
}
watch(() => props.operatorId, () => {
  ++accountRevision
  files.value = []; directory.value = ''; query.value = ''; memberFile.value = null; renameFile.value = null; menu.value = null; notice.value = ''; busy.value = false
  if (props.operatorId) void load()
  else { ++loadRevision; loading.value = false }
}, { immediate: true })
onBeforeUnmount(() => { disposed = true; ++loadRevision; ++accountRevision })
const scope = computed(() => directory.value ? `${directory.value}/` : '')
const folders = computed(() => {
  const names = new Set<string>()
  for (const file of files.value) {
    if (!file.key.startsWith(scope.value)) continue
    const rest = file.key.slice(scope.value.length)
    if (rest.includes('/')) names.add(rest.split('/')[0])
  }
  return [...names].sort()
})
const displayed = computed(() => files.value.filter(file => {
  if (file.key.endsWith('/') || !file.key.startsWith(scope.value)) return false
  const rest = file.key.slice(scope.value.length)
  return query.value.trim() ? rest.toLowerCase().includes(query.value.trim().toLowerCase()) : !rest.includes('/')
}))
const act = async (work: () => Promise<unknown>, text?: string) => {
  if (busy.value) return
  const request = accountRevision
  busy.value = true; error.value = ''; menu.value = null
  try { await work(); if (text && !disposed && request === accountRevision) notice.value = text }
  catch (e) { if (!disposed && request === accountRevision) error.value = e instanceof Error ? e.message : String(e) }
  finally { if (!disposed && request === accountRevision) busy.value = false }
}
const download = (file: DriveFileEntry, saveAs = false) => act(() => saveAs
  ? core.cloudDownloadAsInSpace('shared', file.key) : core.cloudDownloadInSpace('shared', file.key), '已开始下载，可在「活动」查看进度')
const process = (file: DriveFileEntry) => act(() => core.knowledgeProcessDriveFile(file.fileId, file.versionId), '已提交加入知识库，可在「知识 → 文档」查看处理进度')
const manage = (file: DriveFileEntry) => { menu.value = null; memberFile.value = file }
const rename = (file: DriveFileEntry) => { menu.value = null; renameFile.value = file; renameName.value = file.name }
const saveRename = () => {
  const file = renameFile.value
  if (!file || !renameName.value.trim()) return
  void act(async () => { await core.cloudRenameInSpace('shared', file.key, renameName.value.trim()); renameFile.value = null; await load() })
}
const remove = async (file: DriveFileEntry) => {
  menu.value = null
  const revision = accountRevision
  const accepted = await confirm({ title: `删除「${file.name}」？`, message: '文件将移入回收站。', confirmText: '删除', danger: true })
  if (!accepted || disposed || revision !== accountRevision) return
  void act(async () => { await core.cloudDeleteInSpace('shared', file.key); if (revision === accountRevision) await load() })
}
const context = (event: MouseEvent, file: DriveFileEntry) => {
  menu.value = { file, x: Math.max(8, Math.min(event.clientX, window.innerWidth - 210)), y: Math.max(8, Math.min(event.clientY, window.innerHeight - 280)) }
}
const modal = ref<HTMLElement | null>(null)
let previousFocus: HTMLElement | null = null
watch([memberFile, renameFile], async ([member, rename]) => {
  if (member || rename) {
    previousFocus = document.activeElement as HTMLElement | null
    await nextTick()
    modal.value?.querySelector<HTMLElement>('button, input, select')?.focus()
  } else previousFocus?.focus()
})
const modalKey = (event: KeyboardEvent) => {
  if (event.key === 'Escape') { event.stopPropagation(); if (!busy.value) { memberFile.value = null; renameFile.value = null } }
  if (event.key !== 'Tab') return
  const focusable = modal.value?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select:not(:disabled)')
  if (!focusable?.length) return
  const first = focusable[0], last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}
defineExpose({ refresh: load })
</script>
<template>
  <section class="card shared-drive-panel" @click="menu = null" @keydown.esc="menu = null">
    <header class="card-header"><div><span class="section-label">云端</span><h2>共享空间</h2></div><button class="text-button" type="button" :disabled="busy" @click="load">刷新</button></header>
    <p class="setting-hint">文件操作按当前有效权限展示，控制面逐次裁决。仅可查看的文件请到「知识 → 文档」阅读已入库内容；尚未入库时暂无在线阅读入口。</p>
    <p v-if="error" class="detail-rename-error" role="alert">{{ error }}</p>
    <p v-if="notice" class="cloud-notice" role="status">{{ notice }}</p>
    <nav class="cloud-crumbs" aria-label="共享目录"><button class="crumb" type="button" @click="directory = ''">共享空间</button><span v-if="directory">/ {{ directory }}</span><button v-if="directory" class="text-button" type="button" @click="directory = directory.split('/').slice(0,-1).join('/')">上级目录</button></nav>
    <input class="setting-input" v-model="query" aria-label="搜索共享文件" placeholder="搜索当前目录及子目录">
    <p v-if="loading">加载中…</p>
    <div v-else class="shared-file-list">
      <button v-for="folder in folders" v-show="!query" :key="folder" class="shared-folder-row" type="button" @click="directory = scope + folder">{{ folder }} /</button>
      <article v-for="file in displayed" :key="file.fileId || file.key" class="shared-file-row" @contextmenu.prevent="context($event,file)">
        <div class="shared-file-main"><strong>{{ file.name }}</strong><span>{{ labels[file.effectiveRole ?? 'read'] }}{{ file.uploadByName ? ` · 上传者 ${file.uploadByName}` : '' }}</span></div>
        <div class="shared-file-actions">
          <button v-if="rank(file) >= 2" class="secondary-button compact" type="button" :disabled="busy" @click="download(file)">下载</button>
          <button v-if="rank(file) >= 3" class="secondary-button compact" type="button" :disabled="busy" @click="rename(file)">改名</button>
          <button v-if="rank(file) >= 4" data-testid="manage-members" class="secondary-button compact" type="button" :disabled="busy" @click="manage(file)">成员管理</button>
          <button v-if="rank(file) >= 4" class="secondary-button compact destructive-text" type="button" :disabled="busy" @click="remove(file)">删除</button>
          <button v-if="knowledgeEnabled && rank(file) >= 2 && validIdentity(file)" data-testid="process-knowledge" class="secondary-button compact" type="button" :disabled="busy" @click="process(file)">加入知识库</button>
        </div>
      </article>
      <p v-if="!displayed.length && !loading" class="admin-empty">{{ query.trim() ? '没有匹配文件，请更换关键词。' : '当前目录没有可见文件。' }}</p>
    </div>
    <div v-if="menu" class="ctx-menu" :style="{left: `${menu.x}px`, top: `${menu.y}px`}" @click.stop>
      <button v-if="rank(menu.file) >= 2" class="ctx-item" type="button" :disabled="busy" @click="download(menu.file)">下载到接收目录</button>
      <button v-if="rank(menu.file) >= 2" class="ctx-item" type="button" :disabled="busy" @click="download(menu.file,true)">另存为…</button>
      <button v-if="rank(menu.file) >= 3" class="ctx-item" type="button" :disabled="busy" @click="rename(menu.file)">重命名</button>
      <button v-if="rank(menu.file) >= 4" class="ctx-item" type="button" :disabled="busy" @click="manage(menu.file)">成员管理</button>
      <button v-if="knowledgeEnabled && rank(menu.file) >= 2 && validIdentity(menu.file)" class="ctx-item" type="button" :disabled="busy" @click="process(menu.file)">加入知识库</button>
      <button v-if="rank(menu.file) >= 4" class="ctx-item danger" type="button" :disabled="busy" @click="remove(menu.file)">删除</button>
      <span v-if="rank(menu.file) < 2" class="setting-hint">仅可查看 · 到知识文档阅读</span>
    </div>
    <div v-if="memberFile" class="move-overlay" @click.self="memberFile = null">
      <div ref="modal" class="share-dialog file-member-dialog" @keydown="modalKey" role="dialog" aria-label="文件成员管理" aria-modal="true"><header class="move-head"><b>成员管理</b><button data-testid="close-members" class="text-button" type="button" @click="memberFile = null">关闭</button></header><FileMemberEditor :file-id="memberFile.fileId" :file-name="memberFile.name" :operator-id="operatorId" @saved="notice = '成员角色已保存'; load()" /></div>
    </div>
    <div v-if="renameFile" class="move-overlay"><form ref="modal" class="share-dialog" @keydown="modalKey" role="dialog" aria-modal="true" aria-label="共享文件改名" @submit.prevent="saveRename"><h3>重命名</h3><input class="setting-input" v-model="renameName" aria-label="新文件名" :disabled="busy"><p v-if="error" role="alert">{{ error }}</p><button class="primary-button compact" type="submit" :disabled="busy">保存</button><button class="text-button" type="button" :disabled="busy" @click="renameFile = null">取消</button></form></div>
  </section>
</template>
