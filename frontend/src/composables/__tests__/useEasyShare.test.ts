import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useEasyShare } from '../useEasyShare'

const mocks = vi.hoisted(() => ({
  snapshot: vi.fn(),
  shutdown: vi.fn(),
  selectFile: vi.fn(),
  send: vi.fn(),
  accept: vi.fn(),
  reject: vi.fn(),
  startDrive: vi.fn(),
  stopDrive: vi.fn(),
  reportError: vi.fn().mockResolvedValue(undefined),
  logDirectory: vi.fn(),
  currentUser: vi.fn().mockResolvedValue({ loggedIn: false, userName: '', nickName: '', avatar: '', isAdmin: false }),
  login: vi.fn(),
  mySpaces: vi.fn().mockResolvedValue([]),
  logout: vi.fn(),
  openAdminConsole: vi.fn(),
  onFileDrop: vi.fn(),
  onFileDropOff: vi.fn(),
  eventsOn: vi.fn(),
  eventsOff: vi.fn(),
}))

vi.mock('../../services/core', () => ({ core: mocks }))
// 退出确认已迁移到全局 ConfirmDialog（useConfirm）：测试中直接按「确认」了结
vi.mock('../useConfirm', () => ({
  useConfirm: () => ({
    confirmState: { open: false, title: '', message: '', confirmText: '确认', danger: false },
    confirm: vi.fn().mockResolvedValue(true),
    settle: vi.fn(),
  }),
}))
vi.mock('../../../wailsjs/runtime/runtime', () => ({
  OnFileDrop: mocks.onFileDrop,
  OnFileDropOff: mocks.onFileDropOff,
  EventsOn: mocks.eventsOn,
  EventsOff: mocks.eventsOff,
}))

const runningSnapshot = {
  status: { core: true, discovery: true, receiver: true, webdav: true, cloudEnabled: false },
  peers: [],
  tasks: [],
}

const Harness = defineComponent({
  setup: useEasyShare,
  template: `
    <span data-test="error">{{ error }}</span>
    <button data-test="shutdown" @click="shutdown">shutdown</button>
  `,
})

// AuthHarness 暴露登录态与管理入口，用于验证「管理」按钮的显隐条件。
const AuthHarness = defineComponent({
  setup: useEasyShare,
  template: `
    <span data-test="error">{{ error }}</span>
    <span data-test="admin">{{ currentUser.isAdmin }}</span>
    <span data-test="logged-in">{{ currentUser.loggedIn }}</span>
    <span data-test="feature-drive">{{ currentUser.features?.drive }}</span>
    <span data-test="task-state">{{ snapshot.tasks[0]?.status }}</span>
    <span data-test="task-names">{{ snapshot.tasks.map(task => task.fileName).join(",") }}</span>
    <button data-test="login" @click="login('admin', 'fixture-only-password')">login</button>
    <button data-test="logout" @click="logout">logout</button>
    <button data-test="open-admin" @click="openAdminConsole">admin</button>
  `,
})

