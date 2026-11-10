import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdminPanel from '../AdminPanel.vue'

const GB = 1024 * 1024 * 1024

const adminSetPersonalQuota = vi.fn()
const adminSetSharedQuota = vi.fn()
const adminGrantShared = vi.fn()
const adminListSpaces = vi.fn()
const adminSharedMembers = vi.fn()
const adminCapacity = vi.fn()
const adminListDepts = vi.fn()
const currentUser = vi.fn()
const adminPermissionMembers = vi.fn()
const adminPermissionRoles = vi.fn()
const adminAssignRoles = vi.fn()
const adminSavePermissionRole = vi.fn()
const adminDeletePermissionRole = vi.fn()
const adminListUsers = vi.fn()
const sharedFileList = vi.fn()
const fileMemberList = vi.fn()
const fileMemberReplace = vi.fn()

const defaultUserRows = [{
  userId: '1761300000000000042',
  userName: 'zhang',
  nickName: '小张',
  deptName: '',
  status: '0',
  createTime: '',
  loginDate: '',
}]

vi.mock('../../services/core', () => ({
  core: {
    currentUser: () => currentUser(),
    adminListUsers: () => adminListUsers(),
    adminRegisterEnabled: () => Promise.resolve(false),
    adminListSpaces: () => adminListSpaces(),
    adminSharedMembers: () => adminSharedMembers(),
    adminListDepts: () => adminListDepts(),
    adminKnowledgeStats: () => Promise.resolve(null),
    adminCapacity: () => adminCapacity(),
    adminSetPersonalQuota: (...args: unknown[]) => adminSetPersonalQuota(...args),
    adminSetSharedQuota: (...args: unknown[]) => adminSetSharedQuota(...args),
    adminGrantShared: (...args: unknown[]) => adminGrantShared(...args),
    adminPermissionMembers: () => adminPermissionMembers(),
    adminPermissionRoles: () => adminPermissionRoles(),
    adminAssignRoles: (...args: unknown[]) => adminAssignRoles(...args),
    adminSavePermissionRole: (...args: unknown[]) => adminSavePermissionRole(...args),
    adminDeletePermissionRole: (...args: unknown[]) => adminDeletePermissionRole(...args),
    sharedFileList: () => sharedFileList(),
    fileMemberList: () => fileMemberList(),
    fileMemberReplace: (...args: unknown[]) => fileMemberReplace(...args),
  },
}))

const sharedSpace = {
  spaceId: '1',
  spaceType: 'shared' as const,
  ownerId: '0',
  spaceName: '共享空间',
  quotaBytes: 10 * GB,
  usedBytes: 2 * GB,
  status: '0',
  permission: 'write' as const,
}

const mountSpaces = async () => {
  const wrapper = mount(AdminPanel)
  await new Promise(resolve => setTimeout(resolve, 0))
  // 切到「空间」页签（概览 Tab 加入后按文本找，避免索引漂移）
  const tabs = wrapper.findAll('.admin-tab')
  const spacesTab = tabs.find(b => b.text() === '空间')
  await (spacesTab ?? tabs[1]).trigger('click')
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  currentUser.mockResolvedValue({
    loggedIn: true,
    userId: '1761100000000000001',
    userName: 'admin',
    nickName: '管理员',
    avatar: '',
    isAdmin: true,
    adminScope: 'all',
    features: { knowledge: true, meeting: true, drive: true, shared: true, plugin: true },
  })
  adminPermissionMembers.mockResolvedValue([])
  adminListUsers.mockResolvedValue({ total: 1, rows: defaultUserRows })
  sharedFileList.mockResolvedValue([])
  fileMemberList.mockResolvedValue([])
  adminPermissionRoles.mockResolvedValue([])
  adminSetPersonalQuota.mockResolvedValue(undefined)
  adminSetSharedQuota.mockResolvedValue(undefined)
  adminGrantShared.mockResolvedValue(undefined)
  adminSharedMembers.mockResolvedValue([])
  adminListDepts.mockResolvedValue([])
  adminListSpaces.mockResolvedValue([sharedSpace])
  adminCapacity.mockResolvedValue({
    enabled: true,
    usableBytes: 100 * GB,
    poolBytes: 95 * GB,
    reservedBytes: 5 * GB,
    committedBytes: 10 * GB,
    usedBytes: 2 * GB,
    unlimitedCount: 0,
  })
})

