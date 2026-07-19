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
  mapDrive: vi.fn(),
  unmapDrive: vi.fn(),
  reportError: vi.fn().mockResolvedValue(undefined),
  logDirectory: vi.fn(),
}))

vi.mock('../../services/core', () => ({ core: mocks }))

const unmappedSnapshot = {
  status: { core: true, discovery: true, receiver: true, webdav: false, driveMapped: false },
  peers: [],
  tasks: [],
}
const mappedSnapshot = {
  ...unmappedSnapshot,
  status: { ...unmappedSnapshot.status, webdav: true, driveMapped: true },
}

const Harness = defineComponent({
  setup: useEasyShare,
  template: `
    <span data-test="error">{{ error }}</span>
    <span data-test="mapping">{{ mapping }}</span>
    <button data-test="map" @click="mapDrive">map</button>
    <button data-test="unmap" @click="unmapDrive">unmap</button>
    <button data-test="shutdown" @click="shutdown">shutdown</button>
  `,
})

describe('useEasyShare', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    mocks.snapshot.mockResolvedValue(mappedSnapshot)
    mocks.mapDrive.mockResolvedValue(undefined)
    mocks.unmapDrive.mockResolvedValue(undefined)
    mocks.shutdown.mockResolvedValue(undefined)
    mocks.reportError.mockResolvedValue(undefined)
    mocks.logDirectory.mockResolvedValue('C:\\Users\\tester\\EasyShare\\logs')
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('automatically maps once when the first snapshot is not mapped', async () => {
    mocks.snapshot
      .mockResolvedValueOnce(unmappedSnapshot)
      .mockResolvedValue(mappedSnapshot)

    const wrapper = mount(Harness)
    await flushPromises()

    expect(mocks.mapDrive).toHaveBeenCalledTimes(1)
    expect(mocks.snapshot).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="mapping"]').text()).toBe('false')

    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(mocks.mapDrive).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('waits for the first successful Core snapshot before automatic mapping', async () => {
    mocks.snapshot
      .mockRejectedValueOnce(new Error('core unavailable'))
      .mockResolvedValueOnce(unmappedSnapshot)
      .mockResolvedValue(mappedSnapshot)

    const wrapper = mount(Harness)
    await flushPromises()
    expect(mocks.mapDrive).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(mocks.mapDrive).toHaveBeenCalledTimes(1)
    expect(mocks.snapshot).toHaveBeenCalledTimes(3)
    wrapper.unmount()
  })

  it('does not map again when the drive is already connected', async () => {
    const wrapper = mount(Harness)
    await flushPromises()

    expect(mocks.snapshot).toHaveBeenCalledTimes(1)
    expect(mocks.mapDrive).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps an automatic mapping failure visible without retrying on polls', async () => {
    const failure = new Error('Z: is occupied')
    mocks.snapshot.mockResolvedValue(unmappedSnapshot)
    mocks.mapDrive.mockRejectedValueOnce(failure)

    const wrapper = mount(Harness)
    await flushPromises()
    expect(wrapper.get('[data-test="error"]').text()).toContain('Z: is occupied')
    expect(mocks.mapDrive).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.mapDrive).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="error"]').text()).toContain('Z: is occupied')
    expect(mocks.reportError).toHaveBeenCalledWith(
      '连接网络驱动器: Z: is occupied',
      expect.stringContaining('Z: is occupied'),
    )
    wrapper.unmount()
  })

  it('allows a manual retry after automatic mapping fails', async () => {
    mocks.snapshot
      .mockResolvedValueOnce(unmappedSnapshot)
      .mockResolvedValue(mappedSnapshot)
    mocks.mapDrive
      .mockRejectedValueOnce(new Error('temporary failure'))
      .mockResolvedValueOnce(undefined)

    const wrapper = mount(Harness)
    await flushPromises()
    expect(mocks.mapDrive).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="map"]').trigger('click')
    await flushPromises()
    expect(mocks.mapDrive).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="error"]').text()).toBe('')
    wrapper.unmount()
  })

  it('does not automatically remap after the user unmaps', async () => {
    mocks.snapshot
      .mockResolvedValueOnce(mappedSnapshot)
      .mockResolvedValue(unmappedSnapshot)

    const wrapper = mount(Harness)
    await flushPromises()
    await wrapper.get('[data-test="unmap"]').trigger('click')
    await flushPromises()

    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(mocks.unmapDrive).toHaveBeenCalledTimes(1)
    expect(mocks.mapDrive).not.toHaveBeenCalled()
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
