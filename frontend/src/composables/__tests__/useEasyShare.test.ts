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
  onFileDrop: vi.fn(),
  onFileDropOff: vi.fn(),
}))

vi.mock('../../services/core', () => ({ core: mocks }))
vi.mock('../../../wailsjs/runtime/runtime', () => ({
  OnFileDrop: mocks.onFileDrop,
  OnFileDropOff: mocks.onFileDropOff,
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
})
