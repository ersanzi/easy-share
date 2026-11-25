<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { core } from '../services/core'
import { presenceMotion, vContentMotion } from '../directives/contentMotion'
import FileMemberEditor from './FileMemberEditor.vue'
import { useConfirm } from '../composables/useConfirm'
import { ADMIN_SCOPE_LABELS, QUOTA_UNLIMITED, QUOTA_UNSET, type AdminDept, type AdminMember, type AdminRole, type AdminSharedMember, type AuthUser, type Capacity, type FeatureFlags, type KnowledgeStats, type ManagedUser, type SharedFileEntry, type Space } from '../types/core'

type Tab = 'overview' | 'accounts' | 'spaces' | 'permissions'

const tab = ref<Tab>('accounts')
let disposed = false
let spacesRevision = 0, permissionRevision = 0, rolesRevision = 0, filesRevision = 0, usersRevision = 0, deptRevision = 0
let noticeTimer: ReturnType<typeof setTimeout> | undefined
onBeforeUnmount(() => { disposed = true; clearTimeout(noticeTimer) })
const identityLoading = ref(true)
const identityError = ref('')
// 管理范围来自控制面：超管（RuoYi superadmin）看全部标签页；
// 子管理员（任一管理范围档位）只有「权限」页，成员由服务端按范围档位裁剪。
const currentUser = ref<AuthUser | null>(null)
const isSuper = computed(() => currentUser.value?.isAdmin === true)
const users = ref<ManagedUser[]>([])
const total = ref(0)
const loading = ref(false)
const busyId = ref('')
const { confirm } = useConfirm()
const message = ref('')
const messageKind = ref<'ok' | 'error'>('ok')
const registerEnabled = ref(false)
const registerBusy = ref(false)
const registerReady = ref(false)

const notify = (text: string, kind: 'ok' | 'error' = 'ok') => {
  if (disposed) return
  clearTimeout(noticeTimer)
  message.value = text
  messageKind.value = kind
  if (kind === 'ok') noticeTimer = setTimeout(() => { message.value = '' }, 5000)
}
const fail = (e: unknown) => notify(e instanceof Error ? e.message : String(e), 'error')

// ═══ 空间 ═══
const spaces = ref<Space[]>([])
const knowledgeStats = ref<KnowledgeStats | null>(null)
const userTotal = ref<number | null>(null)
const overviewError = ref('')
let overviewRevision = 0
const overviewLoading = ref(false)
const sharedMembers = ref<AdminSharedMember[]>([])
const depts = ref<AdminDept[]>([])
const deptForm = ref<{ deptId: string; permission: string }>({ deptId: '', permission: 'read' })
const spacesLoading = ref(false)
const spacesReady = ref(false)
const spacesError = ref('')
const deptLoading = ref(false)
const deptReady = ref(false)
const deptError = ref('')
const sharedPermissionDraft = ref<Record<string, string>>({})
const spacesLocked = computed(() => !!busyId.value || deptBusy.value || spacesLoading.value || !spacesReady.value)
const loadDepts = async () => {
  const request = ++deptRevision
  deptLoading.value = true; deptReady.value = false; deptError.value = ''
  try {
    const list = await core.adminListDepts()
    if (disposed || request !== deptRevision) return
    depts.value = list ?? []; deptReady.value = true
  } catch (e) { if (!disposed && request === deptRevision) deptError.value = e instanceof Error ? e.message : String(e) }
  finally { if (!disposed && request === deptRevision) deptLoading.value = false }
}

// ═══ 文件访问角色（企业权限第三片）═══
// 按 fileId 给账号/部门配四档角色；整组替换保存。
// 护栏在服务端（不能修改自己的角色、仅 manage 档可授权），前端只做可用的编辑器。
const sharedFiles = ref<SharedFileEntry[]>([])
const memberFileId = ref('')
const memberFileBusy = ref(false)
const filesLoading = ref(false)
const filesReady = ref(false)
const filesError = ref('')

const loadSharedFiles = async () => {
  if (memberFileBusy.value) return
  const request = ++filesRevision
  filesLoading.value = true; filesReady.value = false; filesError.value = ''
  try {
    const list = await core.sharedFileList()
    if (disposed || request !== filesRevision) return
    sharedFiles.value = list ?? []; filesReady.value = true
    if (!sharedFiles.value.some(f => f.fileId === memberFileId.value)) memberFileId.value = ''
  } catch (e) { if (!disposed && request === filesRevision) filesError.value = e instanceof Error ? e.message : String(e) }
  finally { if (!disposed && request === filesRevision) filesLoading.value = false }
}

const memberFileName = computed(() =>
  sharedFiles.value.find(f => f.fileId === memberFileId.value)?.name ?? '')
const pickMemberFile = (fileId: string) => { if (!memberFileBusy.value && filesReady.value) memberFileId.value = fileId }
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

const loadOverview = async () => {
  const request = ++overviewRevision
  overviewLoading.value = true; overviewError.value = ''
  try {
    const [stats, users] = await Promise.all([
      core.adminKnowledgeStats(30),
      core.adminListUsers(1, 1),
    ])
    if (disposed || request !== overviewRevision) return
    knowledgeStats.value = stats
    userTotal.value = users?.total ?? null
  } catch (e) {
    if (!disposed && request === overviewRevision) { overviewError.value = e instanceof Error ? e.message : String(e); knowledgeStats.value = null; userTotal.value = null }
  } finally {
    if (!disposed && request === overviewRevision) overviewLoading.value = false
  }
}