describe('AdminPanel 空间分配', () => {
  it('把 GB 输入换算成字节后提交', async () => {
    const wrapper = await mountSpaces()
    const input = wrapper.findAll('input[type="number"]')[0]
    await input.setValue('20')
    await wrapper.findAll('.admin-space-form .primary-button')[0].trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(adminSetSharedQuota).toHaveBeenCalledWith(20 * GB)
  })

  it('留空表示收回配额，提交 0 而不是跳过请求', async () => {
    const wrapper = await mountSpaces()
    const input = wrapper.findAll('input[type="number"]')[0]
    await input.setValue('')
    await wrapper.findAll('.admin-space-form .primary-button')[0].trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(adminSetSharedQuota).toHaveBeenCalledWith(0)
  })

  it('负数被拒，不发请求', async () => {
    const wrapper = await mountSpaces()
    const input = wrapper.findAll('input[type="number"]')[0]
    await input.setValue('-5')
    await wrapper.findAll('.admin-space-form .primary-button')[0].trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(adminSetSharedQuota).not.toHaveBeenCalled()
    expect(wrapper.find('.admin-message.error').exists()).toBe(true)
  })

  it('个人配额按雪花 ID 字符串提交，不转数字', async () => {
    const wrapper = await mountSpaces()
    const rows = wrapper.findAll('.space-row')
    await rows[0].find('input[type="number"]').setValue('5')
    await rows[0].find('.primary-button').trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(adminSetPersonalQuota).toHaveBeenCalledWith('1761300000000000042', 5 * GB)
    // 关键：ID 必须是字符串，转成 number 会精度丢失指向别的账号
    expect(typeof adminSetPersonalQuota.mock.calls[0][0]).toBe('string')
  })

  it('共享权限按 无 → 只读 → 读写 → 无 轮转', async () => {
    // 组件授权后会重新拉列表：mock 按调用次序返回逐步推进的授权行
    const member = (permission: string) => [{
      memberType: 'user', memberId: '1761300000000000042', permission, name: '',
    }]
    adminSharedMembers
      .mockResolvedValueOnce(member(''))
      .mockResolvedValueOnce(member('read'))
      .mockResolvedValueOnce(member('write'))
      .mockResolvedValueOnce(member(''))

    const wrapper = await mountSpaces()
    const perm = wrapper.find('.admin-perm')
    expect(perm.text()).toBe('无权限')

    await perm.trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(adminGrantShared).toHaveBeenLastCalledWith('user', '1761300000000000042', 'read')

    await wrapper.find('.admin-perm').trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(adminGrantShared).toHaveBeenLastCalledWith('user', '1761300000000000042', 'write')

    await wrapper.find('.admin-perm').trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(adminGrantShared).toHaveBeenLastCalledWith('user', '1761300000000000042', '')
  })

  it('未分配空间的账号显示待开空间', async () => {
    const wrapper = await mountSpaces()
    expect(wrapper.find('.space-row .admin-space-cell.pending').text()).toBe('待开空间')
  })

  it('账号页签显示真实配额，不是硬编码的待开空间', async () => {
    // 曾经这一格是写死的「待开空间」，已分配配额的账号也照样显示未开通。
    // 数据没错、只是没接上——这种错看着像正常，所以钉住它。
    adminListSpaces.mockResolvedValue([
      sharedSpace,
      {
        spaceId: '2',
        spaceType: 'personal' as const,
        ownerId: '1761300000000000042',
        spaceName: '个人空间',
        quotaBytes: 10 * GB,
        usedBytes: 3 * GB,
        status: '0',
        permission: 'owner' as const,
      },
    ])
    const wrapper = mount(AdminPanel)
    await new Promise(resolve => setTimeout(resolve, 0))
    await new Promise(resolve => setTimeout(resolve, 0))

    // 停在账号页签（默认页签），不切到空间页签
    const cell = wrapper.find('.admin-space-cell')
    expect(cell.classes()).toContain('granted')
    expect(cell.text()).toBe('3.00 GB / 10.00 GB')
  })

  it('未超配时不显示警告', async () => {
    const wrapper = await mountSpaces()
    expect(wrapper.find('.capacity-note.warn').exists()).toBe(false)
    expect(wrapper.find('.capacity-item strong.over').exists()).toBe(false)
  })

  it('超配时显示警告并标红已承诺', async () => {
    // 承诺 200 GB 而池只有 95 GB：管理员必须看得见，否则用户会看到
    // 「配额还剩很多」却传不上去
    adminCapacity.mockResolvedValue({
      enabled: true,
      usableBytes: 100 * GB,
      poolBytes: 95 * GB,
      reservedBytes: 5 * GB,
      committedBytes: 200 * GB,
      usedBytes: 2 * GB,
      unlimitedCount: 0,
    })
    const wrapper = await mountSpaces()
    expect(wrapper.find('.capacity-note.warn').exists()).toBe(true)
    expect(wrapper.find('.capacity-item strong.over').exists()).toBe(true)
  })

  it('未配置探测路径时说明池上限不生效', async () => {
    // 这种状态下可以分配出超过磁盘容量的配额，必须讲清楚而不是假装一切正常
    adminCapacity.mockResolvedValue({
      enabled: false,
      usableBytes: -1,
      poolBytes: -1,
      reservedBytes: 5 * GB,
      committedBytes: 10 * GB,
      usedBytes: 0,
      unlimitedCount: 0,
    })
    const wrapper = await mountSpaces()
    const note = wrapper.find('.capacity-note')
    expect(note.exists()).toBe(true)
    expect(note.text()).toContain('池上限不生效')
    // 未启用时不该谎报一个可分配容量
    expect(wrapper.text()).toContain('未探测')
  })

  it('有「不限」配额时说明已承诺数字偏小', async () => {
    adminCapacity.mockResolvedValue({
      enabled: true,
      usableBytes: 100 * GB,
      poolBytes: 95 * GB,
      reservedBytes: 5 * GB,
      committedBytes: 10 * GB,
      usedBytes: 0,
      unlimitedCount: 2,
    })
    const wrapper = await mountSpaces()
    expect(wrapper.text()).toContain('2 个空间配额为「不限」')
  })

  it('读接口失败时报错，不静默留空', async () => {
    adminListSpaces.mockRejectedValue(new Error('没有管理权限'))
    const wrapper = await mountSpaces()
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(wrapper.find('.admin-message.error').text()).toContain('没有管理权限')
  })
})

