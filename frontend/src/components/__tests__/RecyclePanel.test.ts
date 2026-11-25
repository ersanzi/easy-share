import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { RecycleEntry } from '../../types/core'
import RecyclePanel from '../RecyclePanel.vue'
import ConfirmDialog from '../ConfirmDialog.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn<() => Promise<RecycleEntry[]>>(),
  restore: vi.fn<(fileId: string) => Promise<void>>(),
  purge: vi.fn<(fileId: string) => Promise<void>>(),
  eventsOn: vi.fn(),
  stop: vi.fn(),
}))

vi.mock('../../services/core', () => ({ core: {
  cloudRecycleList: mocks.list,
  cloudRecycleRestore: mocks.restore,
  cloudRecyclePurge: mocks.purge,
} }))
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: mocks.eventsOn }))

const directoryEntry: RecycleEntry = {
  fileId: '2110992378747158530',
  path: 'docs/report.txt',
  originPath: 'docs/report.txt',
  directory: true,
  batchId: 'server-batch-42',
  snapshotState: 'ready',
  size: 0,
  versionId: 3,
}
const childEntry: RecycleEntry = {
  fileId: '2110992378747158531',
  path: 'docs/report.txt/child.pdf',
  originPath: 'docs/report.txt/child.pdf',
  batchId: directoryEntry.batchId,
  directory: false,
  size: 12,
  versionId: 1,
}
// 缺新增字段的旧行仍是合法类型，fileId保持字符串。
const legacyEntry: RecycleEntry = {
  fileId: '7', path: 'old.txt', originPath: 'old.txt', size: 12, versionId: 1,
}

const wrappers: VueWrapper[] = []
const mountPanel = () => {
  const wrapper = mount(RecyclePanel, { global: { stubs: { ConfirmDialog: true } } })
  wrappers.push(wrapper)
  return wrapper
}
const cloudChanged = () => {
  const handler = mocks.eventsOn.mock.calls.find(call => call[0] === 'cloud:changed')![1] as () => void
  handler()
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.list.mockReset().mockResolvedValue([])
  mocks.restore.mockReset().mockResolvedValue(undefined)
  mocks.purge.mockReset().mockResolvedValue(undefined)
  mocks.eventsOn.mockReset().mockReturnValue(mocks.stop)
})
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
})

