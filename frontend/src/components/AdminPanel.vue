<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { core } from '../services/core'
import { useConfirm } from '../composables/useConfirm'
import { ADMIN_SCOPE_LABELS, QUOTA_UNLIMITED, QUOTA_UNSET, type AdminDept, type AdminMember, type AdminRole, type AdminSharedMember, type AuthUser, type Capacity, type FeatureFlags, type FileMemberRole, type KnowledgeStats, type ManagedUser, type SharedFileEntry, type Space } from '../types/core'

type Tab = 'overview' | 'accounts' | 'spaces' | 'permissions'

const tab = ref<Tab>('accounts')
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

const notify = (text: string, kind: 'ok' | 'error' = 'ok') => {
  message.value = text
  messageKind.value = kind
  if (kind === 'ok') setTimeout(() => { if (message.value === text) message.value = '' }, 2600)
}
const fail = (e: unknown) => notify(e instanceof Error ? e.message : String(e), 'error')

// ═══ 空间 ═══
const spaces = ref<Space[]>([])
const knowledgeStats = ref<KnowledgeStats | null>(null)
const userTotal = ref(0)
const overviewLoading = ref(false)
const sharedMembers = ref<AdminSharedMember[]>([])
const depts = ref<AdminDept[]>([])
const deptForm = ref<{ deptId: string; permission: string }>({ deptId: '', permission: 'read' })
const spacesLoading = ref(false)

// ═══ 文件访问角色（企业权限第三片）═══
// 按 fileId 给账号/部门配四档角色；整组替换保存。
// 护栏在服务端（不能修改自己的角色、仅 manage 档可授权），前端只做可用的编辑器。
const sharedFiles = ref<SharedFileEntry[]>([])
const memberFileId = ref('')
const memberRows = ref<FileMemberRole[]>([])
const memberLoading = ref(false)
const memberBusy = ref(false)
const memberAdd = ref<{ memberType: 'user' | 'dept'; memberId: string; role: FileMemberRole['role'] }>({
  memberType: 'user', memberId: '', role: 'read',
})
const FILE_ROLE_LABELS: Record<FileMemberRole['role'], string> = {
  manage: '可管理（含授权/删除）',
  edit: '可编辑（无删除）',
  download: '可查看下载',
  read: '仅可查看',
}

const loadSharedFiles = async () => {
  try { sharedFiles.value = (await core.sharedFileList()) ?? [] } catch (e) { fail(e) }
}

const memberFileName = computed(() =>
  sharedFiles.value.find(f => f.fileId === memberFileId.value)?.name ?? '')

// 成员行展示名：账号取昵称（回退登录名），部门取部门名；查不到按 ID 兜底
const memberLabel = (row: FileMemberRole) => {
  const name = row.memberType === 'user'
    ? users.value.find(u => u.userId === row.memberId)?.nickName
      || users.value.find(u => u.userId === row.memberId)?.userName
    : depts.value.find(d => d.deptId === row.memberId)?.deptName
  return name || `#${row.memberId}`
}

const pickMemberFile = async (fileId: string) => {
  memberFileId.value = fileId
  memberRows.value = []
  if (!fileId) return
  memberLoading.value = true
  try { memberRows.value = (await core.fileMemberList(fileId)) ?? [] } catch (e) { fail(e) } finally { memberLoading.value = false }
}

const addMemberRow = () => {
  if (!memberAdd.value.memberId) return
  const dup = memberRows.value.some(m =>
    m.memberType === memberAdd.value.memberType && m.memberId === memberAdd.value.memberId)
  if (dup) { fail(new Error('该成员已有角色行，直接改档位即可')); return }
  memberRows.value = [...memberRows.value, { ...memberAdd.value }]
  memberAdd.value = { memberType: 'user', memberId: '', role: 'read' }
}

const removeMemberRow = (row: FileMemberRole) => {
  memberRows.value = memberRows.value.filter(m =>
    !(m.memberType === row.memberType && m.memberId === row.memberId))
}

