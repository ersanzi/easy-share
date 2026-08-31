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
  logout: vi.fn(),
  openAdminConsole: vi.fn(),
  onFileDrop: vi.fn(),
  onFileDropOff: vi.fn(),
  eventsOn: vi.fn(),
  eventsOff: vi.fn(),
}))

vi.mock('../../services/core', () => ({ core: mocks }))
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
    <button data-test="login" @click="login('admin', 'admin123')">login</button>
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
    vi.spyOn(window, 'confirm').mockReturnValue(true)
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

  it('surfaces admin console open failures as operation errors', async () => {
    mocks.openAdminConsole.mockRejectedValueOnce(new Error('管理后台地址无效（需 http/https）：ftp://x'))

    const wrapper = mount(AuthHarness)
    await flushPromises()
    await wrapper.get('[data-test="open-admin"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="error"]').text()).toContain('管理后台地址无效')
    wrapper.unmount()
  })
})