describe('RecyclePanel safe recycle compatibility', () => {
  it('renders a directory icon and folder type even when its name looks like text', async () => {
    mocks.list.mockResolvedValue([directoryEntry, childEntry, legacyEntry])
    const wrapper = mountPanel()
    await flushPromises()
    const rows = wrapper.findAll('.cloud-row')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.find('.recycle-directory-icon').exists()).toBe(true)
    expect(rows[0]!.find('.recycle-file-icon').exists()).toBe(false)
    expect(rows[0]!.find('strong').text()).toBe('report.txt')
    expect(rows[0]!.text()).toContain('文件夹')
    expect(rows[0]!.text()).not.toContain('文本 / 代码')
    expect(rows[0]!.text()).not.toContain('0 B')
    expect(rows[1]!.find('.recycle-file-icon').exists()).toBe(true)
    expect(rows[1]!.text()).toContain('PDF 文档')
    expect(rows[2]!.text()).toContain('文本 / 代码')
    expect(rows[2]!.text()).toContain('12 B')
    expect(wrapper.find('.count-badge').text()).toBe('3 个项目')
  })

  it('keeps explicit directory=false authoritative and supports trailing-slash legacy folders', async () => {
    mocks.list.mockResolvedValue([
      { ...legacyEntry, path: 'legacy/', originPath: 'legacy/' },
      { ...legacyEntry, fileId: '8', directory: false, path: 'file/', originPath: 'file/' },
    ])
    const wrapper = mountPanel()
    await flushPromises()
    const rows = wrapper.findAll('.cloud-row')
    expect(rows[0]!.find('strong').text()).toBe('legacy')
    expect(rows[0]!.text()).toContain('文件夹')
    expect(rows[0]!.find('.recycle-directory-icon').exists()).toBe(true)
    expect(rows[1]!.find('.recycle-file-icon').exists()).toBe(true)
    expect(rows[1]!.text()).not.toContain('文件夹')
  })

  it('reloads the complete recycle list after restoring a batch root and preserves string IDs', async () => {
    const remaining = { ...legacyEntry, path: 'fresh.json', originPath: 'fresh.json' }
    mocks.list.mockResolvedValueOnce([directoryEntry, childEntry, legacyEntry]).mockResolvedValueOnce([remaining])
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.findAll('.cloud-row')[0]!.find('button[title="还原到原位置"]').trigger('click')
    await flushPromises()
    expect(mocks.restore).toHaveBeenCalledWith('2110992378747158530')
    expect(mocks.list).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('.cloud-row')).toHaveLength(1)
    expect(wrapper.text()).toContain('fresh.json')
    expect(wrapper.text()).not.toContain('child.pdf')
    expect(wrapper.text()).not.toContain('old.txt')
  })

  it('does not remove any batch rows when restore conflicts', async () => {
    mocks.list.mockResolvedValue([directoryEntry, childEntry])
    mocks.restore.mockRejectedValue(new Error('原位置已有同名文件'))
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.findAll('.cloud-row')[0]!.find('button[title="还原到原位置"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.cloud-row')).toHaveLength(2)
    expect(wrapper.text()).toContain('原位置已有同名文件')
    expect(mocks.list).toHaveBeenCalledTimes(1)
  })

  it('requires purge confirmation and reloads the whole batch list after purging', async () => {
    mocks.list.mockResolvedValueOnce([directoryEntry, childEntry, legacyEntry]).mockResolvedValueOnce([legacyEntry])
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.findAll('.cloud-row')[0]!.find('button[title="彻底删除，不可还原"]').trigger('click')
    expect(mocks.purge).not.toHaveBeenCalled()
    const dialog = wrapper.findComponent(ConfirmDialog)
    expect(dialog.props('open')).toBe(true)
    dialog.vm.$emit('confirm')
    await flushPromises()
    expect(mocks.purge).toHaveBeenCalledWith(directoryEntry.fileId)
    expect(mocks.list).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('.cloud-row')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('child.pdf')
  })

  it('subscribes to cross-entry cloud changes and cancels only its own listener on unmount', async () => {
    mocks.list.mockResolvedValueOnce([]).mockResolvedValueOnce([directoryEntry])
    const wrapper = mountPanel()
    await flushPromises()
    expect(mocks.eventsOn).toHaveBeenCalledWith('cloud:changed', expect.any(Function))
    cloudChanged() // 主线删除/还原等入口广播，不依赖事件payload的硬编码。
    await flushPromises()
    expect(mocks.list).toHaveBeenCalledTimes(2)
    expect(wrapper.find('.recycle-directory-icon').exists()).toBe(true)
    wrapper.unmount()
    wrappers.splice(wrappers.indexOf(wrapper), 1)
    expect(mocks.stop).toHaveBeenCalledTimes(1)
  })

  it('ignores an older load that completes after a cloud-change refresh', async () => {
    let finishOld!: (entries: RecycleEntry[]) => void
    mocks.list.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve }))
      .mockResolvedValueOnce([directoryEntry])
    const wrapper = mountPanel()
    cloudChanged()
    await flushPromises()
    expect(wrapper.text()).toContain('report.txt')
    finishOld([legacyEntry])
    await flushPromises()
    expect(wrapper.text()).toContain('report.txt')
    expect(wrapper.text()).not.toContain('old.txt')
  })

  it('shows reloading failures instead of pretending a batch was removed locally', async () => {
    mocks.list.mockResolvedValueOnce([directoryEntry]).mockRejectedValueOnce(new Error('回收站网络失败'))
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.find('button[title="还原到原位置"]').trigger('click')
    await flushPromises()
    expect(mocks.list).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('回收站网络失败')
    expect(wrapper.text()).not.toContain('回收站为空')
  })
})
