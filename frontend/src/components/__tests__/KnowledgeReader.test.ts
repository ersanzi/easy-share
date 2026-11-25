import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import KnowledgeReader from '../KnowledgeReader.vue'
import KnowledgePanel from '../KnowledgePanel.vue'
import type { KnowledgeDocumentView } from '../../types/core'

const knowledgeMocks = vi.hoisted(() => ({
  status: vi.fn(), ask: vi.fn(), documents: vi.fn(), document: vi.fn(),
  events: new Map<string, () => unknown>(), stop: vi.fn(),
}))
vi.mock('../../services/core', () => ({ core: {
  knowledgeStatus: knowledgeMocks.status,
  knowledgeHealth: vi.fn().mockResolvedValue(null),
  knowledgeAsk: knowledgeMocks.ask,
  knowledgeDocuments: knowledgeMocks.documents,
  knowledgeDocument: knowledgeMocks.document,
} }))
vi.mock('../../composables/useEasyShare', () => ({ useEasyShare: () => ({ currentUser: { value: { userName: 'employee' } } }) }))
vi.mock('../../../wailsjs/runtime/runtime', () => ({
  EventsOn: (event: string, callback: () => unknown) => {
    knowledgeMocks.events.set(event, callback)
    return knowledgeMocks.stop
  },
}))
enableAutoUnmount(afterEach)
const loggedIn = { configured: true, loggedIn: true, serverUrl: 'https://knowledge.test', username: 'employee', role: 'user', mode: 'sso' }
beforeEach(() => {
  knowledgeMocks.status.mockReset().mockResolvedValue(loggedIn)
  knowledgeMocks.ask.mockReset()
  knowledgeMocks.documents.mockReset().mockResolvedValue({ documents: [] })
  knowledgeMocks.document.mockReset()
  knowledgeMocks.events.clear()
  knowledgeMocks.stop.mockClear()
})

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
  afterEach(() => { vi.unstubAllGlobals() })

  it('positions a heading immediately when reduced motion is requested', async () => {
    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: true })))
    const wrapper = mount(KnowledgeReader, { props: { view, loading: false, error: '' }, attachTo: document.body })
    const scroll = vi.fn()
    wrapper.get('#knowledge-block-h1').element.scrollIntoView = scroll
    await wrapper.get('.reader-outline button').trigger('click')
    expect(scroll).toHaveBeenCalledWith({ block: 'center', behavior: 'instant' })
    wrapper.unmount()
  })

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

  it('allows reading but does not offer source download without an explicit permission', () => {
    const readView = { ...view, manifest: { ...view.manifest, can_download: false } }
    const wrapper = mount(KnowledgeReader, { props: { view: readView, loading: false, error: '' } })
    expect(wrapper.text()).toContain('住宿标准每晚 400 元')
    expect(wrapper.text()).toContain('仅可查看')
    expect(wrapper.findAll('button').some(button => button.text().includes('下载原件'))).toBe(false)
    const unknown = mount(KnowledgeReader, { props: { view, loading: false, error: '' } })
    expect(unknown.findAll('button').some(button => button.text().includes('下载原件'))).toBe(false)
  })

  it('emits close and download from the header actions', async () => {
    const wrapper = mount(KnowledgeReader, { props: { view: { ...view, manifest: { ...view.manifest, can_download: true } }, loading: false, error: '' } })
    await wrapper.get('.reader-close').trigger('click')
    await wrapper.get('.reader-actions button').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.emitted('download')).toHaveLength(1)
  })
})


describe('KnowledgePanel session lifecycle', () => {
  it('discards an answer if the session ends while the request is pending', async () => {
    let finish!: (value: { answer: string; contexts: never[] }) => void
    knowledgeMocks.ask.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mount(KnowledgePanel, { global: { stubs: { teleport: true } } })
    await flushPromises()
    await wrapper.get('.knowledge-input').setValue('待回答的问题')
    await wrapper.get('.knowledge-composer').trigger('submit')
    await flushPromises()
    knowledgeMocks.status.mockResolvedValue({ ...loggedIn, loggedIn: false })
    await knowledgeMocks.events.get('knowledge:session-changed')?.()
    await flushPromises()
    finish({ answer: '旧会话中的回答', contexts: [] })
    await flushPromises()
    expect(wrapper.find('.knowledge-composer').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('旧会话中的回答')
    expect(wrapper.text()).toContain('连接公司知识服务器')
  })

  it('ignores a document list from a session that has been cleared', async () => {
    let finish!: (value: { documents: unknown[] }) => void
    knowledgeMocks.documents.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mount(KnowledgePanel, { global: { stubs: { teleport: true } } })
    await flushPromises()
    await wrapper.findAll('.knowledge-tabs button')[1]!.trigger('click')
    knowledgeMocks.status.mockResolvedValue({ ...loggedIn, loggedIn: false })
    await knowledgeMocks.events.get('knowledge:session-changed')?.()
    await flushPromises()
    finish({ documents: [{ file_id: 'old', filename: '旧会话文档', ready: true, read_version_id: 'v1' }] })
    await flushPromises()
    knowledgeMocks.status.mockResolvedValue(loggedIn)
    await knowledgeMocks.events.get('knowledge:session-changed')?.()
    await flushPromises()
    expect(wrapper.text()).not.toContain('旧会话文档')
    expect(wrapper.text()).toContain('知识库还没有文档')
  })
})
