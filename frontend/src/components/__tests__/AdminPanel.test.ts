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

vi.mock('../../services/core', () => ({
  core: {
    adminListUsers: () => Promise.resolve({
      total: 1,
      rows: [{
        userId: '1761300000000000042',
        userName: 'zhang',
        nickName: '小张',
        deptName: '',
        status: '0',
        createTime: '',
        loginDate: '',
      }],
    }),
    adminRegisterEnabled: () => Promise.resolve(false),
    adminListSpaces: () => adminListSpaces(),
    adminSharedMembers: () => adminSharedMembers(),
    adminListDepts: () => adminListDepts(),
    adminCapacity: () => adminCapacity(),
    adminSetPersonalQuota: (...args: unknown[]) => adminSetPersonalQuota(...args),
    adminSetSharedQuota: (...args: unknown[]) => adminSetSharedQuota(...args),
    adminGrantShared: (...args: unknown[]) => adminGrantShared(...args),
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
  // 切到「空间」页签
  const tabs = wrapper.findAll('.admin-tab')
  await tabs[1].trigger('click')
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
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
