<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { core } from '../services/core'
import type { FileMemberRole, FileMemberCandidate } from '../types/core'
const props = defineProps<{ fileId: string; fileName: string; operatorId: string }>()
const emit = defineEmits<{ saved: [] }>()
const rows = ref<FileMemberRole[]>([])
const candidates = ref<FileMemberCandidate[]>([])
const keyword = ref('')
const loading = ref(false)
const busy = ref(false)
const searching = ref(false)
const searched = ref(false)
const error = ref('')
const loaded = ref(false)
let generation = 0
let searchGeneration = 0
onBeforeUnmount(() => { ++generation; ++searchGeneration })
const labels = { manage: '可管理', edit: '可编辑', download: '可查看下载', read: '仅可查看' }
const self = (row: FileMemberRole) => row.memberType === 'user' && row.memberId === props.operatorId
const message = (e: unknown) => e instanceof Error ? e.message : String(e)
const load = async () => {
  const request = ++generation
  ++searchGeneration
  rows.value = []; candidates.value = []; keyword.value = ''; error.value = ''; busy.value = false; searching.value = false
  loaded.value = false
  searched.value = false
  loading.value = !!props.fileId
  if (!props.fileId) return
  try {
    const result = await core.fileMemberList(props.fileId)
    if (request === generation) { rows.value = result ?? []; loaded.value = true }
  } catch (e) { if (request === generation) error.value = message(e) }
  finally { if (request === generation) loading.value = false }
}
watch(() => `${props.fileId}:${props.operatorId}`, load, { immediate: true })
const search = async () => {
  const request = ++searchGeneration
  const file = generation
  candidates.value = []; error.value = ''; searched.value = false
  if (!keyword.value.trim()) return
  searching.value = true
  try {
    const result = await core.fileMemberCandidates(props.fileId, keyword.value.trim(), 20)
    if (file === generation && request === searchGeneration) { candidates.value = result; searched.value = true }
  } catch (e) { if (file === generation && request === searchGeneration) error.value = message(e) }
  finally { if (file === generation && request === searchGeneration) searching.value = false }
}
const add = (candidate: FileMemberCandidate) => {
  if (!candidate.grantable || self(candidate as FileMemberRole) || rows.value.some(r => r.memberType === candidate.memberType && r.memberId === candidate.memberId)) return
  rows.value.push({ ...candidate, role: 'read' })
}
const save = async () => {
  if (busy.value || loading.value || !props.fileId) return
  const request = generation
  const fileId = props.fileId
  busy.value = true; error.value = ''
  try {
    await core.fileMemberReplace(fileId, rows.value.filter(r => !self(r)).map(r => ({ memberType: r.memberType, memberId: r.memberId, role: r.role })))
    if (request === generation) emit('saved')
  } catch (e) { if (request === generation) error.value = message(e) }
  finally { if (request === generation) busy.value = false }
}
</script>
<template>
  <section class="file-member-editor" :aria-busy="loading || busy">
    <h3>{{ fileName }} · 成员管理</h3>
    <p class="setting-hint">只能授权同一共享空间的有效成员与部门。自己的用户行只读；移除全部其他行后仍可保存。</p>
    <p v-if="error" class="detail-rename-error" role="alert">{{ error }}<button v-if="!rows.length" class="text-button" type="button" :disabled="busy" @click="load">重试读取</button></p>
    <p v-if="loading">正在读取成员…</p>
    <template v-else>
      <div v-if="!rows.length" class="admin-empty">没有显式成员行，按空间默认权限生效。</div>
      <div v-for="row in rows" :key="row.memberType + ':' + row.memberId" class="role-row">
        <div class="role-main"><strong>{{ row.memberName || `#${row.memberId}` }}</strong><span class="role-tag">{{ row.memberType === 'user' ? '账号' : '部门' }}{{ self(row) ? ' · 自己（只读）' : '' }}</span><span v-if="row.grantable === false" class="role-meta">已停用或不在授权范围，可保留或移除</span></div>
        <div class="role-actions">
          <select :aria-label="`${row.memberName || row.memberId}的角色`" :disabled="busy || self(row) || row.grantable === false" v-model="row.role"><option v-for="(label, role) in labels" :key="role" :value="role">{{ label }}</option></select>
          <button data-testid="remove-member" class="secondary-button compact" type="button" :disabled="busy || self(row)" @click="rows = rows.filter(r => r !== row)">移除</button>
        </div>
      </div>
      <form class="file-member-search" @submit.prevent="search">
        <input v-model="keyword" class="setting-input" aria-label="搜索成员或部门" placeholder="输入姓名、账号或部门关键词" maxlength="64" :disabled="busy">
        <button class="secondary-button compact" type="submit" :disabled="busy || searching || !keyword.trim()">{{ searching ? '搜索中…' : '搜索' }}</button>
      </form>
      <p v-if="searched && !searching && !candidates.length && !error" class="setting-hint" role="status">没有匹配的有效成员或部门，请换一个关键词。</p>
      <div v-for="candidate in candidates" :key="candidate.memberType + candidate.memberId" class="role-row">
        <span>{{ candidate.memberName }} · {{ candidate.memberType === 'user' ? '账号' : '部门' }}</span>
        <button class="secondary-button compact" type="button" :disabled="busy || !candidate.grantable || self(candidate as FileMemberRole) || rows.some(r => r.memberType === candidate.memberType && r.memberId === candidate.memberId)" @click="add(candidate)">添加成员</button>
      </div>
      <button data-testid="save-members" class="primary-button compact" type="button" :disabled="busy || loading || !loaded" @click="save">{{ busy ? '保存中…' : '保存成员角色' }}</button>
    </template>
  </section>
</template>
