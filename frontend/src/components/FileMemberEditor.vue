<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { core } from '../services/core'
import { presenceMotion, vContentMotion } from '../directives/contentMotion'
import type { FileMemberRole, FileMemberCandidate } from '../types/core'
const props = defineProps<{ fileId: string; fileName: string; operatorId: string }>()
const emit = defineEmits<{ saved: []; busyChange: [busy: boolean] }>()
const rows = ref<FileMemberRole[]>([])
const candidates = ref<FileMemberCandidate[]>([])
const keyword = ref('')
const loading = ref(false)
const busy = ref(false)
const searching = ref(false)
const searched = ref(false)
const loadError = ref('')
const searchError = ref('')
const saveError = ref('')
const loaded = ref(false)
const saved = ref(false)
const baseline = ref('')
const pendingRemove = ref<FileMemberRole | null>(null)
const cancelRemove = ref<HTMLButtonElement | null>(null)
const saveButton = ref<HTMLButtonElement | null>(null)
let removeTrigger: HTMLElement | null = null
let generation = 0
let searchGeneration = 0
onBeforeUnmount(() => { ++generation; ++searchGeneration; emit('busyChange', false) })
watch(busy, value => emit('busyChange', value), { flush: 'sync' })
const labels = { manage: '可管理', edit: '可编辑', download: '可查看下载', read: '仅可查看' }
const self = (row: Pick<FileMemberRole, 'memberType' | 'memberId'>) => row.memberType === 'user' && row.memberId === props.operatorId
const key = (row: Pick<FileMemberRole, 'memberType' | 'memberId'>) => `${row.memberType}:${row.memberId}`
const payload = () => rows.value.filter(r => !self(r)).map(r => ({ memberType: r.memberType, memberId: r.memberId, role: r.role }))
const dirty = computed(() => loaded.value && JSON.stringify(payload()) !== baseline.value)
watch(() => JSON.stringify(payload()), () => { saved.value = false; saveError.value = '' })
const message = (e: unknown) => e instanceof Error ? e.message : String(e)
const load = async () => {
  if (busy.value) return
  const request = ++generation
  ++searchGeneration
  rows.value = []; candidates.value = []; keyword.value = ''
  loadError.value = ''; searchError.value = ''; saveError.value = ''; saved.value = false
  searching.value = false; loaded.value = false; searched.value = false; pendingRemove.value = null
  loading.value = !!props.fileId && !!props.operatorId
  if (!loading.value) return
  try {
    const result = await core.fileMemberList(props.fileId)
    if (request !== generation) return
    rows.value = (result ?? []).map(row => ({ ...row }))
    baseline.value = JSON.stringify(payload()); loaded.value = true
  } catch (e) { if (request === generation) loadError.value = message(e) }
  finally { if (request === generation) loading.value = false }
}
watch(() => [props.fileId, props.operatorId], () => {
  // 目标切换独立于旧提交生命周期，迟到结果不能作用于新目标。
  ++generation; busy.value = false; void load()
}, { immediate: true })
watch(keyword, () => {
  ++searchGeneration; candidates.value = []; searched.value = false; searching.value = false; searchError.value = ''
}, { flush: 'sync' })
const search = async () => {
  if (!loaded.value || busy.value || searching.value || !keyword.value.trim()) return
  const request = ++searchGeneration
  const file = generation
  candidates.value = []; searchError.value = ''; searched.value = false; searching.value = true
  try {
    const result = await core.fileMemberCandidates(props.fileId, keyword.value.trim(), 20)
    if (file === generation && request === searchGeneration) { candidates.value = result ?? []; searched.value = true }
  } catch (e) { if (file === generation && request === searchGeneration) searchError.value = message(e) }
  finally { if (file === generation && request === searchGeneration) searching.value = false }
}
const add = (candidate: FileMemberCandidate) => {
  if (!loaded.value || busy.value || !candidate.grantable || self(candidate) || rows.value.some(r => key(r) === key(candidate))) return
  rows.value.push({ ...candidate, role: 'read' })
}
const requestRemove = async (row: FileMemberRole, event: Event) => {
  if (!loaded.value || busy.value || self(row)) return
  removeTrigger = event.currentTarget as HTMLElement
  pendingRemove.value = row
  await nextTick(); cancelRemove.value?.focus()
}
const finishRemove = async (accepted: boolean) => {
  const row = pendingRemove.value
  if (busy.value || !row) return
  if (accepted) rows.value = rows.value.filter(r => key(r) !== key(row))
  pendingRemove.value = null
  await nextTick()
  if (accepted) saveButton.value?.focus()
  else removeTrigger?.focus()
}
const save = async () => {
  if (busy.value || loading.value || !loaded.value || !props.fileId || pendingRemove.value) return
  const request = generation
  const fileId = props.fileId
  const submitted = payload()
  busy.value = true; saveError.value = ''; saved.value = false
  try {
    await core.fileMemberReplace(fileId, submitted)
    if (request === generation) { baseline.value = JSON.stringify(submitted); saved.value = true; emit('saved') }
  } catch (e) { if (request === generation) saveError.value = message(e) }
  finally { if (request === generation) busy.value = false }
}
</script>
<template>
  <section class="file-member-editor" :aria-busy="loading || busy">
    <h3>{{ fileName }} · 成员管理</h3>
    <p class="setting-hint">下方是文件的显式授权。账号与部门授权可能同时生效；移除一行不代表失去全部访问权。自己的用户行只读，实际权限由控制面裁决。</p>
    <div role="alert" aria-atomic="true"><Transition v-bind="presenceMotion" name="es-fade"><p v-if="loadError" class="detail-rename-error">成员读取失败：{{ loadError }} <button class="text-button" type="button" @click="load">重试读取</button></p></Transition></div>
    <Transition v-bind="presenceMotion" name="es-content">
      <p v-if="loading" key="loading" role="status">正在读取成员…</p>
      <div v-else-if="loaded" key="editor" class="file-member-content" v-content-motion="rows.map(key).join('|') + ':' + candidates.map(key).join('|')">
        <div v-if="!rows.length" class="admin-empty">{{ dirty ? '待保存：将移除其他显式成员授权。' : '当前没有显式成员行。' }} 空间默认规则、上传者及部门授权仍可能生效。</div>
        <div v-for="row in rows" :key="key(row)" class="role-row">
          <div class="role-main"><strong>{{ row.memberName || '#' + row.memberId }}</strong><span class="role-tag">{{ row.memberType === 'user' ? '账号显式授权' : '部门显式授权（成员继承）' }}{{ self(row) ? ' · 自己（只读）' : '' }}</span><span v-if="row.grantable === false" class="role-meta">已停用或不在授权范围，可保留或移除</span></div>
          <div class="role-actions">
            <select :aria-label="(row.memberName || row.memberId) + '的角色'" :disabled="busy || !!pendingRemove || self(row) || row.grantable === false" v-model="row.role"><option v-for="(label, role) in labels" :key="role" :value="role">{{ label }}</option></select>
            <button data-testid="remove-member" class="secondary-button compact" type="button" :disabled="busy || !!pendingRemove || self(row)" @click="requestRemove(row, $event)">移除</button>
          </div>
        </div>
        <Transition v-bind="presenceMotion" name="es-disclosure">
          <div v-if="pendingRemove" class="admin-message" role="group" aria-label="确认移除成员" @keydown.esc.stop="finishRemove(false)">
            <p>移除「{{ pendingRemove.memberName || pendingRemove.memberId }}」的显式授权？此改动在保存后生效，其他授权可能继续有效。</p>
            <button ref="cancelRemove" class="secondary-button compact" type="button" @click="finishRemove(false)">保留成员</button>
            <button data-testid="confirm-remove-member" class="secondary-button compact destructive-text" type="button" @click="finishRemove(true)">确认移除</button>
          </div>
        </Transition>
        <form class="file-member-search" @submit.prevent="search">
          <input v-model="keyword" class="setting-input" aria-label="搜索成员或部门" placeholder="输入姓名、账号或部门关键词" maxlength="64" :disabled="busy || !!pendingRemove">
          <button class="secondary-button compact" type="submit" v-content-motion="searching" :disabled="busy || !!pendingRemove || searching || !keyword.trim()">{{ searching ? '搜索中…' : '搜索' }}</button>
        </form>
        <div role="alert" aria-atomic="true"><Transition v-bind="presenceMotion" name="es-fade"><p v-if="searchError" class="detail-rename-error">搜索失败：{{ searchError }}，可重新搜索。</p></Transition></div>
        <div role="status" aria-atomic="true"><Transition v-bind="presenceMotion" name="es-fade"><p v-if="searched && !searching && !candidates.length" class="setting-hint">没有匹配的有效成员或部门，请换一个关键词。</p></Transition></div>
        <div v-for="candidate in candidates" :key="key(candidate)" class="role-row">
          <span>{{ candidate.memberName }} · {{ candidate.memberType === 'user' ? '账号' : '部门' }}</span>
          <button class="secondary-button compact" type="button" :disabled="busy || !!pendingRemove || !candidate.grantable || self(candidate) || rows.some(r => key(r) === key(candidate))" @click="add(candidate)">添加成员</button>
        </div>
        <div role="alert" aria-atomic="true"><Transition v-bind="presenceMotion" name="es-fade"><p v-if="saveError" class="detail-rename-error">保存失败：{{ saveError }}。草稿已保留，请重试保存。</p></Transition></div>
        <p role="status" v-content-motion="busy + ':' + saved + ':' + dirty">{{ busy ? '正在保存成员角色…' : saved ? '成员角色已保存' : dirty ? '有未保存的成员变更' : '修改后点击保存生效' }}</p>
        <button ref="saveButton" data-testid="save-members" class="primary-button compact" type="button" :disabled="busy || !!pendingRemove" @click="save">{{ busy ? '保存中…' : '保存成员角色' }}</button>
      </div>
    </Transition>
  </section>
</template>
