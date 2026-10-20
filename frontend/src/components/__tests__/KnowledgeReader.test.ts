import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import KnowledgeReader from '../KnowledgeReader.vue'
import type { KnowledgeDocumentView } from '../../types/core'

const view: KnowledgeDocumentView = {
  manifest: {
    file_id: 'file-1',
    version_id: 'v1',
    filename: '差旅制度.doc',
    blocks: 3,
    source_bytes: 2048,
    processed_at: '2026-10-20T10:00:00Z',
  },
  document: {
    filename: '差旅制度.doc',
    blocks: [
      { id: 'h1', type: 'heading', text: '差旅制度', level: 1, source: { page: 1 } },
      { id: 'p1', type: 'paragraph', text: '住宿标准每晚 400 元', source: { page: 2 } },
      { id: 't1', type: 'table', rows: [['项目', '标准'], ['住宿', '400']], source: { page: 2 } },
    ],
  },
}

describe('KnowledgeReader', () => {
  it('renders headings, paragraphs and tables as read-only content', () => {
    const wrapper = mount(KnowledgeReader, { props: { view, loading: false, error: '' } })
    expect(wrapper.get('.reader-title h2').text()).toBe('差旅制度.doc')
    expect(wrapper.text()).toContain('住宿标准每晚 400 元')
    expect(wrapper.get('table').text()).toContain('400')
    expect(wrapper.get('table caption').text()).toContain('第 2 页')
  })

  it('highlights only the cited blocks', () => {
    const wrapper = mount(KnowledgeReader, {
      props: { view, loading: false, error: '', focusBlockIds: ['p1'] },
    })
    expect(wrapper.get('#knowledge-block-p1').classes()).toContain('is-cited')
    expect(wrapper.get('#knowledge-block-h1').classes()).not.toContain('is-cited')
  })

  it('exposes loading and error states', () => {
    const loading = mount(KnowledgeReader, { props: { view: null, loading: true, error: '' } })
    expect(loading.text()).toContain('正在读取原文')
    const failed = mount(KnowledgeReader, { props: { view: null, loading: false, error: '无权查看该文档' } })
    expect(failed.text()).toContain('无权查看该文档')
  })

  it('emits close and download from the header actions', async () => {
    const wrapper = mount(KnowledgeReader, { props: { view, loading: false, error: '' } })
    await wrapper.get('.reader-close').trigger('click')
    await wrapper.get('.reader-actions button').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.emitted('download')).toHaveLength(1)
  })
})
