import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CloudPreview from '../CloudPreview.vue'
import CloudPanel from '../CloudPanel.vue'
import type { CloudFile, CloudPreview as CloudPreviewData } from '../../types/core'

const cloudMocks = vi.hoisted(() => ({
  list: vi.fn(), preview: vi.fn(), spaces: vi.fn(),
}))
vi.mock('../../services/core', () => ({ core: {
  cloudListInSpace: cloudMocks.list,
  cloudPreview: cloudMocks.preview,
  mySpaces: cloudMocks.spaces,
  cfapiMountPath: vi.fn().mockResolvedValue(''),
  cloudStars: vi.fn().mockResolvedValue([]),
  cloudTouch: vi.fn().mockResolvedValue(undefined),
} }))
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn(() => () => {}) }))
vi.mock('../SharedDrivePanel.vue', () => ({ default: { template: '<div />' } }))
vi.mock('../RecyclePanel.vue', () => ({ default: { template: '<div />' } }))
enableAutoUnmount(afterEach)

const file = (key: string): CloudFile => ({ key, name: key, size: 32, lastModified: '', previewKind: 'text', contentType: 'text/plain', fileId: key })
const pending = <T,>() => {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(finish => { resolve = finish })
  return { promise, resolve }
}

beforeEach(() => {
  cloudMocks.list.mockReset().mockResolvedValue([file('a.txt'), file('b.txt')])
  cloudMocks.preview.mockReset()
  cloudMocks.spaces.mockReset().mockResolvedValue([])
})

const preview = (overrides: Partial<CloudPreviewData> = {}): CloudPreviewData => ({
  key: 'note.txt',
  name: 'note.txt',
  kind: 'text',
  contentType: 'text/plain',
  size: 32,
  text: '<script>alert("safe")</script>',
  ...overrides,
})

describe('CloudPreview', () => {
  it('renders text as escaped plain text and shows truncation guidance', () => {
    const wrapper = mount(CloudPreview, { props: { preview: preview({ truncated: true }), loading: false, error: '' } })
    expect(wrapper.find('pre').text()).toContain('<script>alert("safe")</script>')
    expect(wrapper.find('pre').html()).toContain('&lt;script&gt;')
    expect(wrapper.text()).toContain('仅显示前 1 MiB')
  })

  it('uses the image and PDF renderers selected by backend capability', () => {
    const image = mount(CloudPreview, { props: { preview: preview({ kind: 'image', contentUrl: 'http://core/image' }), loading: false, error: '' } })
    expect(image.find('img').attributes('src')).toBe('http://core/image')

    const pdf = mount(CloudPreview, { props: { preview: preview({ kind: 'pdf', contentUrl: 'http://core/file.pdf' }), loading: false, error: '' } })
    expect(pdf.find('iframe').attributes('src')).toBe('http://core/file.pdf')
  })

  it('closes from the close button, backdrop, and Escape key', async () => {
    const wrapper = mount(CloudPreview, { props: { preview: preview(), loading: false, error: '' }, attachTo: document.body })
    await wrapper.get('.preview-close').trigger('click')
    await wrapper.get('.preview-overlay').trigger('click')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('close')).toHaveLength(3)
    wrapper.unmount()
  })
})

describe('CloudPanel preview lifecycle', () => {
  it('keeps the newest preview when an earlier request arrives late', async () => {
    const first = pending<CloudPreviewData>()
    cloudMocks.preview.mockReturnValueOnce(first.promise).mockResolvedValueOnce(preview({ key: 'b.txt', name: 'b.txt', text: '第二个文件' }))
    const wrapper = mount(CloudPanel, { props: { enabled: true } })
    await flushPromises()
    await wrapper.findAll('.preview-action')[0]!.trigger('click')
    await wrapper.get('.preview-close').trigger('click')
    await wrapper.findAll('.preview-action')[1]!.trigger('click')
    await flushPromises()
    first.resolve(preview({ key: 'a.txt', name: 'a.txt', text: '已过期的文件' }))
    await flushPromises()
    expect(wrapper.get('.preview-text').text()).toBe('第二个文件')
    expect(wrapper.get('.preview-title h2').text()).toBe('b.txt')
  })

  it('does not let an older file list replace a newer refresh', async () => {
    const first = pending<CloudFile[]>()
    cloudMocks.list.mockReturnValueOnce(first.promise).mockResolvedValueOnce([file('new.txt')])
    const wrapper = mount(CloudPanel, { props: { enabled: true } })
    await wrapper.findAll('.header-actions button').find(button => button.text() === '刷新')!.trigger('click')
    await flushPromises()
    first.resolve([file('old.txt')])
    await flushPromises()
    expect(wrapper.text()).toContain('new.txt')
    expect(wrapper.text()).not.toContain('old.txt')
  })

  it('retains the preview DOM for a real leave transition when closing', async () => {
    cloudMocks.preview.mockResolvedValue(preview())
    const wrapper = mount(CloudPanel, { props: { enabled: true }, global: { stubs: { transition: false } }, attachTo: document.body })
    await flushPromises()
    await wrapper.findAll('.preview-action')[0]!.trigger('click')
    await flushPromises()
    const overlay = wrapper.get('.preview-overlay').element
    await wrapper.get('.preview-close').trigger('click')
    expect(overlay.isConnected).toBe(true)
    expect(overlay.classList.contains('es-dialog-leave-active')).toBe(true)
    expect((overlay as HTMLElement).inert).toBe(true)
    wrapper.unmount()
  })

  it('renders quota progress with a clamped transform', async () => {
    cloudMocks.spaces.mockResolvedValue([{ spaceType: 'personal', usedBytes: 250, quotaBytes: 100 }])
    const wrapper = mount(CloudPanel, { props: { enabled: true } })
    await flushPromises()
    expect(wrapper.get('.usage-bar i').attributes('style')).toContain('scaleX(1)')
    expect(wrapper.get('.usage-bar i').attributes('style')).not.toContain('width:')
  })
})