const saveMemberRows = async () => {
  if (!memberFileId.value) return
  memberBusy.value = true
  try {
    await core.fileMemberReplace(memberFileId.value, memberRows.value)
    notify(`已保存「${memberFileName.value}」的成员角色（${memberRows.value.length} 条）`)
    await pickMemberFile(memberFileId.value)
  } catch (e) {
    fail(e)
  } finally {
    memberBusy.value = false
  }
}
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
  overviewLoading.value = true
  try {
    const [stats, users] = await Promise.all([
      core.adminKnowledgeStats(30).catch(() => null),
      core.adminListUsers(1, 1).catch(() => null),
    ])
    knowledgeStats.value = stats
    if (users) userTotal.value = users.total
  } catch (e) {
    fail(e)
  } finally {
    overviewLoading.value = false
  }
}

const loadSpaces = async () => {
  spacesLoading.value = true
  // 文件访问角色卡的文件列表并行刷（失败不阻塞空间页其余数据）
  void loadSharedFiles()
  try {
    const [list, members, cap, deptList] = await Promise.all([
      core.adminListSpaces(),
      core.adminSharedMembers(),
      core.adminCapacity(),
      core.adminListDepts().catch(() => []),
    ])
    spaces.value = list ?? []
    sharedMembers.value = members ?? []
    capacity.value = cap ?? null
    depts.value = deptList ?? []
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

// 某账号的显式授权行（不含部门继承——生效规则是个人行与部门行取宽，见控制面 SpaceService）
const userMemberRow = (userId: string) => sharedMembers.value.find(m => m.memberType === 'user' && m.memberId === userId)
const deptMemberRows = computed(() => sharedMembers.value.filter(m => m.memberType === 'dept'))

// 共享空间权限三态轮转：无 → 只读 → 读写 → 无（仅操作个人显式授权）
const cycleSharedPermission = async (user: ManagedUser) => {
  const current = userMemberRow(user.userId)?.permission ?? ''
  const next = current === '' ? 'read' : current === 'read' ? 'write' : ''
  busyId.value = user.userId
  try {
    await core.adminGrantShared('user', user.userId, next)
    const label = next === '' ? '已撤销共享空间权限' : next === 'read' ? '已设为只读' : '已设为读写'
    notify(`${user.nickName || user.userName}：${label}`)
    await loadSpaces()
  } catch (e) {
    fail(e)
  } finally {
    busyId.value = ''
  }
}

const permissionLabel = (userId: string) => {
  const perm = userMemberRow(userId)?.permission ?? ''
  return perm === 'write' ? '读写' : perm === 'read' ? '只读' : '无权限'
}

// ── 部门授权（片 1）：整部门生效，个人显式授权与其取宽 ──
const deptBusy = ref(false)
const addDeptAuth = async () => {
  if (!deptForm.value.deptId) {
    notify('请选择部门', 'error')
    return
  }
  deptBusy.value = true
  try {
    await core.adminGrantShared('dept', deptForm.value.deptId, deptForm.value.permission)
    notify(deptForm.value.permission === 'write' ? '部门已可读写共享空间' : '部门已可只读共享空间')
    deptForm.value = { deptId: '', permission: 'read' }
    await loadSpaces()
  } catch (e) {
    fail(e)
  } finally {
    deptBusy.value = false
  }
}
const revokeDeptAuth = async (row: AdminSharedMember) => {
  deptBusy.value = true
  try {
    await core.adminGrantShared('dept', row.memberId, '')
    notify(`已撤销部门「${row.name || row.memberId}」的授权`)
    await loadSpaces()
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

// 服务配置（租户服务发现）：管理员在这里登记知识服务地址，同事端登录后自动拿到，
// 免手填。来源 derived 表示还没登记、展示的是同主机推导的默认值。
const serviceKnowledgeUrl = ref('')
const serviceSource = ref<'' | 'registry' | 'derived'>('')
const serviceBusy = ref(false)

const loadServiceConfig = async () => {
  try {
    const endpoints = await core.serviceEndpoints()
    serviceKnowledgeUrl.value = endpoints.knowledgeUrl
    serviceSource.value = endpoints.knowledgeUrlSource
  } catch (e) {
    fail(e)
  }
}

const submitServiceConfig = async () => {
  if (serviceBusy.value) return
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
const aiPush = ref('')
const aiPushOk = ref(true)

const loadAiConfig = async () => {
  try {
    const vo = await core.adminAiConfigGet()
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
  if (aiBusy.value) return
  aiBusy.value = true
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
  const confirmed = await confirm({
    title: '删除账号',
    message: `删除账号「${user.nickName || user.userName}」？该账号的个人空间将无人可访问。`,
    confirmText: '删除',
    danger: true,
  })
  if (!confirmed) return
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

onMounted(async () => {
  try { currentUser.value = await core.currentUser() } catch { currentUser.value = null }
  if (isSuper.value) {
    void load()
    void loadRegisterSwitch()
    void loadServiceConfig()
    void loadAiConfig()
    void loadSpaces()
  } else {
    // 子管理员只有权限矩阵；账号/空间接口在控制面按超管拒绝，不能一进来就去撞
    tab.value = 'permissions'
  }
  // 权限矩阵按需加载：旧版控制面没有 /admin/members，超管停在账号页时不该先报一次错
  if (tab.value === 'permissions') {
    void loadPermissions()
    void loadRoles()
  }
})
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
const permissionBusy = ref('')
const roles = ref<AdminRole[]>([])
// 每个成员的角色勾选草稿（键 userId）；保存后才回写服务端
const roleDraft = ref<Record<string, string[]>>({})
const roleForm = ref<AdminRole | null>(null)
const roleBusy = ref(false)

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
  if (!roleForm.value) return
  const current = roleForm.value.scopeDeptIds ?? []
  roleForm.value.scopeDeptIds = checked
    ? Array.from(new Set([...current, deptId]))
    : current.filter(id => id !== deptId)
}

const loadPermissions = async () => {
  permissionLoading.value = true
  try {
    const list = (await core.adminPermissionMembers()) ?? []
    permissionRows.value = list.map(member => ({
      member,
      flags: {
        userId: member.userId,
        knowledge: member.knowledge,
        meeting: member.meeting,
        drive: member.drive,
        shared: member.shared,
        plugin: member.plugin,
      },
      dirty: false,
    }))
    const drafts: Record<string, string[]> = {}
    for (const member of list) drafts[member.userId] = [...(member.roleIds ?? [])]
    roleDraft.value = drafts
  } catch (e) {
    fail(e)
  } finally {
    permissionLoading.value = false
  }
}

const loadRoles = async () => {
  if (!isSuper.value) return
  try { roles.value = (await core.adminPermissionRoles()) ?? [] } catch (e) { fail(e) }
}

const openPermissions = () => {
  tab.value = 'permissions'
  void loadPermissions()
  void loadRoles()
  // 角色表单的「指定部门」勾选要用部门树；超管才看得到角色卡，提前拉好
  if (isSuper.value && !depts.value.length) {
    core.adminListDepts().then(list => { depts.value = list ?? [] }).catch(() => {})
  }
}

const roleDriven = (row: PermissionRow) => (row.member.roleIds ?? []).length > 0

const toggleFeature = (row: PermissionRow, key: FeatureKey) => {
  if (roleDriven(row)) return
  row.flags = { ...row.flags, [key]: !row.flags[key] }
  row.dirty = true
}

const savePermissions = async (row: PermissionRow) => {
  permissionBusy.value = row.member.userId
  try {
    await core.adminSavePermissions({ ...row.flags, userId: row.member.userId })
    row.dirty = false
    notify(`已保存 ${row.member.nickName || row.member.userName} 的功能权限`)
  } catch (e) {
    fail(e)
  } finally {
    permissionBusy.value = ''
  }
}

const saveMemberRoles = async (row: PermissionRow) => {
  permissionBusy.value = row.member.userId
  try {
    await core.adminAssignRoles(row.member.userId, roleDraft.value[row.member.userId] ?? [])
    notify(`已更新 ${row.member.nickName || row.member.userName} 的角色`)
    await loadPermissions()
  } catch (e) {
    fail(e)
  } finally {
    permissionBusy.value = ''
  }
}

const toggleRoleDraft = (row: PermissionRow, roleId: string, checked: boolean) => {
  const current = roleDraft.value[row.member.userId] ?? []
  roleDraft.value[row.member.userId] = checked
    ? Array.from(new Set([...current, roleId]))
    : current.filter(id => id !== roleId)
}

const startRoleForm = (role?: AdminRole) => {
  roleForm.value = role ? { ...role } : emptyRole()
}

const saveRole = async () => {
  if (!roleForm.value) return
  roleBusy.value = true
  try {
    const name = roleForm.value.roleName
    // 范围部门列表只在 specified 档携带；其余档位清掉，避免上次编辑的残留误存
    const draft = { ...roleForm.value }
    if (draft.adminScope !== 'specified') {
      delete draft.scopeDeptIds
    }
    await core.adminSavePermissionRole(draft)
    roleForm.value = null
    notify(`角色「${name}」已保存`)
    await Promise.all([loadRoles(), loadPermissions()])
  } catch (e) {
    fail(e)
  } finally {
    roleBusy.value = false
  }
}

const removeRole = async (role: AdminRole) => {
  if (!role.roleId) return
  const confirmed = await confirm({
    title: '删除角色',
    message: `删除角色「${role.roleName}」？只挂这一个角色的成员会保留当前权限（转为逐账号配置），不会被放大成全开。`,
    confirmText: '删除',
    danger: true,
  })
  if (!confirmed) return
  try {
    await core.adminDeletePermissionRole(role.roleId)
    notify(`角色「${role.roleName}」已删除`)
    await Promise.all([loadRoles(), loadPermissions()])
  } catch (e) {
    fail(e)
  }
}

const roleSummary = (role: AdminRole) => {
  const enabled = featureLabels.filter(f => role[f.key]).map(f => f.label)
  return enabled.length ? enabled.join(' · ') : '五项全关'
}

</script>

<template>
  <div class="admin-panel">
    <header class="workspace-header">
      <div>
        <span class="section-label">管理</span>
        <h1>账号与空间</h1>
        <p>{{ isSuper ? '开设账号、分配空间大小，控制共享空间的访问权限。' : '管理本部门成员的功能权限。' }}</p>
      </div>
      <div class="admin-tabs" role="tablist">
        <button
          v-if="isSuper"
          :class="['admin-tab', tab === 'overview' ? 'active' : '']"
          type="button" role="tab" :aria-selected="tab === 'overview'"
          @click="tab = 'overview'"
        >概览</button>
        <button
          v-if="isSuper"
          :class="['admin-tab', tab === 'accounts' ? 'active' : '']"
          type="button" role="tab" :aria-selected="tab === 'accounts'"
          @click="tab = 'accounts'"
        >账号</button>
        <button
          v-if="isSuper"
          :class="['admin-tab', tab === 'spaces' ? 'active' : '']"
          type="button" role="tab" :aria-selected="tab === 'spaces'"
          @click="tab = 'spaces'"
        >空间</button>
        <button
          :class="['admin-tab', tab === 'permissions' ? 'active' : '']"
          type="button" role="tab" :aria-selected="tab === 'permissions'"
          @click="openPermissions"
        >权限</button>
      </div>
    </header>

    <div v-if="message" :class="['admin-message', messageKind]" role="status">
      <span>{{ message }}</span>
      <button type="button" aria-label="关闭提示" @click="message = ''">×</button>
    </div>

    <!-- ═══ 账号 ═══ -->
    <template v-if="tab === 'overview'">
      <div class="overview-grid">
        <div class="overview-card">
          <span class="overview-label">账号总数</span>
          <strong>{{ userTotal }}</strong>
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
          <span class="overview-sub">LLM {{ knowledgeStats?.llm === 'configured' ? '已配置' : '未配置' }}</span>
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
      <p v-if="overviewLoading" class="setting-hint">加载中…</p>
      <p class="setting-hint">
        统计窗口 30 天，数据来自知识服务查询日志与控制面容量聚合。高频引用文档与盲区明细见知识驾驶舱。
      </p>
    </template>
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
              id="svcKnowledgeUrl"
              v-model="serviceKnowledgeUrl"
              class="setting-input"
              type="text"
              autocomplete="off"
              placeholder="http://192.168.1.10:8000"
            />
            <button class="secondary-button" type="button" :disabled="serviceBusy" @click="submitServiceConfig">
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
            id="aiLlmBaseUrl"
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
            id="aiLlmModel"
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
            id="aiLlmApiKey"
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
            :disabled="aiBusy" @click="ai.asrEnabled = !ai.asrEnabled"
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
            :disabled="aiBusy" @click="ai.asrSpeakerEnabled = !ai.asrSpeakerEnabled"
          >
            <i />
          </button>
        </div>
        <div class="admin-service-row">
          <p v-if="aiPush" :class="['setting-hint', { 'meeting-failed': !aiPushOk }]">{{ aiPush }}</p>
          <button class="secondary-button" type="button" :disabled="aiBusy" @click="submitAiConfig">
            {{ aiBusy ? '保存中…' : '保存' }}
          </button>
        </div>
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
            <button class="secondary-button compact" type="button" :disabled="memberLoading" @click="void loadSharedFiles()">
              <svg viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/></svg>
              刷新文件
            </button>
          </div>
        </header>
        <p class="setting-hint perm-hint">
          四档：可管理（含授权/删除）&gt; 可编辑 &gt; 可查看下载 &gt; 仅可查看。
          删除是可管理独占；未出现在下方列表的文件按默认规则生效（空间可写成员全权）。
          服务端护栏：不能修改自己的角色。
        </p>
        <div class="role-field">
          <span>选择文件</span>
          <select :value="memberFileId" @change="void pickMemberFile(($event.target as HTMLSelectElement).value)">
            <option value="">— 选择共享空间文件 —</option>
            <option v-for="f in sharedFiles.filter(f => f.fileId && f.fileId !== '0')" :key="f.fileId" :value="f.fileId">
              {{ f.name }}{{ f.uploadByName ? `（上传者 ${f.uploadByName}）` : '' }}
            </option>
          </select>
        </div>
        <template v-if="memberFileId">
          <div v-if="memberLoading" class="admin-empty">正在读取成员角色…</div>
          <div v-else-if="!memberRows.length" class="admin-empty">还没有显式成员角色，此文件按默认规则生效。</div>
          <div v-else class="role-list">
            <div v-for="row in memberRows" :key="row.memberType + row.memberId" class="role-row">
              <div class="role-main">
                <strong>{{ memberLabel(row) }}</strong>
                <span class="role-tag">{{ row.memberType === 'user' ? '账号' : '部门' }}</span>
                <span class="role-meta">{{ FILE_ROLE_LABELS[row.role] }}</span>
              </div>
              <div class="role-actions">
                <select
                  class="role-select" :value="row.role" :aria-label="`${memberLabel(row)} 的角色`"
                  @change="row.role = ($event.target as HTMLSelectElement).value as FileMemberRole['role']"
                >
                  <option v-for="(label, value) in FILE_ROLE_LABELS" :key="value" :value="value">{{ label }}</option>
                </select>
                <button class="secondary-button compact" type="button" @click="removeMemberRow(row)">移除</button>
              </div>
            </div>
          </div>
          <div class="role-form">
            <label class="role-field">
              <span>主体类型</span>
              <select v-model="memberAdd.memberType">
                <option value="user">账号</option>
                <option value="dept">部门</option>
              </select>
            </label>
            <label class="role-field">
              <span>成员</span>
              <select v-model="memberAdd.memberId">
                <option value="">— 选择 —</option>
                <template v-if="memberAdd.memberType === 'user'">
                  <option v-for="u in users.filter(u => u.userId !== currentUser?.userId)" :key="u.userId" :value="u.userId">
                    {{ u.nickName || u.userName }}
                  </option>
                </template>
                <template v-else>
                  <option v-for="d in depts" :key="d.deptId" :value="d.deptId">{{ d.deptName }}</option>
                </template>
              </select>
            </label>
            <label class="role-field">
              <span>角色</span>
              <select v-model="memberAdd.role">
                <option v-for="(label, value) in FILE_ROLE_LABELS" :key="value" :value="value">{{ label }}</option>
              </select>
            </label>
            <div class="role-form-actions">
              <button class="secondary-button compact" type="button" :disabled="!memberAdd.memberId" @click="addMemberRow">添加行</button>
              <button class="primary-button compact" type="button" :disabled="memberBusy || !memberRows.length" @click="void saveMemberRows()">
                {{ memberBusy ? '保存中…' : '保存成员角色' }}
              </button>
            </div>
          </div>
        </template>
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
              :class="['admin-perm', userMemberRow(user.userId)?.permission || 'none']"
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

        <div class="dept-auth">
          <div class="admin-usage-head"><span>按部门授权</span></div>
          <div class="dept-auth-form">
            <select class="dept-select" :value="deptForm.deptId" @change="onDeptChange">
              <option value="">选择部门…</option>
              <option v-for="dept in depts" :key="dept.deptId" :value="dept.deptId">{{ dept.deptName }}</option>
            </select>
            <select class="dept-select" :value="deptForm.permission" @change="onDeptPermChange">
              <option value="read">只读</option>
              <option value="write">读写</option>
            </select>
            <button class="primary-button compact" type="button" :disabled="deptBusy || !deptForm.deptId" @click="addDeptAuth">授权</button>
          </div>
          <ul v-if="deptMemberRows.length" class="admin-user-list">
            <li v-for="row in deptMemberRows" :key="row.memberId" class="admin-user-row">
              <div class="admin-user-main">
                <strong>🏢 {{ row.name || row.memberId }}</strong>
                <span>{{ row.permission === 'write' ? '读写' : '只读' }} · 整部门生效</span>
              </div>
              <div class="admin-row-actions">
                <button class="secondary-button compact" type="button" :disabled="deptBusy" @click="revokeDeptAuth(row)">撤销</button>
              </div>
            </li>
          </ul>
          <p class="setting-hint">
            部门授权对该部门全部成员生效；账号的显式授权与部门授权取宽（读写优先于只读）。
          </p>
        </div>
      </section>
    </template>

    <!-- ═══ 权限：角色组 + 成员矩阵 ═══ -->
    <template v-if="tab === 'permissions'">
      <section v-if="isSuper" class="card">
        <header class="card-header">
          <div>
            <span class="section-label">权限角色</span>
            <h2>一批人共用一套权限</h2>
          </div>
          <div class="card-actions">
            <button class="secondary-button compact" type="button" @click="startRoleForm()">新建角色</button>
          </div>
        </header>
        <p class="setting-hint perm-hint">
          成员挂了角色就按角色裁决，多个角色取并集；已挂角色的成员不再看逐账号开关。
          角色可设为「子管理员」：该成员能进入本页，按管理范围看到、修改范围内成员的功能权限
          （本部门 / 含下级 / 指定部门 / 全部成员）。
        </p>
        <div v-if="roleForm" class="role-form">
          <label class="role-field">
            <span>角色名</span>
            <input v-model="roleForm.roleName" type="text" maxlength="64" placeholder="如：车间主管、只读成员" />
          </label>
          <label class="role-field">
            <span>管理范围</span>
            <select v-model="roleForm.adminScope">
              <option v-for="opt in scopeOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
            </select>
          </label>
          <div v-if="roleForm.adminScope === 'specified'" class="role-field role-dept-field">
            <span>指定部门（至少勾选一个）</span>
            <div class="role-dept-list">
              <label v-for="dept in depts" :key="dept.deptId">
                <input
                  type="checkbox" :value="dept.deptId"
                  :checked="(roleForm.scopeDeptIds ?? []).includes(dept.deptId)"
                  @change="toggleScopeDept(dept.deptId, ($event.target as HTMLInputElement).checked)"
                />
                <span>{{ dept.deptName }}</span>
              </label>
              <div v-if="!depts.length" class="admin-empty">部门列表为空或尚未加载</div>
            </div>
          </div>
          <div class="role-flags">
            <div v-for="f in featureLabels" :key="f.key" class="role-flag">
              <button
                :class="['admin-toggle', roleForm[f.key] ? 'on' : '']"
                type="button" role="switch" :aria-checked="roleForm[f.key]"
                :aria-label="`${f.label}：${roleForm[f.key] ? '开启' : '关闭'}`"
                @click="roleForm[f.key] = !roleForm[f.key]"
              ><i /></button>
              <span>{{ f.label }}</span>
            </div>
          </div>
          <div class="role-form-actions">
            <button class="secondary-button compact" type="button" :disabled="roleBusy" @click="roleForm = null">取消</button>
            <button
              class="primary-button compact" type="button"
              :disabled="roleBusy || !roleForm.roleName.trim() || roleDeptEmpty" @click="saveRole"
            >
              {{ roleBusy ? '保存中…' : '保存角色' }}
            </button>
          </div>
        </div>
        <div v-if="!roles.length" class="admin-empty">还没有角色。成员按下方逐账号开关生效。</div>
        <div v-else class="role-list">
          <div v-for="role in roles" :key="role.roleId" class="role-row">
            <div class="role-main">
              <strong>{{ role.roleName }}</strong>
              <span v-if="role.adminScope !== 'none'" class="role-tag">{{ ADMIN_SCOPE_LABELS[role.adminScope] }}</span>
              <span class="role-meta">{{ roleSummary(role) }} · {{ role.memberCount || 0 }} 人</span>
            </div>
            <div class="role-actions">
              <button class="secondary-button compact" type="button" @click="startRoleForm(role)">编辑</button>
              <button class="secondary-button compact" type="button" @click="removeRole(role)">删除</button>
            </div>
          </div>
        </div>
      </section>

      <section class="card perm-card">
        <header class="card-header">
          <div>
            <span class="section-label">成员权限</span>
            <h2>{{ isSuper ? '每个成员能用哪些功能' : '本部门成员的功能权限' }}</h2>
          </div>
          <div class="card-actions">
            <button class="secondary-button compact" type="button" :disabled="permissionLoading" @click="openPermissions">
              <svg viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/></svg>
              刷新
            </button>
          </div>
        </header>
        <p class="setting-hint perm-hint">
          关闭后客户端隐藏入口，控制面与知识服务同时拒绝——不是只藏按钮。
          关闭「网盘」时共享空间一并停用。挂了角色的成员由角色决定，开关在这里只读。
        </p>
        <div v-if="permissionLoading && !permissionRows.length" class="admin-empty">正在读取权限…</div>
        <div v-else-if="!permissionRows.length" class="admin-empty">本范围内暂无成员。</div>
        <div v-else class="perm-table">
          <div class="perm-row perm-head" aria-hidden="true">
            <span>成员</span>
            <span>角色</span>
            <span v-for="f in featureLabels" :key="f.key">{{ f.label }}</span>
            <span></span>
          </div>
          <div v-for="row in permissionRows" :key="row.member.userId" class="perm-row">
            <div class="perm-user">
              <strong>
                {{ row.member.nickName || row.member.userName }}
                <span v-if="row.member.adminScope !== 'none'" class="role-tag">{{ ADMIN_SCOPE_LABELS[row.member.adminScope] }}</span>
                <span v-if="row.member.status === '1'" class="role-tag muted">停用</span>
              </strong>
              <span>{{ row.member.userName }}<template v-if="row.member.deptName"> · {{ row.member.deptName }}</template></span>
            </div>
            <div class="perm-role-cell">
              <details v-if="isSuper" class="perm-role-details">
                <summary>{{ row.member.roleNames?.length ? row.member.roleNames.join('、') : '未分配' }}</summary>
                <div class="perm-role-menu">
                  <label v-for="role in roles" :key="role.roleId">
                    <input
                      type="checkbox" :value="role.roleId"
                      :checked="(roleDraft[row.member.userId] ?? []).includes(role.roleId ?? '')"
                      @change="toggleRoleDraft(row, role.roleId ?? '', ($event.target as HTMLInputElement).checked)"
                    />
                    <span>{{ role.roleName }}</span>
                  </label>
                  <button class="secondary-button compact" type="button" :disabled="permissionBusy === row.member.userId" @click="saveMemberRoles(row)">保存角色</button>
                </div>
              </details>
              <span v-else class="perm-role-static">{{ row.member.roleNames?.length ? row.member.roleNames.join('、') : '未分配' }}</span>
            </div>
            <span v-for="f in featureLabels" :key="f.key" class="perm-cell">
              <button
                :class="['admin-toggle', row.flags[f.key] ? 'on' : '']"
                type="button" role="switch" :aria-checked="row.flags[f.key]"
                :aria-label="`${f.label}：${row.flags[f.key] ? '开启' : '关闭'}`"
                :title="roleDriven(row) ? `由角色「${row.member.roleNames.join('、')}」决定` : (f.key === 'shared' && !row.flags.drive ? '网盘关闭时共享空间不可用' : '')"
                :disabled="roleDriven(row) || permissionBusy === row.member.userId || (f.key === 'shared' && !row.flags.drive)"
                @click="toggleFeature(row, f.key)"
              ><i /></button>
            </span>
            <span class="perm-cell">
              <button
                class="secondary-button compact" type="button"
                :disabled="roleDriven(row) || !row.dirty || permissionBusy === row.member.userId"
                @click="savePermissions(row)"
              >{{ permissionBusy === row.member.userId ? '保存中…' : roleDriven(row) ? '由角色决定' : row.dirty ? '保存' : '已保存' }}</button>
            </span>
          </div>
        </div>
      </section>
    </template>
  </div>
</template>
