import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import KnowledgeDocumentList from '../KnowledgeDocumentList.vue'
import type { KnowledgeDocumentSummary } from '../../types/core'

const document = (overrides: Partial<KnowledgeDocumentSummary> = {}): KnowledgeDocumentSummary => ({
  job_id: 'job-1',
  file_id: 'file-1',
  version_id: 'v2',
  filename: '差旅制度.doc',
  status: 'completed',
  stage: 'finalizing',
  progress: 97,
  updated_at: '2026-10-20T11:00:00Z',
  ready: true,
  read_version_id: 'v1',
  chunks: 12,
  characters: 3456,
  processed_at: '2026-10-20T10:00:00Z',
  source_bytes: 2048,
  owner: 'alice',
  ...overrides,
})

const mountList = (documents: KnowledgeDocumentSummary[], overrides: Record<string, unknown> = {}) =>
  mount(KnowledgeDocumentList, {
    props: { documents, total: documents.length, query: '', loading: false, error: '', ...overrides },
  })

describe('KnowledgeDocumentList', () => {
  it('renders a ready document and emits open', async () => {
    const wrapper = mountList([document()])
    expect(wrapper.text()).toContain('差旅制度.doc')
    expect(wrapper.text()).toContain('12 块')
    expect(wrapper.get('.knowledge-doc-status').text()).toBe('已入库')
    await wrapper.get('.knowledge-doc-row').trigger('click')
    expect(wrapper.emitted('open')?.[0]?.[0]).toMatchObject({ file_id: 'file-1' })
  })

  it('keeps the previous version readable when the latest update failed', () => {
    const wrapper = mountList([
      document({ status: 'failed', ready: true, read_version_id: 'v1', error_message: 'NoSuchKey' }),
    ])
    expect(wrapper.get('.knowledge-doc-status').text()).toBe('更新失败')
    expect(wrapper.text()).toContain('NoSuchKey')
    expect(wrapper.get('.knowledge-doc-row').attributes('disabled')).toBeUndefined()
  })

  it('disables rows that have no readable version yet', () => {
    const wrapper = mountList([
      document({ status: 'processing', progress: 40, ready: false, read_version_id: null, chunks: 0 }),
    ])
    expect(wrapper.get('.knowledge-doc-status').text()).toBe('处理中 40%')
    expect(wrapper.get('.knowledge-doc-row').attributes('disabled')).toBeDefined()
  })

  it('distinguishes an empty knowledge base from an empty search', () => {
    expect(mountList([]).text()).toContain('知识库还没有文档')
    const searching = mountList([], { total: 3, query: '不存在' })
    expect(searching.text()).toContain('没有匹配的文档')
  })
})