const member = (overrides: Record<string, unknown> = {}) => ({
  userId: '1761300000000000042',
  userName: 'zhang',
  nickName: '小张',
  deptId: '10',
  deptName: '车间',
  status: '0',
  knowledge: true,
  meeting: true,
  drive: true,
  shared: true,
  plugin: true,
  roleIds: [] as string[],
  roleNames: [] as string[],
  adminScope: 'none',
  ...overrides,
})

const role = (overrides: Record<string, unknown> = {}) => ({
  roleId: '7',
  roleName: '只读成员',
  adminScope: 'none',
  knowledge: true,
  meeting: false,
  drive: true,
  shared: true,
  plugin: true,
  memberCount: 1,
  ...overrides,
})

const mountPermissions = async () => {
  const wrapper = mount(AdminPanel)
  await new Promise(resolve => setTimeout(resolve, 0))
  await new Promise(resolve => setTimeout(resolve, 0))
  const tab = wrapper.findAll('.admin-tab').find(b => b.text() === '权限')
  await tab!.trigger('click')
  await new Promise(resolve => setTimeout(resolve, 0))
  return wrapper
}

describe('AdminPanel 权限角色', () => {
  it('挂了角色的成员开关只读，保存按钮说明由角色决定', async () => {
    adminPermissionMembers.mockResolvedValue([
      member({ roleIds: ['7'], roleNames: ['只读成员'] }),
    ])
    adminPermissionRoles.mockResolvedValue([role()])

    const wrapper = await mountPermissions()
    const row = wrapper.find('.perm-row:not(.perm-head)')
    const toggles = row.findAll('.admin-toggle')
    expect(toggles).toHaveLength(5)
    expect(toggles.every(t => t.attributes('disabled') !== undefined)).toBe(true)
    expect(row.text()).toContain('由角色决定')
  })

  it('勾选角色后提交角色 ID 列表', async () => {
    adminPermissionMembers.mockResolvedValue([member()])
    adminPermissionRoles.mockResolvedValue([role()])
    adminAssignRoles.mockResolvedValue(undefined)

    const wrapper = await mountPermissions()
    const row = wrapper.find('.perm-row:not(.perm-head)')
    await row.find('.perm-role-menu input[type="checkbox"]').setValue(true)
    await row.find('.perm-role-menu .secondary-button').trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(adminAssignRoles).toHaveBeenCalledWith('1761300000000000042', ['7'])
  })

  it('子管理员只看到权限页签，且没有角色管理入口', async () => {
    currentUser.mockResolvedValue({
      loggedIn: true,
      userId: '1761300000000000099',
      userName: 'sub',
      nickName: '子管理员',
      avatar: '',
      isAdmin: false,
      adminScope: 'dept',
      features: { knowledge: true, meeting: true, drive: true, shared: true, plugin: true },
    })
    adminPermissionMembers.mockResolvedValue([member()])

    const wrapper = await mountPermissions()
    const labels = wrapper.findAll('.admin-tab').map(t => t.text())
    expect(labels).toEqual(['权限'])
    expect(wrapper.text()).not.toContain('新建角色')
    // 角色列只读展示，不出现勾选菜单
    expect(wrapper.find('.perm-role-details').exists()).toBe(false)
  })

  it('指定部门范围：勾选部门后随角色提交，未勾选时不能保存', async () => {
    adminPermissionMembers.mockResolvedValue([member()])
    adminPermissionRoles.mockResolvedValue([])
    adminListDepts.mockResolvedValue([
      { deptId: '10', deptName: '车间' },
      { deptId: '20', deptName: '质检' },
    ])

    const wrapper = await mountPermissions()
    const newBtn = wrapper.findAll('button').find(b => b.text() === '新建角色')!
    await newBtn.trigger('click')
    const form = wrapper.find('.role-form')
    await form.find('input[type="text"]').setValue('质检管理')
    await form.find('select').setValue('specified')
    const saveBtn = form.findAll('button').find(b => b.text() === '保存角色')!

    // 指定部门档至少要勾一个部门，否则不给提交
    expect(saveBtn.attributes('disabled')).toBeDefined()
    await form.findAll('.role-dept-list input[type="checkbox"]')[0].setValue(true)
    expect(saveBtn.attributes('disabled')).toBeUndefined()
    await saveBtn.trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(adminSavePermissionRole).toHaveBeenCalledWith(
      expect.objectContaining({ adminScope: 'specified', scopeDeptIds: ['10'] }))
  })

  it('本部门及下级档位保存时不携带部门列表', async () => {
    adminPermissionMembers.mockResolvedValue([member()])
    adminPermissionRoles.mockResolvedValue([])

    const wrapper = await mountPermissions()
    const newBtn = wrapper.findAll('button').find(b => b.text() === '新建角色')!
    await newBtn.trigger('click')
    const form = wrapper.find('.role-form')
    await form.find('input[type="text"]').setValue('华东管理')
    await form.find('select').setValue('dept_children')
    // 指定部门的勾选列表只在 specified 档出现
    expect(form.find('.role-dept-list').exists()).toBe(false)
    await form.findAll('button').find(b => b.text() === '保存角色')!.trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(adminSavePermissionRole).toHaveBeenCalledWith(
      expect.objectContaining({ adminScope: 'dept_children' }))
    const payload = adminSavePermissionRole.mock.calls[0][0] as Record<string, unknown>
    expect('scopeDeptIds' in payload).toBe(false)
  })
})