describe('useEasyShare', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    mocks.snapshot.mockResolvedValue(runningSnapshot)
    mocks.shutdown.mockResolvedValue(undefined)
    mocks.reportError.mockResolvedValue(undefined)
    mocks.logDirectory.mockResolvedValue('C:\\Users\\tester\\EasyShare\\logs')
    mocks.currentUser.mockReset().mockResolvedValue({ loggedIn: false, userName: '', nickName: '', avatar: '', isAdmin: false })

  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('waits for the first successful Core snapshot', async () => {
    mocks.snapshot
      .mockRejectedValueOnce(new Error('core unavailable'))
      .mockResolvedValue(runningSnapshot)

    const wrapper = mount(Harness)
    await flushPromises()
    expect(mocks.snapshot).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(mocks.snapshot).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  // 冷启动：前端先挂载时 Core 还没就绪，Go 侧也还没恢复落盘会话。
  // Core 就绪后必须重新取一次登录态，否则有效会话会被显示成登录页、逼用户重登。
  it('re-reads auth state once Core becomes ready', async () => {
    mocks.snapshot
      .mockRejectedValueOnce(new Error('core unavailable'))
      .mockResolvedValue(runningSnapshot)
    mocks.currentUser
      .mockResolvedValueOnce({ loggedIn: false, userName: '', nickName: '', avatar: '', isAdmin: false })
      .mockResolvedValue({ loggedIn: true, userName: 'admin', nickName: '管理员', avatar: '', isAdmin: true })

    const wrapper = mount(AuthHarness)
    await flushPromises()
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('false')

    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('true')
    wrapper.unmount()
    mocks.currentUser.mockResolvedValue({ loggedIn: false, userName: '', nickName: '', avatar: '', isAdmin: false })
  })

  it('stops polling before ShutdownAll and never contacts the exited Core again', async () => {
    const wrapper = mount(Harness)
    await flushPromises()
    expect(mocks.snapshot).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="shutdown"]').trigger('click')
    await flushPromises()
    expect(mocks.shutdown).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.snapshot).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="shutdown"]').trigger('click')
    await flushPromises()
    expect(mocks.shutdown).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('writes snapshot failures to the desktop runtime log', async () => {
    const failure = new Error('snapshot failed')
    mocks.snapshot.mockRejectedValueOnce(failure)

    const wrapper = mount(Harness)
    await flushPromises()

    expect(mocks.reportError).toHaveBeenCalledWith(
      '刷新状态: snapshot failed',
      expect.stringContaining('snapshot failed'),
    )
    wrapper.unmount()
  })

  // 「管理」入口的显隐由 isAdmin 决定，登出后必须收起——否则会留一个非管理员点得到的入口。
  it('tracks isAdmin across login and clears it on logout', async () => {
    mocks.login.mockResolvedValue({
      loggedIn: true, userName: 'admin', nickName: '管理员', avatar: '', isAdmin: true,
    })
    mocks.logout.mockResolvedValue(undefined)

    const wrapper = mount(AuthHarness)
    await flushPromises()
    expect(wrapper.get('[data-test="admin"]').text()).toBe('false')

    await wrapper.get('[data-test="login"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="admin"]').text()).toBe('true')

    await wrapper.get('[data-test="logout"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="admin"]').text()).toBe('false')
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('false')
    wrapper.unmount()
  })

  it('updates the frontend when the saved session restores after the initial query', async () => {
    const wrapper = mount(AuthHarness)
    await flushPromises()
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('false')

    const restored = mocks.eventsOn.mock.calls.find(call => call[0] === 'auth:restored')![1]
    restored({ loggedIn: true, userName: 'admin', nickName: '管理员', avatar: '', isAdmin: true })
    await flushPromises()
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('true')
    expect(wrapper.get('[data-test="admin"]').text()).toBe('true')
    wrapper.unmount()
    expect(mocks.eventsOff).toHaveBeenCalledWith('auth:restored')
  })

  it('does not let an earlier auth query overwrite restored or expired state', async () => {
    let resolveInitial!: (user: { loggedIn: boolean; userName: string; nickName: string; avatar: string; isAdmin: boolean }) => void
    mocks.currentUser.mockReturnValueOnce(new Promise(resolve => { resolveInitial = resolve }))
    const wrapper = mount(AuthHarness)
    await flushPromises()
    const restored = mocks.eventsOn.mock.calls.find(call => call[0] === 'auth:restored')![1]
    const expired = mocks.eventsOn.mock.calls.find(call => call[0] === 'auth:expired')![1]
    restored({ loggedIn: true, userName: 'admin', nickName: '管理员', avatar: '', isAdmin: true })
    resolveInitial({ loggedIn: false, userName: '', nickName: '', avatar: '', isAdmin: false })
    await flushPromises()
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('true')
    expired()
    await flushPromises()
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('false')
    wrapper.unmount()
  })

  it('does not restore old auth after a later logout during profile refresh', async () => {
    const wrapper = mount(AuthHarness)
    await flushPromises()
    let resolveProfile!: (user: { loggedIn: boolean; userName: string; nickName: string; avatar: string; isAdmin: boolean }) => void
    mocks.currentUser.mockReturnValueOnce(new Promise(resolve => { resolveProfile = resolve }))
    const updated = mocks.eventsOn.mock.calls.find(call => call[0] === 'auth:user-updated')![1]
    updated()
    mocks.logout.mockResolvedValue(undefined)
    await wrapper.get('[data-test="logout"]').trigger('click')
    resolveProfile({ loggedIn: true, userName: 'old', nickName: '旧账号', avatar: '', isAdmin: true })
    await flushPromises()
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('false')
    wrapper.unmount()
  })

  it('surfaces admin console open failures as operation errors', async () => {
    mocks.openAdminConsole.mockRejectedValueOnce(new Error('管理后台地址无效（需 http/https）：ftp://x'))

    const wrapper = mount(AuthHarness)
    await flushPromises()
    await wrapper.get('[data-test="open-admin"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="error"]').text()).toContain('管理后台地址无效')
    wrapper.unmount()
  })
  it('权限刷新失败收回入口，不保留之前的管理员能力', async () => {
    mocks.currentUser.mockResolvedValue({ loggedIn: true, userId: '7', userName: 'admin', isAdmin: true,
      features: { drive: true, shared: true, knowledge: true, meeting: true, plugin: true } })
    const wrapper = mount(AuthHarness)
    await flushPromises()
    expect(wrapper.get('[data-test="admin"]').text()).toBe('true')
    mocks.currentUser.mockRejectedValueOnce(new Error('bridge unavailable'))
    mocks.eventsOn.mock.calls.find(call => call[0] === 'auth:user-updated')![1]()
    await flushPromises()
    expect(wrapper.get('[data-test="admin"]').text()).toBe('false')
    expect(wrapper.get('[data-test="feature-drive"]').text()).toBe('false')
    expect(wrapper.get('[data-test="error"]').text()).toContain('无法确认访问权限')
    wrapper.unmount()
  })

  it('迟到的退出响应不覆盖后来登录的账号', async () => {
    let finishLogout!: () => void
    mocks.logout.mockReturnValueOnce(new Promise<void>(resolve => { finishLogout = resolve }))
    mocks.login.mockResolvedValue({ loggedIn: true, userName: 'new', isAdmin: true })
    const wrapper = mount(AuthHarness)
    await flushPromises()
    await wrapper.get('[data-test="logout"]').trigger('click')
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('false')
    await wrapper.get('[data-test="login"]').trigger('click')
    await flushPromises()
    finishLogout()
    await flushPromises()
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('true')
    wrapper.unmount()
  })

  it('退出后的迟到登录失败不污染新状态', async () => {
    let failLogin!: (reason: Error) => void
    mocks.login.mockReturnValueOnce(new Promise((_resolve, reject) => { failLogin = reject }))
    mocks.logout.mockResolvedValue(undefined)
    const wrapper = mount(AuthHarness)
    await flushPromises()
    await wrapper.get('[data-test="login"]').trigger('click')
    await wrapper.get('[data-test="logout"]').trigger('click')
    failLogin(new Error('old login failed'))
    await flushPromises()
    expect(wrapper.get('[data-test="error"]').text()).not.toContain('old login failed')
    expect(wrapper.get('[data-test="logged-in"]').text()).toBe('false')
    wrapper.unmount()
  })

  it('换号立即清除云任务；旧快照和退出后的云事件不能恢复私有文件名', async () => {
    const user = { loggedIn: true, userId: 'alice', userName: 'alice', features: { drive: true } }
    const lan = { id: 'lan', fileName: '附近文件', status: 'completed', kind: 'lan_send' }
    const cloud = { id: 'cloud', fileName: '甲的机密文件', status: 'completed', kind: 'cloud_download', ownerId: 'alice-scope' }
    mocks.currentUser.mockResolvedValue(user)
    mocks.snapshot.mockResolvedValue({ ...runningSnapshot, tasks: [cloud, lan] })
    const wrapper = mount(AuthHarness)
    await flushPromises()
    expect(wrapper.get('[data-test="task-names"]').text()).toContain('甲的机密文件')
    const event = mocks.eventsOn.mock.calls.find(call => call[0] === 'core-event')![1]
    let finishOld!: (value: unknown) => void
    mocks.snapshot.mockReturnValueOnce(new Promise(resolve => { finishOld = resolve }))
    event(JSON.stringify({ type: 'status.changed' }))
    await flushPromises()
    mocks.logout.mockResolvedValue(undefined)
    mocks.snapshot.mockResolvedValue({ ...runningSnapshot, tasks: [lan] })
    await wrapper.get('[data-test="logout"]').trigger('click')
    expect(wrapper.get('[data-test="task-names"]').text()).toBe('附近文件')
    event(JSON.stringify({ type: 'transfer.updated', data: cloud }))
    finishOld({ ...runningSnapshot, tasks: [cloud, lan] })
    await flushPromises()
    expect(wrapper.get('[data-test="task-names"]').text()).toBe('附近文件')
    wrapper.unmount()
  })

  it('合并重复快照请求，实时完成事件不被旧快照回滚', async () => {
    const wrapper = mount(AuthHarness)
    await flushPromises()
    const event = mocks.eventsOn.mock.calls.find(call => call[0] === 'core-event')![1]
    let resolveSnapshot!: (value: unknown) => void
    mocks.snapshot.mockReturnValueOnce(new Promise(resolve => { resolveSnapshot = resolve }))
    const calls = mocks.snapshot.mock.calls.length
    event(JSON.stringify({ type: 'status.changed' }))
    event(JSON.stringify({ type: 'status.changed' }))
    await flushPromises()
    expect(mocks.snapshot.mock.calls.length).toBe(calls + 1)
    event(JSON.stringify({ type: 'transfer.updated', data: { id: 'task', status: 'completed' } }))
    resolveSnapshot({ ...runningSnapshot, tasks: [{ id: 'task', status: 'running' }] })
    await flushPromises()
    expect(wrapper.get('[data-test="task-state"]').text()).toBe('completed')
    wrapper.unmount()
  })
})
