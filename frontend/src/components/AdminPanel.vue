<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { core } from '../services/core'
import { QUOTA_UNLIMITED, QUOTA_UNSET, type Capacity, type ManagedUser, type Space } from '../types/core'

type Tab = 'accounts' | 'spaces'

const tab = ref<Tab>('accounts')
const users = ref<ManagedUser[]>([])
const total = ref(0)
const loading = ref(false)
const busyId = ref('')
const message = ref('')
const messageKind = ref<'ok' | 'error'>('ok')
const registerEnabled = ref(false)
const registerBusy = ref(false)

const notify = (text: string, kind: 'ok' | 'error' = 'ok') => {
  message.value = text
  messageKind.value = kind
  if (kind === 'ok') setTimeout(() => { if (message.value === text) message.value = '' }, 2600)
}
const fail = (e: unknown) => notify(e instanceof Error ? e.message : String(e), 'error')

// ═══ 空间 ═══
const spaces = ref<Space[]>([])
const sharedMembers = ref<Record<string, string>>({})
const spacesLoading = ref(false)
// 每个账号的配额输入框各自的草稿值（GB），键是 userId；共享空间用 'shared'。
// 值可能是 number：Vue 的 v-model 对 <input type="number"> 会自动做数字转换，
// 清空输入框时又会给回空串，所以两种类型都要接。
const quotaDraft = ref<Record<string, string | number>>({})
const GB = 1024 * 1024 * 1024

const sharedSpace = computed(() => spaces.value.find(s => s.spaceType === 'shared') ?? null)
// 按 ownerId 索引个人空间，账号列表据此显示各自的配额与用量
const personalByOwner = computed(() => {
  const map: Record<string, Space> = {}
  for (const space of spaces.value) {
    if (space.spaceType === 'personal') map[space.ownerId] = space
  }
  return map
})