const loadSpaces = async () => {
  const request = ++spacesRevision
  spacesLoading.value = true; spacesReady.value = false; spacesError.value = ''
  // 文件访问角色卡的文件列表并行刷（失败不阻塞空间页其余数据）
  void loadSharedFiles()
  try {
    const [list, members, cap] = await Promise.all([
      core.adminListSpaces(),
      core.adminSharedMembers(),
      core.adminCapacity(),
    ])
    if (disposed || request !== spacesRevision) return false
    spaces.value = list ?? []
    sharedMembers.value = members ?? []
    capacity.value = cap ?? null
    spacesReady.value = true
    sharedPermissionDraft.value = Object.fromEntries(sharedMembers.value.filter(m => m.memberType === 'user').map(m => [m.memberId, m.permission]))
    // 草稿值以服务端为准重置，避免上次没提交的输入残留成假象
    const draft: Record<string, string> = {}
    for (const space of spaces.value) {
      const key = space.spaceType === 'shared' ? 'shared' : space.ownerId
      draft[key] = space.quotaBytes > 0 ? String(+(space.quotaBytes / GB).toFixed(2)) : ''
    }
    quotaDraft.value = draft
    return true
  } catch (e) {
    if (!disposed && request === spacesRevision) { spacesError.value = e instanceof Error ? e.message : String(e); fail(e) }
    return false
  } finally {
    if (!disposed && request === spacesRevision) spacesLoading.value = false
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
  if (spacesLocked.value) return
  const bytes = draftToBytes(quotaDraft.value.shared)
  if (bytes === null) {
    notify('容量请填非负数字', 'error')
    return
  }
  busyId.value = 'shared'; message.value = ''
  try {
    await core.adminSetSharedQuota(bytes)
    if (disposed) return
    notify(bytes === QUOTA_UNSET ? '已收回共享空间容量' : `共享空间容量已设为 ${formatBytes(bytes)}`)
    await loadSpaces()
  } catch (e) {
    fail(e)
  } finally {
    busyId.value = ''
  }
}

const savePersonalQuota = async (user: ManagedUser) => {
  if (spacesLocked.value) return
  const bytes = draftToBytes(quotaDraft.value[user.userId])
  if (bytes === null) {
    notify('容量请填非负数字', 'error')
    return
  }
  busyId.value = user.userId; message.value = ''
  try {
    await core.adminSetPersonalQuota(user.userId, bytes)
    if (disposed) return
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

// 某账号的显式授权行（不含部门继承——生效规则是个人行与部门行取宽，见控制面 SpaceService）
const userMemberRow = (userId: string) => sharedMembers.value.find(m => m.memberType === 'user' && m.memberId === userId)
const deptMemberRows = computed(() => sharedMembers.value.filter(m => m.memberType === 'dept'))

// 显式选择后保存；撤销仅移除个人行，部门继承仍可能生效。
const saveSharedPermission = async (user: ManagedUser) => {
  if (spacesLocked.value) return
  const previous = userMemberRow(user.userId)?.permission ?? ''
  const next = sharedPermissionDraft.value[user.userId] ?? previous
  if (!['', 'read', 'write'].includes(next) || next === previous) return
  busyId.value = user.userId
  try {
    if (next === '' && !await confirm({ title: '撤销个人授权', message: '撤销「' + (user.nickName || user.userName) + '」的个人显式授权？部门继承可能继续有效。', confirmText: '撤销授权', danger: true })) return
    if (disposed) return
    await core.adminGrantShared('user', user.userId, next)
    if (disposed) return
    const refreshed = await loadSpaces()
    if (refreshed) notify('个人显式授权已保存；实际权限还需结合部门继承与功能开关。')
    else fail('授权已保存，但读取最新权限失败，请刷新后继续。')
  } catch (e) { fail(e) }
  finally { busyId.value = '' }
}
const permissionLabel = (userId: string) => {
  if (!spacesReady.value) return '尚未读取'
  const perm = userMemberRow(userId)?.permission ?? ''
  return perm === 'write' ? '读写' : perm === 'read' ? '只读' : '未单独授权'
}

// ── 部门授权（片 1）：整部门生效，个人显式授权与其取宽 ──
const deptBusy = ref(false)
const addDeptAuth = async () => {
  if (spacesLocked.value || !deptReady.value) return
  if (!deptForm.value.deptId) {
    notify('请选择部门', 'error')
    return
  }
  const { deptId, permission } = deptForm.value
  deptBusy.value = true; message.value = ''
  try {
    await core.adminGrantShared('dept', deptId, permission)
    if (disposed) return
    deptForm.value = { deptId: '', permission: 'read' }
    if (await loadSpaces()) notify(permission === 'write' ? '部门显式授权已设为读写' : '部门显式授权已设为只读')
    else fail('部门授权已保存，但最新权限读取失败，请刷新。')
  } catch (e) {
    fail(e)
  } finally {
    deptBusy.value = false
  }
}
const revokeDeptAuth = async (row: AdminSharedMember) => {
  if (spacesLocked.value) return
  deptBusy.value = true
  try {
    if (!await confirm({ title: '撤销部门授权', message: '撤销部门「' + (row.name || row.memberId) + '」的授权？个人显式授权可能继续有效。', confirmText: '撤销授权', danger: true }) || disposed) return
    await core.adminGrantShared('dept', row.memberId, '')
    if (disposed) return
    if (await loadSpaces()) notify('部门显式授权已撤销，个人授权可能继续有效。')
    else fail('部门授权已撤销，但最新权限读取失败，请刷新。')
  } catch (e) {
    fail(e)
  } finally {
    deptBusy.value = false
  }
}
const onDeptChange = (event: Event) => {
  deptForm.value.deptId = (event.target as HTMLSelectElement).value
}
const onDeptPermChange = (event: Event) => {
  deptForm.value.permission = (event.target as HTMLSelectElement).value
}

const creating = ref(false)
const createOpen = ref(false)
const form = ref({ userName: '', nickName: '', password: '' })

const load = async () => {
  void loadOverview()
  const request = ++usersRevision
  loading.value = true
  try {
    const page = await core.adminListUsers(1, 100)
    if (disposed || request !== usersRevision) return
    users.value = page.rows ?? []
    total.value = page.total ?? 0
  } catch (e) {
    fail(e)
  } finally {
    if (!disposed && request === usersRevision) loading.value = false
  }
}

const loadRegisterSwitch = async () => {
  try {
    const enabled = await core.adminRegisterEnabled()
    if (disposed) return
    registerEnabled.value = enabled; registerReady.value = true
  } catch (e) {
    fail(e)
  }
}

const toggleRegister = async () => {
  if (registerBusy.value || !registerReady.value) return
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

// 服务配置（租户服务发现）：管理员在这里登记知识服务地址，同事端登录后自动拿到，
// 免手填。来源 derived 表示还没登记、展示的是同主机推导的默认值。
const serviceKnowledgeUrl = ref('')
const serviceSource = ref<'' | 'registry' | 'derived'>('')
const serviceBusy = ref(false)
const serviceReady = ref(false)

const loadServiceConfig = async () => {
  try {
    const endpoints = await core.serviceEndpoints()
    if (disposed) return
    serviceReady.value = true
    serviceKnowledgeUrl.value = endpoints.knowledgeUrl
    serviceSource.value = endpoints.knowledgeUrlSource
  } catch (e) {
    fail(e)
  }
}

const submitServiceConfig = async () => {
  if (serviceBusy.value || !serviceReady.value) return
  serviceBusy.value = true
  try {
    await core.saveServiceConfig(serviceKnowledgeUrl.value.trim())
    notify('服务配置已保存，同事端登录后自动生效')
    await loadServiceConfig()
  } catch (e) {
    fail(e)
  } finally {
    serviceBusy.value = false
  }
}

// AI 能力配置（管理端配置化）：LLM 与听记开关，控制面 sys_config 为权威，
// 保存后控制面推送知识服务热生效；推送失败只提示，配置本身已保存。
// 密钥掩码回显、留空保持不变。
const ai = ref({
  llmBaseUrl: '',
  llmModel: '',
  apiKeySet: false,
  apiKeyMasked: '',
  asrEnabled: null as boolean | null,
  asrSpeakerEnabled: null as boolean | null,
})
const aiApiKey = ref('')
const aiBusy = ref(false)
const aiReady = ref(false)
const aiPush = ref('')
const aiPushOk = ref(true)

const loadAiConfig = async () => {
  try {
    const vo = await core.adminAiConfigGet()
    if (disposed) return
    aiReady.value = true
    ai.value = {
      llmBaseUrl: vo.llmBaseUrl,
      llmModel: vo.llmModel,
      apiKeySet: vo.apiKeySet,
      apiKeyMasked: vo.apiKeyMasked,
      asrEnabled: vo.asrEnabled,
      asrSpeakerEnabled: vo.asrSpeakerEnabled,
    }
    aiApiKey.value = ''
  } catch (e) {
    fail(e)
  }
}

const submitAiConfig = async () => {
  if (aiBusy.value || !aiReady.value) return
  aiBusy.value = true; aiPush.value = ''; message.value = ''
  try {
    const vo = await core.adminAiConfigSave(
      ai.value.llmBaseUrl.trim(),
      ai.value.llmModel.trim(),
      aiApiKey.value.trim(),
      ai.value.asrEnabled,
      ai.value.asrSpeakerEnabled,
    )
    aiPushOk.value = vo.pushed
    aiPush.value = vo.pushed ? '已保存并推送知识服务，配置热生效' : `已保存（未推送：${vo.pushError}）`
    notify(aiPushOk.value ? 'AI 配置已保存并热生效' : 'AI 配置已保存，暂未推送')
    await loadAiConfig()
  } catch (e) {
    fail(e)
  } finally {
    aiBusy.value = false
  }
}

const submitCreate = async () => {
  if (creating.value) return
  creating.value = true
  try {
    await core.adminCreateUser(form.value.userName, form.value.nickName, form.value.password)
    if (disposed) return
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
  if (busyId.value || loading.value) return
  busyId.value = user.userId; message.value = ''
  try {
    if (user.status === '0' && !await confirm({ title: '停用账号', message: '停用「' + (user.nickName || user.userName) + '」后，该成员将无法继续使用账号。', confirmText: '停用', danger: true })) return
    if (disposed) return
    await core.adminSetUserStatus(user.userId, user.status !== '0')
    if (!disposed) await load()
  } catch (e) {
    fail(e)
  } finally {
    busyId.value = ''
  }
}

const removeUser = async (user: ManagedUser) => {
  if (busyId.value || loading.value) return
  busyId.value = user.userId; message.value = ''
  try {
    if (!await confirm({ title: '删除账号', message: '删除账号「' + (user.nickName || user.userName) + '」？该账号的个人空间将无人可访问。', confirmText: '删除', danger: true }) || disposed) return
    await core.adminDeleteUser(user.userId)
    if (disposed) return
    notify('账号 ' + user.userName + ' 已删除')
    await load()
  } catch (e) { fail(e) }
  finally { busyId.value = '' }
}

const canSubmit = computed(() =>
  form.value.userName.trim().length > 0 && form.value.password.length > 0)

const initial = (user: ManagedUser) => {
  const name = user.nickName || user.userName || ''
  return name ? Array.from(name)[0] : '?'
}

const initialize = async () => {
  identityLoading.value = true; identityError.value = ''
  try {
    const user = await core.currentUser()
    if (disposed) return
    if (!user?.loggedIn || (!user.isAdmin && (!user.adminScope || user.adminScope === 'none'))) throw new Error('当前账号没有管理权限')
    currentUser.value = user
  } catch (e) { identityError.value = e instanceof Error ? e.message : String(e); return }
  finally { identityLoading.value = false }
  if (isSuper.value) {
    void load()
    void loadRegisterSwitch()
    void loadServiceConfig()
    void loadAiConfig()
    void loadSpaces()
    void loadDepts()
  } else {
    // 子管理员只有权限矩阵；账号/空间接口在控制面按超管拒绝，不能一进来就去撞
    tab.value = 'permissions'
  }
  // 权限矩阵按需加载：旧版控制面没有 /admin/members，超管停在账号页时不该先报一次错
  if (tab.value === 'permissions') {
    void loadPermissions()
    void loadRoles()
  }
}
onMounted(initialize)
// ═══ 权限：角色组 + 成员矩阵 ═══
// 角色把五项开关打包给一批人，成员挂了角色就按角色裁决（服务端取并集），逐账号开关不再生效；
// 没有角色的成员沿用逐账号矩阵，「缺行 = 全开」。子管理员只能看/改本部门成员的逐账号开关，
// 角色本身跨部门，只有超管能增删改与分配。
type FeatureKey = 'knowledge' | 'meeting' | 'drive' | 'shared' | 'plugin'
const featureLabels: { key: FeatureKey; label: string }[] = [
  { key: 'knowledge', label: '知识问答' },
  { key: 'meeting', label: '会议听记' },
  { key: 'drive', label: '网盘' },
  { key: 'shared', label: '共享空间' },
  { key: 'plugin', label: '插件中心' },
]
interface PermissionRow {
  member: AdminMember
  flags: FeatureFlags
  dirty: boolean
}
const permissionRows = ref<PermissionRow[]>([])
const permissionLoading = ref(false)
const permissionReady = ref(false)
const permissionError = ref('')
const rolesLoading = ref(false)
const rolesReady = ref(false)
const rolesError = ref('')
const permissionBusy = ref('')
const roles = ref<AdminRole[]>([])
// 每个成员的角色勾选草稿（键 userId）；保存后才回写服务端
const roleDraft = ref<Record<string, string[]>>({})
const roleForm = ref<AdminRole | null>(null)
const roleBusy = ref(false)
const permissionLocked = computed(() => !!permissionBusy.value || roleBusy.value || permissionLoading.value || !permissionReady.value || (isSuper.value && (rolesLoading.value || !rolesReady.value)))
const roleFormElement = ref<HTMLElement | null>(null)
const roleMenu = ref('')
let roleMenuTrigger: HTMLElement | null = null
const toggleRoleMenu = (userId: string, event: Event) => {
  if (permissionLocked.value) return
  roleMenuTrigger = event.currentTarget as HTMLElement
  roleMenu.value = roleMenu.value === userId ? '' : userId
}
const closeRoleMenu = () => { roleMenu.value = ''; roleMenuTrigger?.focus() }
const roleDraftChanged = (row: PermissionRow) => JSON.stringify([...(roleDraft.value[row.member.userId] ?? [])].sort()) !== JSON.stringify([...(row.member.roleIds ?? [])].sort())

// 管理范围档位：none 是普通权限组，其余档位授子管理员入口（成员范围由服务端裁剪）
const scopeOptions = [
  { value: 'none', label: '普通权限组' },
  { value: 'dept', label: '子管理员 · 本部门' },
  { value: 'dept_children', label: '子管理员 · 本部门及下级' },
  { value: 'specified', label: '子管理员 · 指定部门' },
  { value: 'all', label: '子管理员 · 全部成员' },
] as const

const emptyRole = (): AdminRole => ({
  roleName: '', adminScope: 'none', scopeDeptIds: [],
  knowledge: true, meeting: true, drive: true, shared: true, plugin: true,
})

// specified 档的部门勾选草稿；保存前至少要勾一个，否则不给提交
const roleDeptEmpty = computed(() =>
  roleForm.value?.adminScope === 'specified' && !(roleForm.value.scopeDeptIds ?? []).length)

const toggleScopeDept = (deptId: string, checked: boolean) => {
  if (!roleForm.value || roleBusy.value || !deptReady.value) return
  const current = roleForm.value.scopeDeptIds ?? []
  roleForm.value.scopeDeptIds = checked
    ? Array.from(new Set([...current, deptId]))
    : current.filter(id => id !== deptId)
}

const loadPermissions = async (savedUserId?: string) => {
  const request = ++permissionRevision
  permissionLoading.value = true; permissionReady.value = false; permissionError.value = ''
  try {
    const list = (await core.adminPermissionMembers()) ?? []
    if (disposed || request !== permissionRevision) return false
    const previous = permissionRows.value
    const drafts: Record<string, string[]> = {}
    permissionRows.value = list.map(member => {
      const old = previous.find(row => row.member.userId === member.userId)
      const keep = savedUserId !== undefined && member.userId !== savedUserId && old && JSON.stringify(old.member.roleIds ?? []) === JSON.stringify(member.roleIds ?? [])
      drafts[member.userId] = keep ? [...(roleDraft.value[member.userId] ?? member.roleIds ?? [])] : [...(member.roleIds ?? [])]
      return {
        member,
        flags: keep && old.dirty && !(member.roleIds ?? []).length ? { ...old.flags } : {
          userId: member.userId, knowledge: member.knowledge, meeting: member.meeting,
          drive: member.drive, shared: member.shared, plugin: member.plugin,
        },
        dirty: !!(keep && old.dirty && !(member.roleIds ?? []).length),
      }
    })
    roleDraft.value = drafts; permissionReady.value = true
    return true
  } catch (e) {
    if (!disposed && request === permissionRevision) permissionError.value = e instanceof Error ? e.message : String(e)
    return false
  } finally { if (!disposed && request === permissionRevision) permissionLoading.value = false }
}
const loadRoles = async () => {
  if (!isSuper.value) return true
  const request = ++rolesRevision
  rolesLoading.value = true; rolesReady.value = false; rolesError.value = ''
  try {
    const list = await core.adminPermissionRoles()
    if (disposed || request !== rolesRevision) return false
    roles.value = list ?? []; rolesReady.value = true
    return true
  } catch (e) {
    if (!disposed && request === rolesRevision) rolesError.value = e instanceof Error ? e.message : String(e)
    return false
  } finally { if (!disposed && request === rolesRevision) rolesLoading.value = false }
}
const openPermissions = async () => {
  if (identityLoading.value || identityError.value || !currentUser.value) return
  if (permissionBusy.value || roleBusy.value || permissionLoading.value || rolesLoading.value) return
  if (permissionRows.value.some(row => row.dirty || roleDraftChanged(row)) || roleForm.value) {
    if (tab.value !== 'permissions') { tab.value = 'permissions'; return }
    permissionBusy.value = 'refresh'
    const accepted = await confirm({ title: '重新读取权限', message: '刷新将丢弃本页尚未保存的权限和角色草稿。', confirmText: '放弃草稿并刷新' })
    permissionBusy.value = ''
    if (!accepted || disposed) return
    roleForm.value = null
  }
  tab.value = 'permissions'
  void loadPermissions(); void loadRoles()
  if (isSuper.value && !deptReady.value && !deptLoading.value) void loadDepts()
}
const roleDriven = (row: PermissionRow) => (row.member.roleIds ?? []).length > 0
const featureEnabled = (row: PermissionRow, key: FeatureKey) => key === 'shared' && !row.flags.drive ? false : row.flags[key]
const toggleFeature = (row: PermissionRow, key: FeatureKey) => {
  if (permissionLocked.value || roleDriven(row) || roleDraftChanged(row) || (key === 'shared' && !row.flags.drive)) return
  row.flags = { ...row.flags, [key]: !row.flags[key] }
  row.dirty = true
}
const savePermissions = async (row: PermissionRow) => {
  if (permissionLocked.value || roleDriven(row) || roleDraftChanged(row) || !row.dirty) return
  permissionBusy.value = row.member.userId
  message.value = ''
  try {
    await core.adminSavePermissions({ ...row.flags, userId: row.member.userId })
    if (disposed) return
    if (await loadPermissions(row.member.userId)) notify('已保存 ' + (row.member.nickName || row.member.userName) + ' 的功能权限')
    else fail('功能权限已提交，但最新权限读取失败，请刷新确认。')
  } catch (e) { fail(e) }
  finally { permissionBusy.value = '' }
}
const saveMemberRoles = async (row: PermissionRow) => {
  if (permissionLocked.value || !isSuper.value || !roleDraftChanged(row)) return
  const selected = [...(roleDraft.value[row.member.userId] ?? [])]
  permissionBusy.value = row.member.userId; message.value = ''
  try {
    const addsManagement = roles.value.some(role => selected.includes(role.roleId ?? '') && !(row.member.roleIds ?? []).includes(role.roleId ?? '') && role.adminScope !== 'none')
    const removesRole = (row.member.roleIds ?? []).some(id => !selected.includes(id))
    if ((addsManagement || removesRole) && !await confirm({ title: '确认角色变更', message: '此操作将重新计算「' + (row.member.nickName || row.member.userName) + '」的功能权限与管理范围。移除最后一个角色时保留当前有效功能限制，转为逐账号配置；角色带来的管理范围将撤去。', confirmText: '保存角色', danger: removesRole })) return
    if (disposed) return
    await core.adminAssignRoles(row.member.userId, selected)
    if (disposed) return
    if (await loadPermissions(row.member.userId)) notify('成员角色已更新，已读取实际权限')
    else fail('角色已提交，但实际权限读取失败，请刷新确认。')
  } catch (e) { fail(e) }
  finally { permissionBusy.value = '' }
}
const toggleRoleDraft = (row: PermissionRow, roleId: string, checked: boolean) => {
  if (permissionLocked.value || !isSuper.value || !roleId) return
  const current = roleDraft.value[row.member.userId] ?? []
  roleDraft.value[row.member.userId] = checked ? Array.from(new Set([...current, roleId])) : current.filter(id => id !== roleId)
}
const startRoleForm = async (role?: AdminRole) => {
  if (permissionLocked.value || !isSuper.value) return
  roleForm.value = role ? { ...role, scopeDeptIds: [...(role.scopeDeptIds ?? [])] } : emptyRole()
  await nextTick(); roleFormElement.value?.querySelector<HTMLInputElement>('input')?.focus()
}
const saveRole = async () => {
  if (permissionLocked.value || !isSuper.value || !roleForm.value || !roleForm.value.roleName.trim() || roleDeptEmpty.value) return
  if (roleForm.value.adminScope === 'specified' && !deptReady.value) return
  const draft: AdminRole = { ...roleForm.value, roleName: roleForm.value.roleName.trim(), scopeDeptIds: [...(roleForm.value.scopeDeptIds ?? [])] }
  if (draft.adminScope !== 'specified') delete draft.scopeDeptIds
  roleBusy.value = true; message.value = ''
  try {
    const previous = roles.value.find(role => role.roleId === draft.roleId)
    const scopeChanged = previous && (previous.adminScope !== draft.adminScope || JSON.stringify([...(previous.scopeDeptIds ?? [])].sort()) !== JSON.stringify([...(draft.scopeDeptIds ?? [])].sort()))
    if (scopeChanged && !await confirm({ title: '变更角色管理范围', message: '角色「' + draft.roleName + '」的管理范围将改变，对已分配该角色的成员同时生效。', confirmText: '确认变更' })) return
    if (disposed) return
    await core.adminSavePermissionRole(draft)
    if (disposed) return
    roleForm.value = null
    const results = await Promise.all([loadRoles(), loadPermissions('')])
    if (results.every(Boolean)) notify('角色「' + draft.roleName + '」已保存')
    else fail('角色已提交，但最新权限读取失败，请刷新确认。')
  } catch (e) { fail(e) }
  finally { roleBusy.value = false }
}
const removeRole = async (role: AdminRole) => {
  if (!role.roleId || permissionLocked.value || !isSuper.value) return
  roleBusy.value = true; message.value = ''
  try {
    if (!await confirm({ title: '删除角色', message: '删除角色「' + role.roleName + '」？仅分配此角色的成员会保留当前有效功能限制并转为逐账号配置；角色带来的管理范围将撤去。', confirmText: '删除', danger: true }) || disposed) return
    await core.adminDeletePermissionRole(role.roleId)
    if (disposed) return
    if (roleForm.value?.roleId === role.roleId) roleForm.value = null
    const results = await Promise.all([loadRoles(), loadPermissions('')])
    if (results.every(Boolean)) notify('角色「' + role.roleName + '」已删除')
    else fail('角色已删除，但最新权限读取失败，请刷新确认。')
  } catch (e) { fail(e) }
  finally { roleBusy.value = false }
}

const tabKey = (event: KeyboardEvent) => {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  const buttons = Array.from((event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role=tab]:not(:disabled)'))
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
  if (index < 0) return
  event.preventDefault()
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + buttons.length) % buttons.length
  buttons[next]?.focus(); buttons[next]?.click()
}
const roleSummary = (role: AdminRole) => {
  const enabled = featureLabels.filter(f => role[f.key]).map(f => f.label)
  return enabled.length ? enabled.join(' · ') : '五项全关'
}

</script>

<template>
  <div class="admin-panel" @click="roleMenu = ''">
    <header class="workspace-header">
      <div>
        <span class="section-label">管理</span>
        <h1>账号与空间</h1>
        <p>{{ isSuper ? '开设账号、分配空间大小，控制共享空间的访问权限。' : '管理授权范围内成员的功能权限。' }}</p>
      </div>
      <div class="admin-tabs" role="tablist" aria-label="管理功能" @keydown="tabKey">
        <button
          v-if="isSuper"
          :class="['admin-tab', tab === 'overview' ? 'active' : '']"
          type="button" role="tab" :disabled="memberFileBusy || !!permissionBusy || roleBusy || deptBusy || !!busyId" :aria-selected="tab === 'overview'" :tabindex="tab === 'overview' ? 0 : -1"
          @click="tab = 'overview'"
        >概览</button>
        <button
          v-if="isSuper"
          :class="['admin-tab', tab === 'accounts' ? 'active' : '']"
          type="button" role="tab" :disabled="memberFileBusy || !!permissionBusy || roleBusy || deptBusy || !!busyId" :aria-selected="tab === 'accounts'" :tabindex="tab === 'accounts' ? 0 : -1"
          @click="tab = 'accounts'"
        >账号</button>
        <button
          v-if="isSuper"
          :class="['admin-tab', tab === 'spaces' ? 'active' : '']"
          type="button" role="tab" :disabled="memberFileBusy || !!permissionBusy || roleBusy || deptBusy || !!busyId" :aria-selected="tab === 'spaces'" :tabindex="tab === 'spaces' ? 0 : -1"
          @click="tab = 'spaces'"
        >空间</button>
        <button
          :class="['admin-tab', tab === 'permissions' ? 'active' : '']"
          type="button" role="tab" :disabled="memberFileBusy || !!permissionBusy || roleBusy || deptBusy || !!busyId" :aria-selected="tab === 'permissions'" :tabindex="tab === 'permissions' ? 0 : -1"
          @click="openPermissions"
        >权限</button>
      </div>
    </header>

    <div :role="messageKind === 'error' ? 'alert' : 'status'" aria-atomic="true"><Transition v-bind="presenceMotion" name="es-fade"><div v-if="message" :key="message" :class="['admin-message', messageKind]">
      <span>{{ message }}</span>
      <button type="button" aria-label="关闭提示" @click="message = ''">×</button>
    </div></Transition></div>

    <!-- ═══ 账号 ═══ -->
    <Transition v-bind="presenceMotion" name="es-content">
    <div v-if="identityLoading" key="identity-loading" role="status">正在确认管理权限…</div>
    <div v-else-if="identityError" key="identity-error" role="alert">{{ identityError }} <button class="text-button" type="button" @click="initialize">重试</button></div>
    <div v-else-if="tab === 'overview'" key="overview" class="admin-panel" role="tabpanel" aria-label="概览">
      <div class="overview-grid">
        <div class="overview-card">
          <span class="overview-label">账号总数</span>
          <strong v-content-motion="userTotal">{{ userTotal ?? '—' }}</strong>
          <span class="overview-sub">管理页可开设与配额管理</span>
        </div>
        <div class="overview-card">
          <span class="overview-label">近 30 天查询</span>
          <strong>{{ knowledgeStats?.recent_queries ?? '—' }}</strong>
          <span class="overview-sub">累计 {{ knowledgeStats?.total_queries ?? '—' }} 次 · 盲区 {{ knowledgeStats?.blind_spot_count ?? '—' }} 次</span>
        </div>
        <div class="overview-card">
          <span class="overview-label">知识文档</span>
          <strong>{{ knowledgeStats?.documents ?? '—' }}</strong>
          <span class="overview-sub">LLM {{ !knowledgeStats ? '尚未确认' : knowledgeStats.llm === 'configured' ? '已配置' : '未配置' }}</span>
        </div>
        <div class="overview-card">
          <span class="overview-label">生成质量</span>
          <strong>{{ knowledgeStats?.generation?.avg_faithfulness != null ? knowledgeStats.generation.avg_faithfulness : '—' }}</strong>
          <span class="overview-sub">平均忠实度 · 生成 {{ knowledgeStats?.generation?.total ?? 0 }} 次</span>
        </div>
        <div class="overview-card">
          <span class="overview-label">共享空间用量</span>
          <strong>{{ formatBytes(sharedSpace?.usedBytes ?? 0) }}</strong>
          <span class="overview-sub">配额 {{ formatBytes(sharedSpace?.quotaBytes ?? 0) }}</span>
        </div>
        <div class="overview-card" :class="{ warn: overcommitted }">
          <span class="overview-label">容量承诺</span>
          <strong>{{ formatBytes(capacity?.committedBytes ?? 0) }}</strong>
          <span class="overview-sub">{{ overcommitted ? '⚠ 已超物理容量' : `物理可用 ${formatBytes(capacity?.usableBytes ?? 0)}` }}</span>
        </div>
      </div>
      <p v-if="overviewLoading" class="setting-hint" role="status">加载中…</p>
      <p v-if="overviewError" role="alert">统计读取失败：{{ overviewError }} <button class="text-button" type="button" @click="loadOverview">重试</button></p>
      <p class="setting-hint">
        统计窗口 30 天，数据来自知识服务查询日志与控制面容量聚合。高频引用文档与盲区明细见知识驾驶舱。
      </p>
    </div>
    <div v-else-if="tab === 'accounts'" key="accounts" class="admin-panel" role="tabpanel" aria-label="账号">
      <section class="card">
        <header class="card-header">
          <div>
            <span class="section-label">成员</span>
            <h2 v-content-motion="total">{{ total }} 个账号</h2>
          </div>
          <div class="card-actions">
            <button class="secondary-button compact" type="button" :disabled="loading" @click="load">
              <svg viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/></svg>
              刷新
            </button>
            <button class="primary-button compact" type="button" :disabled="creating" :aria-expanded="createOpen" @click="createOpen = !createOpen">
              <svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>
              新建账号
            </button>
          </div>
        </header>

        <!-- 新建表单：就地展开，不弹窗，避免遮住列表 -->
        <Transition v-bind="presenceMotion" name="es-disclosure"><form v-if="createOpen" class="admin-create" @submit.prevent="submitCreate">
          <label class="admin-field">
            <span>账号名</span>
            <input v-model="form.userName" :disabled="creating" class="setting-input" autocomplete="off" placeholder="登录用的账号" />
          </label>
          <label class="admin-field">
            <span>昵称</span>
            <input v-model="form.nickName" :disabled="creating" class="setting-input" autocomplete="off" placeholder="留空则同账号名" />
          </label>
          <label class="admin-field">
            <span>初始密码</span>
            <input v-model="form.password" :disabled="creating" class="setting-input" type="password" autocomplete="new-password" placeholder="告知该成员后请其自行修改" />
          </label>
          <div class="admin-create-actions">
            <button class="secondary-button compact" type="button" :disabled="creating" @click="createOpen = false">取消</button>
            <button class="primary-button compact" type="submit" :disabled="!canSubmit || creating">
              {{ creating ? '创建中…' : '创建' }}
            </button>
          </div>
        </form></Transition>

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
              <button class="secondary-button compact" type="button" :disabled="!!busyId || spacesLoading" @click="toggleStatus(user)">
                {{ user.status === '0' ? '停用' : '启用' }}
              </button>
              <button class="secondary-button compact danger" type="button" :disabled="!!busyId || spacesLoading" @click="removeUser(user)">
                删除
              </button>
            </div>
          </li>
        </ul>
      </section>

      <section class="card admin-switch-card">
        <div class="admin-switch-copy">
          <strong>允许用户自助注册</strong>
          <span>{{ registerReady ? '关闭后只能由管理员在此页面开设账号。' : '注册设置尚未确认，请重试读取。' }}</span>
          <button v-if="!registerReady" class="text-button" type="button" @click="loadRegisterSwitch">重试读取</button>
        </div>
        <button
          v-if="registerReady" aria-label="允许用户自助注册" :class="['admin-toggle', registerEnabled ? 'on' : '']"
          type="button" role="switch" :aria-checked="registerEnabled"
          :disabled="registerBusy" @click="toggleRegister"
        >
          <i />
        </button>
      </section>

      <section class="card">
        <header class="card-header">
          <div>
            <span class="section-label">服务配置</span>
            <h2>租户服务地址</h2>
          </div>
        </header>
        <div class="setting-row">
          <label class="setting-label" for="svcKnowledgeUrl">知识服务地址</label>
          <p class="setting-hint">
            同事「知识」页登录后自动使用该地址，免手填（当前展示：{{
              serviceSource === 'registry' ? '已登记' : '未登记，同主机推导值'
            }}）。清空保存即恢复推导（同主机 :8000）。
          </p>
          <div class="admin-service-row">
            <input
              id="svcKnowledgeUrl" :disabled="serviceBusy || !serviceReady"
              v-model="serviceKnowledgeUrl"
              class="setting-input"
              type="text"
              autocomplete="off"
              placeholder="http://192.168.1.10:8000"
            />
            <button v-if="!serviceReady" class="text-button" type="button" @click="loadServiceConfig">重试读取配置</button>
            <button class="secondary-button" type="button" :disabled="serviceBusy || !serviceReady" @click="submitServiceConfig">
              {{ serviceBusy ? '保存中…' : '保存' }}
            </button>
          </div>
        </div>
      </section>

      <section class="card">
        <header class="card-header">
          <div>
            <span class="section-label">AI 能力</span>
            <h2>大模型与会议听记</h2>
          </div>
        </header>
        <p class="setting-hint">
          知识问答与会议听记共用的 AI 配置。保存后由控制面推送到知识服务热生效；
          服务不可达时仅保存，恢复后重新保存即可下发。密钥保存在公司服务器，界面只显示尾四位。
        </p>
        <div class="setting-row">
          <label class="setting-label" for="aiLlmBaseUrl">LLM 地址</label>
          <input
            id="aiLlmBaseUrl" :disabled="aiBusy || !aiReady"
            v-model="ai.llmBaseUrl"
            class="setting-input"
            type="text"
            autocomplete="off"
            placeholder="https://token.sensenova.cn/v1"
          />
        </div>
        <div class="setting-row">
          <label class="setting-label" for="aiLlmModel">LLM 模型</label>
          <input
            id="aiLlmModel" :disabled="aiBusy || !aiReady"
            v-model="ai.llmModel"
            class="setting-input"
            type="text"
            autocomplete="off"
            placeholder="deepseek-v4-flash"
          />
        </div>
        <div class="setting-row">
          <label class="setting-label" for="aiLlmApiKey">LLM 密钥</label>
          <p class="setting-hint">{{ ai.apiKeySet ? `已设置（${ai.apiKeyMasked}）` : '未设置' }}，留空保持不变</p>
          <input
            id="aiLlmApiKey" :disabled="aiBusy || !aiReady"
            v-model="aiApiKey"
            class="setting-input"
            type="password"
            autocomplete="new-password"
            placeholder="留空保持不变"
          />
        </div>
        <div class="setting-row">
          <label class="setting-label" for="aiAsrToggle">会议听记</label>
          <p class="setting-hint">
            允许同事录音生成转写与纪要（{{ ai.asrEnabled === null ? '未设置，沿用部署默认' : ai.asrEnabled ? '当前：开' : '当前：关' }}）
          </p>
          <button
            id="aiAsrToggle"
            :class="['admin-toggle', ai.asrEnabled ? 'on' : '']"
            type="button" role="switch" :aria-checked="!!ai.asrEnabled"
            :disabled="aiBusy || !aiReady" @click="ai.asrEnabled = !ai.asrEnabled"
          >
            <i />
          </button>
        </div>
        <div class="setting-row">
          <label class="setting-label" for="aiSpkToggle">说话人分离</label>
          <p class="setting-hint">
            转写时区分不同发言人（{{ ai.asrSpeakerEnabled === null ? '未设置，沿用部署默认' : ai.asrSpeakerEnabled ? '当前：开' : '当前：关' }}）
          </p>
          <button
            id="aiSpkToggle"
            :class="['admin-toggle', ai.asrSpeakerEnabled ? 'on' : '']"
            type="button" role="switch" :aria-checked="!!ai.asrSpeakerEnabled"
            :disabled="aiBusy || !aiReady" @click="ai.asrSpeakerEnabled = !ai.asrSpeakerEnabled"
          >
            <i />
          </button>
        </div>
        <div class="admin-service-row">
          <p v-if="aiPush" :class="['setting-hint', { 'meeting-failed': !aiPushOk }]">{{ aiPush }}</p>
          <button v-if="!aiReady" class="text-button" type="button" @click="loadAiConfig">重试读取配置</button>
          <button class="secondary-button" type="button" :disabled="aiBusy || !aiReady" @click="submitAiConfig">
            {{ aiBusy ? '保存中…' : '保存' }}
          </button>
        </div>
      </section>
    </div>

    <!-- ═══ 空间 ═══ -->
    <div v-else-if="tab === 'spaces'" key="spaces" class="admin-panel" role="tabpanel" aria-label="空间">
      <p v-if="spacesError" role="alert">空间权限读取失败：{{ spacesError }}，请刷新后继续编辑。</p>
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

      <!--
        文件访问角色（企业权限第三片）。按 fileId 给账号/部门配四档角色，
        服务端在下载/删除/改名/覆盖上传六处真实拦截；未配角色的文件保持默认行为。
      -->
      <section class="card">
        <header class="card-header">
          <div>
            <span class="section-label">文件访问角色</span>
            <h2>按文件给成员配权限</h2>
          </div>
          <div class="card-actions">
            <button class="secondary-button compact" type="button" :disabled="filesLoading || memberFileBusy" @click="void loadSharedFiles()">
              <svg viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/></svg>
              刷新文件
            </button>
          </div>
        </header>
        <p class="setting-hint perm-hint">
          四档：可管理（含授权/删除）&gt; 可编辑 &gt; 可查看下载 &gt; 仅可查看。
          删除是可管理独占；文件列表只用于选择目标，实际访问仍由控制面按文件授权与空间规则裁决。
          服务端护栏：不能修改自己的角色。
        </p>
        <div class="role-field">
          <span>选择文件</span>
          <select aria-label="选择共享文件" :disabled="filesLoading || !filesReady || memberFileBusy" :value="memberFileId" @change="void pickMemberFile(($event.target as HTMLSelectElement).value)">
            <option value="">— 选择共享空间文件 —</option>
            <option v-for="f in sharedFiles.filter(f => f.fileId && f.fileId !== '0')" :key="f.fileId" :value="f.fileId">
              {{ f.name }}{{ f.uploadByName ? `（上传者 ${f.uploadByName}）` : '' }}
            </option>
          </select>
        </div>
        <p v-if="filesLoading" role="status">正在读取共享文件…</p>
        <p v-if="filesError" role="alert">共享文件读取失败：{{ filesError }}，可点击刷新文件重试。</p>
        <Transition v-bind="presenceMotion" name="es-content"><FileMemberEditor v-if="memberFileId && filesReady" :file-id="memberFileId" :file-name="memberFileName" :operator-id="currentUser?.userId ?? ''" @busy-change="memberFileBusy = $event" @saved="notify('成员角色已保存')" /></Transition>
      </section>

      <section class="card">
        <header class="card-header">
          <div>
            <span class="section-label">共享空间</span>
            <h2>按账号与部门授权的共享空间</h2>
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
                v-model="quotaDraft.shared" :disabled="spacesLocked"
                class="setting-input" type="number" min="0" step="0.5" placeholder="未分配"
                @keyup.enter="saveSharedQuota"
              />
              <button
                class="primary-button compact" type="button"
                :disabled="spacesLocked" @click="saveSharedQuota"
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
              <i :style="{ transform: `scaleX(${(usedPercent(sharedSpace) ?? 0) / 100})` }" />
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
                v-model="quotaDraft[user.userId]" :disabled="spacesLocked"
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
                  <i :style="{ transform: `scaleX(${(usedPercent(personalByOwner[user.userId]) ?? 0) / 100})` }" />
                </div>
              </template>
              <span v-else class="admin-space-cell pending">待开空间</span>
            </div>

            <div class="role-field">
              <label :for="'shared-permission-' + user.userId">个人显式授权 · 当前{{ permissionLabel(user.userId) }}</label>
              <select :id="'shared-permission-' + user.userId" class="admin-perm" :disabled="spacesLocked" :value="sharedPermissionDraft[user.userId] ?? userMemberRow(user.userId)?.permission ?? ''" @change="sharedPermissionDraft[user.userId] = ($event.target as HTMLSelectElement).value">
                <option value="">未单独授权（保留部门继承）</option><option value="read">只读</option><option value="write">读写</option>
              </select>
              <button data-testid="save-shared-permission" class="secondary-button compact" type="button" :disabled="spacesLocked || (sharedPermissionDraft[user.userId] ?? '') === (userMemberRow(user.userId)?.permission ?? '')" @click="saveSharedPermission(user)">保存授权</button>
              <span class="setting-hint">实际权限需合并部门继承与功能开关，以控制面裁决为准。</span>
            </div>

            <div class="admin-row-actions">
              <button
                class="primary-button compact" type="button"
                :disabled="spacesLocked" @click="savePersonalQuota(user)"
              >{{ busyId === user.userId ? '保存中…' : '保存' }}</button>
            </div>
          </li>
        </ul>
        <p class="setting-hint">
          容量留空即收回，账号在客户端会显示为「待开空间」。配额在控制面签发上传授权时校验。
        </p>

        <div class="dept-auth">
          <div class="admin-usage-head"><span>按部门授权</span></div>
          <div class="dept-auth-form">
            <select class="dept-select" aria-label="授权部门" :disabled="spacesLocked || !deptReady" :value="deptForm.deptId" @change="onDeptChange">
              <option value="">选择部门…</option>
              <option v-for="dept in depts" :key="dept.deptId" :value="dept.deptId">{{ dept.deptName }}</option>
            </select>
            <select class="dept-select" aria-label="部门权限" :disabled="spacesLocked || !deptReady" :value="deptForm.permission" @change="onDeptPermChange">
              <option value="read">只读</option>
              <option value="write">读写</option>
            </select>
            <button class="primary-button compact" type="button" :disabled="spacesLocked || !deptReady || !deptForm.deptId" @click="addDeptAuth">授权</button>
          </div>
          <p v-if="deptLoading" role="status">正在读取部门…</p>
          <p v-if="deptError" role="alert">部门读取失败：{{ deptError }} <button class="text-button" type="button" @click="loadDepts">重试</button></p>
          <ul v-if="deptMemberRows.length" class="admin-user-list">
            <li v-for="row in deptMemberRows" :key="row.memberId" class="admin-user-row">
              <div class="admin-user-main">
                <strong>🏢 {{ row.name || row.memberId }}</strong>
                <span>{{ row.permission === 'write' ? '读写' : '只读' }} · 整部门生效</span>
              </div>
              <div class="admin-row-actions">
                <button class="secondary-button compact" type="button" :disabled="spacesLocked" @click="revokeDeptAuth(row)">撤销</button>
              </div>
            </li>
          </ul>
          <p class="setting-hint">
            部门授权对该部门全部成员生效；账号的显式授权与部门授权取宽（读写优先于只读）。
          </p>
        </div>
      </section>
    </div>

    <!-- ═══ 权限：角色组 + 成员矩阵 ═══ -->
    <div v-else-if="tab === 'permissions'" key="permissions" class="admin-panel" role="tabpanel" aria-label="权限">
      <section v-if="isSuper" class="card">
        <header class="card-header">
          <div>
            <span class="section-label">权限角色</span>
            <h2>一批人共用一套权限</h2>
          </div>
          <div class="card-actions">
            <button class="secondary-button compact" type="button" :disabled="permissionLocked" @click="startRoleForm()">新建角色</button>
          </div>
        </header>
        <p class="setting-hint perm-hint">
          成员拥有其所有角色授予的功能。已分配角色时，请通过角色调整权限。
          角色可设为「子管理员」：该成员能进入本页，按管理范围看到、修改范围内成员的功能权限
          （本部门 / 含下级 / 指定部门 / 全部成员）。
        </p>
        <Transition v-bind="presenceMotion" name="es-disclosure"><form v-if="roleForm" ref="roleFormElement" class="role-form" :aria-busy="roleBusy" @submit.prevent="saveRole">
          <label class="role-field">
            <span>角色名</span>
            <input :disabled="roleBusy" v-model="roleForm.roleName" type="text" maxlength="64" placeholder="如：车间主管、只读成员" />
          </label>
          <label class="role-field">
            <span>管理范围</span>
            <select :disabled="roleBusy" v-model="roleForm.adminScope">
              <option v-for="opt in scopeOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
            </select>
          </label>
          <Transition v-bind="presenceMotion" name="es-disclosure"><div v-if="roleForm.adminScope === 'specified'" class="role-field role-dept-field">
            <span>指定部门（至少勾选一个）</span>
            <div class="role-dept-list">
              <label v-for="dept in depts" :key="dept.deptId">
                <input
                  type="checkbox" :disabled="roleBusy || !deptReady" :value="dept.deptId"
                  :checked="(roleForm.scopeDeptIds ?? []).includes(dept.deptId)"
                  @change="toggleScopeDept(dept.deptId, ($event.target as HTMLInputElement).checked)"
                />
                <span>{{ dept.deptName }}</span>
              </label>
              <div v-for="id in deptReady ? (roleForm.scopeDeptIds ?? []).filter(id => !depts.some(dept => dept.deptId === id)) : []" :key="id" class="role-row">
                <span>部门 #{{ id }} 已不在可选范围</span><button type="button" class="text-button" :disabled="roleBusy" @click="toggleScopeDept(id, false)">移除无效部门</button>
              </div>
              <p v-if="deptLoading" role="status">正在读取部门…</p>
              <p v-else-if="deptError" role="alert">部门读取失败：{{ deptError }} <button type="button" class="text-button" @click="loadDepts">重试</button></p>
              <div v-else-if="deptReady && !depts.length" class="admin-empty">暂无可授权部门</div>
            </div>
          </div></Transition>
          <div class="role-flags">
            <div v-for="f in featureLabels" :key="f.key" class="role-flag">
              <button
                :class="['admin-toggle', roleForm[f.key] ? 'on' : '']"
                type="button" role="switch" :aria-checked="roleForm[f.key]"
                :aria-label="`${f.label}：${roleForm[f.key] ? '开启' : '关闭'}`"
                :disabled="roleBusy" @click="roleForm[f.key] = !roleForm[f.key]"
              ><i /></button>
              <span>{{ f.label }}</span>
            </div>
          </div>
          <div class="role-form-actions">
            <button class="secondary-button compact" type="button" :disabled="roleBusy" @click="roleForm = null">取消</button>
            <button
              class="primary-button compact" type="submit"
              :disabled="permissionLocked || !roleForm.roleName.trim() || roleDeptEmpty || (roleForm.adminScope === 'specified' && !deptReady)"
            >
              {{ roleBusy ? '保存中…' : '保存角色' }}
            </button>
          </div>
        </form></Transition>
        <Transition v-bind="presenceMotion" name="es-content">
        <p v-if="rolesLoading" key="loading" role="status">正在读取角色…</p>
        <p v-else-if="rolesError" key="error" role="alert">角色读取失败：{{ rolesError }} <button class="text-button" type="button" @click="openPermissions">重试</button></p>
        <div v-else-if="rolesReady && !roles.length" class="admin-empty">还没有角色。成员按下方逐账号开关生效。</div>
        <div v-else-if="rolesReady" key="roles" class="role-list">
          <div v-for="role in roles" :key="role.roleId" class="role-row">
            <div class="role-main">
              <strong>{{ role.roleName }}</strong>
              <span v-if="role.adminScope !== 'none'" class="role-tag">{{ ADMIN_SCOPE_LABELS[role.adminScope] }}</span>
              <span class="role-meta">{{ roleSummary(role) }} · {{ role.memberCount || 0 }} 人</span>
            </div>
            <div class="role-actions">
              <button class="secondary-button compact" type="button" :disabled="permissionLocked" @click="startRoleForm(role)">编辑</button>
              <button class="secondary-button compact" type="button" :disabled="permissionLocked" @click="removeRole(role)">删除</button>
            </div>
          </div>
        </div>
        </Transition>
      </section>

      <section class="card perm-card">
        <header class="card-header">
          <div>
            <span class="section-label">成员权限</span>
            <h2>{{ isSuper ? '每个成员能用哪些功能' : '授权范围内成员的功能权限' }}</h2>
          </div>
          <div class="card-actions">
            <button class="secondary-button compact" type="button" :disabled="permissionLoading || rolesLoading || !!permissionBusy || roleBusy" @click="openPermissions">
              <svg viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/></svg>
              刷新
            </button>
          </div>
        </header>
        <p class="setting-hint perm-hint">
          关闭后，成员将无法使用该功能。
          关闭「网盘」时共享空间一并停用。挂了角色的成员由角色决定，开关在这里只读。
        </p>
        <Transition v-bind="presenceMotion" name="es-content">
        <p v-if="permissionLoading" key="loading" role="status">正在读取权限…</p>
        <p v-else-if="permissionError" key="error" role="alert">权限读取失败：{{ permissionError }}，请刷新重试。</p>
        <div v-else-if="permissionReady && !permissionRows.length" class="admin-empty">本范围内暂无成员。</div>
        <div v-else-if="permissionReady" key="members" class="perm-table">
          <div class="perm-row perm-head" aria-hidden="true">
            <span>成员</span>
            <span>角色</span>
            <span v-for="f in featureLabels" :key="f.key">{{ f.label }}</span>
            <span></span>
          </div>
          <div v-for="row in permissionRows" :key="row.member.userId" class="perm-row" role="group" :aria-label="(row.member.nickName || row.member.userName) + '的功能权限'">
            <div class="perm-user">
              <strong>
                {{ row.member.nickName || row.member.userName }}
                <span v-if="row.member.adminScope !== 'none'" class="role-tag">{{ ADMIN_SCOPE_LABELS[row.member.adminScope] }}</span>
                <span v-if="row.member.status === '1'" class="role-tag muted">停用</span>
              </strong>
              <span>{{ row.member.userName }}<template v-if="row.member.deptName"> · {{ row.member.deptName }}</template></span>
            </div>
            <div class="perm-role-cell">
              <div v-if="isSuper" class="perm-role-details" @click.stop @keydown.esc.stop="closeRoleMenu">
                <button class="text-button perm-role-trigger" type="button" :disabled="permissionLocked" :aria-expanded="roleMenu === row.member.userId" :aria-label="(row.member.nickName || row.member.userName) + '的角色分配'" @click="toggleRoleMenu(row.member.userId, $event)">{{ row.member.roleNames?.length ? row.member.roleNames.join('、') : '未分配' }}</button>
                <Transition v-bind="presenceMotion" name="es-popover"><div v-if="roleMenu === row.member.userId" class="perm-role-menu">
                  <label v-for="role in roles" :key="role.roleId">
                    <input
                      type="checkbox" :disabled="permissionLocked" :value="role.roleId"
                      :checked="(roleDraft[row.member.userId] ?? []).includes(role.roleId ?? '')"
                      @change="toggleRoleDraft(row, role.roleId ?? '', ($event.target as HTMLInputElement).checked)"
                    />
                    <span>{{ role.roleName }}</span>
                  </label>
                  <button class="secondary-button compact" type="button" :disabled="permissionLocked || !roleDraftChanged(row)" @click="saveMemberRoles(row)">保存角色</button>
                </div></Transition>
              </div>
              <span v-else class="perm-role-static">{{ row.member.roleNames?.length ? row.member.roleNames.join('、') : '未分配' }}</span>
              <span v-if="roleDraftChanged(row)" class="setting-hint">角色草稿未保存，当前权限仍按原角色生效</span>
              <span v-else-if="row.dirty" class="setting-hint">功能权限草稿未保存</span>
            </div>
            <span v-for="f in featureLabels" :key="f.key" class="perm-cell">
              <button
                :class="['admin-toggle', featureEnabled(row, f.key) ? 'on' : '']"
                type="button" role="switch" :aria-checked="featureEnabled(row, f.key)"
                :aria-label="`${f.label}：${featureEnabled(row, f.key) ? '开启' : '关闭'}`"
                :title="roleDriven(row) ? `由角色「${(row.member.roleNames ?? []).join('、')}」决定` : (f.key === 'shared' && !row.flags.drive ? '网盘关闭时共享空间不可用' : '')"
                :disabled="roleDriven(row) || roleDraftChanged(row) || permissionLocked || (f.key === 'shared' && !row.flags.drive)"
                @click="toggleFeature(row, f.key)"
              ><i /></button>
            </span>
            <span class="perm-cell">
              <button
                class="secondary-button compact" type="button"
                :disabled="roleDriven(row) || roleDraftChanged(row) || !row.dirty || permissionLocked"
                @click="savePermissions(row)"
              >{{ permissionBusy === row.member.userId ? '保存中…' : roleDraftChanged(row) ? '先保存角色' : roleDriven(row) ? '由角色决定' : row.dirty ? '保存' : '已保存' }}</button>
            </span>
          </div>
        </div>
        </Transition>
      </section>
    </div>
    </Transition>
  </div>
</template>
