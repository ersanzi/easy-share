import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CloudPreview from '../CloudPreview.vue'
import type { CloudPreview as CloudPreviewData } from '../../types/core'

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