describe('AdminPanel 文件访问角色', () => {
  it('选文件载入成员行，添加行后整组保存', async () => {
    sharedFileList.mockResolvedValue([
      { key: 'docs/报告.pdf', name: '报告.pdf', size: 1, uploadByName: '小张', fileId: '1761000000000000100' },
    ])
    fileMemberList.mockResolvedValue([
      { memberType: 'user', memberId: '1761300000000000042', role: 'read' },
    ])
    fileMemberReplace.mockResolvedValue(undefined)
    // 用户表给两个人：已有行是小张，新加的是小李
    adminListUsers.mockResolvedValue({
      total: 2,
      rows: [
        { userId: '1761300000000000042', userName: 'zhang', nickName: '小张', deptName: '', status: '0', createTime: '', loginDate: '' },
        { userId: '1761300000000000043', userName: 'li', nickName: '小李', deptName: '', status: '0', createTime: '', loginDate: '' },
      ],
    })

    const wrapper = await mountSpaces()
    const card = wrapper.findAll('section').find(s => s.text().includes('文件访问角色'))!
    // fileId 走字符串（雪花 ID 经 number 会撞值），未登记行（fileId=0）不进下拉
    await card.find('select').setValue('1761000000000000100')
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(card.text()).toContain('仅可查看')

    // 添加行：成员选小李，档位 manage
    const addSelects = card.findAll('.role-form select')
    await addSelects[1].setValue('1761300000000000043')
    await addSelects[2].setValue('manage')
    await card.findAll('button').find(b => b.text() === '添加行')!.trigger('click')
    await card.findAll('button').find(b => b.text() === '保存成员角色')!.trigger('click')
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(fileMemberReplace).toHaveBeenCalledWith('1761000000000000100', [
      { memberType: 'user', memberId: '1761300000000000042', role: 'read' },
      { memberType: 'user', memberId: '1761300000000000043', role: 'manage' },
    ])
  })

  it('重复主体不重复添加', async () => {
    sharedFileList.mockResolvedValue([
      { key: 'a.txt', name: 'a.txt', size: 1, uploadByName: '', fileId: '1761000000000000101' },
    ])
    fileMemberList.mockResolvedValue([
      { memberType: 'user', memberId: '1761300000000000042', role: 'download' },
    ])

    const wrapper = await mountSpaces()
    const card = wrapper.findAll('section').find(s => s.text().includes('文件访问角色'))!
    await card.find('select').setValue('1761000000000000101')
    await new Promise(resolve => setTimeout(resolve, 0))
    const addSelects = card.findAll('.role-form select')
    await addSelects[1].setValue('1761300000000000042')
    await addSelects[2].setValue('manage')
    await card.findAll('button').find(b => b.text() === '添加行')!.trigger('click')

    // 已有行 → 全局消息区报错提示，且行数不翻倍
    expect(wrapper.text()).toContain('该成员已有角色行')
    expect(card.findAll('.role-row').length).toBe(1)
  })
})