const formatBytes = (bytes: number) => {
  if (bytes === QUOTA_UNLIMITED) return '不限'
  if (bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${unit === 0 ? value : value.toFixed(2)} ${units[unit]}`
}

// 已用百分比。不限容量时不画进度条，返回 null
const usedPercent = (space: Space | null) => {
  if (!space || space.quotaBytes <= 0) return null
  return Math.min(100, Math.round(space.usedBytes / space.quotaBytes * 100))
}

const capacity = ref<Capacity | null>(null)

// 承诺总量是否超过池容量。超配本身允许（多数账号用不满，禁止会浪费容量），
// 但必须让管理员看见——否则用户会看到「配额还剩很多」却传不上去。
const overcommitted = computed(() => {
  const c = capacity.value
  return !!c && c.enabled && c.poolBytes >= 0 && c.committedBytes > c.poolBytes
})

const loadSpaces = async () => {
  spacesLoading.value = true
  try {
    const [list, members, cap] = await Promise.all([
      core.adminListSpaces(),
      core.adminSharedMembers(),
      core.adminCapacity(),
    ])
    spaces.value = list ?? []
    sharedMembers.value = members ?? {}
    capacity.value = cap ?? null
    // 草稿值以服务端为准重置，避免上次没提交的输入残留成假象
    const draft: Record<string, string> = {}
    for (const space of spaces.value) {
      const key = space.spaceType === 'shared' ? 'shared' : space.ownerId
      draft[key] = space.quotaBytes > 0 ? String(+(space.quotaBytes / GB).toFixed(2)) : ''
    }
    quotaDraft.value = draft
  } catch (e) {
    fail(e)
  } finally {
    spacesLoading.value = false
  }
}

// 把输入框的 GB 值换成字节数。空按 0（收回配额），非法值返回 null 由调用方拒绝。
const draftToBytes = (raw: string | number | undefined): number | null => {
  if (raw === undefined || raw === null || raw === '') return QUOTA_UNSET
  const value = typeof raw === 'number' ? raw : Number(String(raw).trim())
  if (!Number.isFinite(value) || value < 0) return null
  return Math.round(value * GB)
}

const saveSharedQuota = async () => {
  const bytes = draftToBytes(quotaDraft.value.shared)
  if (bytes === null) {
    notify('容量请填非负数字', 'error')
    return
  }
  busyId.value = 'shared'
  try {
    await core.adminSetSharedQuota(bytes)
    notify(bytes === QUOTA_UNSET ? '已收回共享空间容量' : `共享空间容量已设为 ${formatBytes(bytes)}`)
    await loadSpaces()
  } catch (e) {
    fail(e)
  } finally {
    busyId.value = ''
  }
}

const savePersonalQuota = async (user: ManagedUser) => {
  const bytes = draftToBytes(quotaDraft.value[user.userId])
  if (bytes === null) {
    notify('容量请填非负数字', 'error')
    return
  }
  busyId.value = user.userId
  try {
    await core.adminSetPersonalQuota(user.userId, bytes)
    notify(bytes === QUOTA_UNSET
      ? `已收回 ${user.nickName || user.userName} 的空间`
      : `${user.nickName || user.userName} 的空间已设为 ${formatBytes(bytes)}`)
    await loadSpaces()
  } catch (e) {
    fail(e)
  } finally {
    busyId.value = ''
  }
}

// 共享空间权限三态轮转：无 → 只读 → 读写 → 无
const cycleSharedPermission = async (user: ManagedUser) => {
  const current = sharedMembers.value[user.userId] ?? ''
  const next = current === '' ? 'read' : current === 'read' ? 'write' : ''
  busyId.value = user.userId
  try {
    await core.adminGrantShared(user.userId, next)
    const label = next === '' ? '已撤销共享空间权限' : next === 'read' ? '已设为只读' : '已设为读写'
    notify(`${user.nickName || user.userName}：${label}`)
    sharedMembers.value = { ...sharedMembers.value, [user.userId]: next }
  } catch (e) {
    fail(e)
  } finally {
    busyId.value = ''
  }
}

const permissionLabel = (userId: string) => {
  const perm = sharedMembers.value[userId] ?? ''
  return perm === 'write' ? '读写' : perm === 'read' ? '只读' : '无权限'
}

const creating = ref(false)
const createOpen = ref(false)
const form = ref({ userName: '', nickName: '', password: '' })

const load = async () => {
  loading.value = true
  try {
    const page = await core.adminListUsers(1, 100)
    users.value = page.rows ?? []
    total.value = page.total ?? 0
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}

const loadRegisterSwitch = async () => {
  try {
    registerEnabled.value = await core.adminRegisterEnabled()
  } catch (e) {
    fail(e)
  }
}

const toggleRegister = async () => {
  if (registerBusy.value) return
  const next = !registerEnabled.value
  registerBusy.value = true
  try {
    await core.adminSetRegisterEnabled(next)
    registerEnabled.value = next
    notify(next ? '已允许用户自助注册' : '已关闭自助注册')
  } catch (e) {
    fail(e)
  } finally {
    registerBusy.value = false
  }
}

const submitCreate = async () => {
  if (creating.value) return
  creating.value = true
  try {
    await core.adminCreateUser(form.value.userName, form.value.nickName, form.value.password)
    notify(`账号 ${form.value.userName} 已创建`)
    form.value = { userName: '', nickName: '', password: '' }
    createOpen.value = false
    await load()
  } catch (e) {
    fail(e)
  } finally {
    creating.value = false
  }
}

const toggleStatus = async (user: ManagedUser) => {
  if (busyId.value) return
  busyId.value = user.userId
  try {
    await core.adminSetUserStatus(user.userId, user.status !== '0')
    await load()
  } catch (e) {
    fail(e)
  } finally {
    busyId.value = ''
  }
}

const removeUser = async (user: ManagedUser) => {
  if (busyId.value) return
  if (!window.confirm(`删除账号「${user.nickName || user.userName}」？该账号的个人空间将无人可访问。`)) return
  busyId.value = user.userId
  try {
    await core.adminDeleteUser(user.userId)
    notify(`账号 ${user.userName} 已删除`)
    await load()
  } catch (e) {
    fail(e)
  } finally {
    busyId.value = ''
  }
}

const canSubmit = computed(() =>
  form.value.userName.trim().length > 0 && form.value.password.length > 0)

const initial = (user: ManagedUser) => {
  const name = user.nickName || user.userName || ''
  return name ? Array.from(name)[0] : '?'
}

onMounted(() => { void load(); void loadRegisterSwitch(); void loadSpaces() })
</script>

<template>
  <div class="admin-panel">
    <header class="workspace-header">
      <div>
        <span class="section-label">管理</span>
        <h1>账号与空间</h1>
        <p>开设账号、分配空间大小，控制共享空间的访问权限。</p>
      </div>
      <div class="admin-tabs" role="tablist">
        <button
          :class="['admin-tab', tab === 'accounts' ? 'active' : '']"
          type="button" role="tab" :aria-selected="tab === 'accounts'"
          @click="tab = 'accounts'"
        >账号</button>
        <button
          :class="['admin-tab', tab === 'spaces' ? 'active' : '']"
          type="button" role="tab" :aria-selected="tab === 'spaces'"
          @click="tab = 'spaces'"
        >空间</button>
      </div>
    </header>

    <div v-if="message" :class="['admin-message', messageKind]" role="status">
      <span>{{ message }}</span>
      <button type="button" aria-label="关闭提示" @click="message = ''">×</button>
    </div>

    <!-- ═══ 账号 ═══ -->
    <template v-if="tab === 'accounts'">
      <section class="card">
        <header class="card-header">
          <div>
            <span class="section-label">成员</span>
            <h2>{{ total }} 个账号</h2>
          </div>
          <div class="card-actions">
            <button class="secondary-button compact" type="button" :disabled="loading" @click="load">
              <svg viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/></svg>
              刷新
            </button>
            <button class="primary-button compact" type="button" @click="createOpen = !createOpen">
              <svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>
              新建账号
            </button>
          </div>
        </header>

        <!-- 新建表单：就地展开，不弹窗，避免遮住列表 -->
        <form v-if="createOpen" class="admin-create" @submit.prevent="submitCreate">
          <label class="admin-field">
            <span>账号名</span>
            <input v-model="form.userName" class="setting-input" autocomplete="off" placeholder="登录用的账号" />
          </label>
          <label class="admin-field">
            <span>昵称</span>
            <input v-model="form.nickName" class="setting-input" autocomplete="off" placeholder="留空则同账号名" />
          </label>
          <label class="admin-field">
            <span>初始密码</span>
            <input v-model="form.password" class="setting-input" type="password" autocomplete="new-password" placeholder="告知该成员后请其自行修改" />
          </label>
          <div class="admin-create-actions">
            <button class="secondary-button compact" type="button" @click="createOpen = false">取消</button>
            <button class="primary-button compact" type="submit" :disabled="!canSubmit || creating">
              {{ creating ? '创建中…' : '创建' }}
            </button>
          </div>
        </form>

        <div v-if="loading && !users.length" class="admin-empty">正在读取账号…</div>
        <ul v-else class="admin-list">
          <li v-for="user in users" :key="user.userId" class="admin-row">
            <span class="admin-avatar">{{ initial(user) }}</span>
            <div class="admin-identity">
              <strong>{{ user.nickName || user.userName }}</strong>
              <span>{{ user.userName }}<template v-if="user.deptName"> · {{ user.deptName }}</template></span>
            </div>
            <!--
              空间列。此前这里是硬编码的「待开空间」占位符，导致已分配配额的账号
              也一直显示未开通——数据本身是对的，只是这一格没接上。
            -->
            <span
              v-if="personalByOwner[user.userId] && personalByOwner[user.userId].quotaBytes !== QUOTA_UNSET"
              class="admin-space-cell granted"
            >{{ formatBytes(personalByOwner[user.userId].usedBytes) }} / {{ formatBytes(personalByOwner[user.userId].quotaBytes) }}</span>
            <span v-else class="admin-space-cell pending">待开空间</span>
            <span :class="['admin-status', user.status === '0' ? 'on' : 'off']">
              {{ user.status === '0' ? '正常' : '已停用' }}
            </span>
            <div class="admin-row-actions">
              <button class="secondary-button compact" type="button" :disabled="busyId === user.userId" @click="toggleStatus(user)">
                {{ user.status === '0' ? '停用' : '启用' }}
              </button>
              <button class="secondary-button compact danger" type="button" :disabled="busyId === user.userId" @click="removeUser(user)">
                删除
              </button>
            </div>
          </li>
        </ul>
      </section>

      <section class="card admin-switch-card">
        <div class="admin-switch-copy">
          <strong>允许用户自助注册</strong>
          <span>关闭后只能由管理员在此页面开设账号。</span>
        </div>
        <button
          :class="['admin-toggle', registerEnabled ? 'on' : '']"
          type="button" role="switch" :aria-checked="registerEnabled"
          :disabled="registerBusy" @click="toggleRegister"
        >
          <i />
        </button>
      </section>
    </template>

    <!-- ═══ 空间 ═══ -->
    <template v-else>
      <!--
        容量总览。逐空间配额看不出「承诺总量是否超过物理磁盘」——
        没有这一行，管理员会在不知情下超配，用户则看到「还剩很多」却传不上去。
      -->
      <section v-if="capacity" class="card capacity-card">
        <div class="capacity-row">
          <div class="capacity-item">
            <span>物理可用</span>
            <strong>{{ capacity.enabled ? formatBytes(capacity.usableBytes) : '未探测' }}</strong>
          </div>
          <div class="capacity-item">
            <span>可分配（扣预留 {{ formatBytes(capacity.reservedBytes) }}）</span>
            <strong>{{ capacity.enabled ? formatBytes(capacity.poolBytes) : '—' }}</strong>
          </div>
          <div class="capacity-item">
            <span>已承诺</span>
            <strong :class="overcommitted ? 'over' : ''">{{ formatBytes(capacity.committedBytes) }}</strong>
          </div>
          <div class="capacity-item">
            <span>实际已用</span>
            <strong>{{ formatBytes(capacity.usedBytes) }}</strong>
          </div>
        </div>
        <p v-if="!capacity.enabled" class="capacity-note">
          控制面未配置容量探测路径（<code>easyshare.drive.capacity-path</code>），
          池上限不生效：可以分配出超过磁盘实际容量的配额。
        </p>
        <p v-else-if="overcommitted" class="capacity-note warn">
          已承诺容量超过可分配容量。多数账号用不满时这样做是可行的，
          但一旦实际用量触到物理上限，上传会被拒并提示「服务器存储不足」。
        </p>
        <p v-if="capacity.unlimitedCount > 0" class="capacity-note">
          有 {{ capacity.unlimitedCount }} 个空间配额为「不限」，未计入已承诺——该数字偏小。
        </p>
      </section>

      <section class="card">
        <header class="card-header">
          <div>
            <span class="section-label">共享空间</span>
            <h2>全员可见的公共空间</h2>
          </div>
          <div class="card-actions">
            <button class="secondary-button compact" type="button" :disabled="spacesLoading" @click="loadSpaces">
              <svg viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/></svg>
              刷新
            </button>
          </div>
        </header>
        <div class="admin-space-form">
          <label class="admin-field">
            <span>空间容量（GB，留空表示收回）</span>
            <div class="admin-quota-input">
              <input
                v-model="quotaDraft.shared"
                class="setting-input" type="number" min="0" step="0.5" placeholder="未分配"
                @keyup.enter="saveSharedQuota"
              />
              <button
                class="primary-button compact" type="button"
                :disabled="busyId === 'shared'" @click="saveSharedQuota"
              >{{ busyId === 'shared' ? '保存中…' : '保存' }}</button>
            </div>
          </label>

          <!-- 用量条：已用量是控制面实时聚合对象存储得出的，不是库里的镜像值 -->
          <div v-if="sharedSpace" class="admin-usage">
            <div class="admin-usage-head">
              <span>已用 {{ formatBytes(sharedSpace.usedBytes) }} / {{ sharedSpace.quotaBytes === QUOTA_UNSET ? '未分配' : formatBytes(sharedSpace.quotaBytes) }}</span>
              <b v-if="usedPercent(sharedSpace) !== null">{{ usedPercent(sharedSpace) }}%</b>
            </div>
            <div v-if="usedPercent(sharedSpace) !== null" class="mini-track">
              <i :style="{ width: `${usedPercent(sharedSpace)}%` }" />
            </div>
          </div>

          <p class="setting-hint">共享空间的读写权限在下方逐账号授予，未授权的账号看不到这个空间。</p>
        </div>
      </section>

      <section class="card">
        <header class="card-header">
          <div>
            <span class="section-label">个人空间</span>
            <h2>逐账号分配</h2>
          </div>
        </header>
        <div v-if="spacesLoading && !spaces.length" class="admin-empty">正在读取空间…</div>
        <ul v-else class="admin-list">
          <li v-for="user in users" :key="user.userId" class="admin-row space-row">
            <span class="admin-avatar">{{ initial(user) }}</span>
            <div class="admin-identity">
              <strong>{{ user.nickName || user.userName }}</strong>
              <span>{{ user.userName }}</span>
            </div>

            <div class="admin-quota-input compact">
              <input
                v-model="quotaDraft[user.userId]"
                class="setting-input" type="number" min="0" step="0.5" placeholder="未分配"
                @keyup.enter="savePersonalQuota(user)"
              />
              <span class="admin-unit-label">GB</span>
            </div>

            <div class="admin-usage compact">
              <template v-if="personalByOwner[user.userId] && personalByOwner[user.userId].quotaBytes !== QUOTA_UNSET">
                <div class="admin-usage-head">
                  <span>{{ formatBytes(personalByOwner[user.userId].usedBytes) }} / {{ formatBytes(personalByOwner[user.userId].quotaBytes) }}</span>
                </div>
                <div v-if="usedPercent(personalByOwner[user.userId]) !== null" class="mini-track">
                  <i :style="{ width: `${usedPercent(personalByOwner[user.userId])}%` }" />
                </div>
              </template>
              <span v-else class="admin-space-cell pending">待开空间</span>
            </div>

            <!-- 共享空间权限：点击轮转 无 → 只读 → 读写 → 无 -->
            <button
              :class="['admin-perm', sharedMembers[user.userId] || 'none']"
              type="button" :disabled="busyId === user.userId"
              :title="`共享空间：${permissionLabel(user.userId)}，点击切换`"
              @click="cycleSharedPermission(user)"
            >{{ permissionLabel(user.userId) }}</button>

            <div class="admin-row-actions">
              <button
                class="primary-button compact" type="button"
                :disabled="busyId === user.userId" @click="savePersonalQuota(user)"
              >{{ busyId === user.userId ? '保存中…' : '保存' }}</button>
            </div>
          </li>
        </ul>
        <p class="setting-hint">
          容量留空即收回，账号在客户端会显示为「待开空间」。配额在控制面签发上传授权时校验。
        </p>
      </section>
    </template>
  </div>
</template>
