import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TransferList from '../TransferList.vue'
import type { TransferTask } from '../../types/core'

const task = (overrides: Partial<TransferTask> = {}): TransferTask => ({
  id: 'task-1',
  kind: 'lan_send',
  fileName: 'demo.zip',
  direction: 'send',
  peer: 'Office PC',
  totalBytes: 100,
  transferredBytes: 40,
  speed: 0,
  status: 'running',
  createdAt: '2026-07-27T00:00:00Z',
  updatedAt: '2026-07-27T00:01:00Z',
  ...overrides,
})

describe('TransferList', () => {
  it('keeps the pending receive actions for legacy tasks without kind', async () => {
    const wrapper = mount(TransferList, {
      props: {
        tasks: [task({
          id: 'legacy-receive',
          kind: undefined,
          direction: 'receive',
          status: 'pending',
        })],
      },
    })

    expect(wrapper.text()).toContain('接收自 Office PC')
    expect(wrapper.text()).toContain('40%')
    const row = wrapper.get('[data-task-id="legacy-receive"]')
    const buttons = row.findAll('button')
    expect(buttons.map(button => button.text())).toEqual(['拒绝', '另存', '接收'])

    await buttons[0].trigger('click')
    await buttons[1].trigger('click')
    await buttons[2].trigger('click')
    expect(wrapper.emitted('reject')).toEqual([['legacy-receive']])
    expect(wrapper.emitted('acceptAs')).toEqual([['legacy-receive']])
    expect(wrapper.emitted('accept')).toEqual([['legacy-receive']])
  })

  it('uses user-facing labels for cloud tasks and every extended state', () => {
    const wrapper = mount(TransferList, {
      props: {
        tasks: [
          task({ id: 'upload', kind: 'cloud_upload', status: 'queued', fileName: 'upload.docx' }),
          task({ id: 'download', kind: 'cloud_download', direction: 'receive', status: 'paused', fileName: 'download.pdf' }),
          task({ id: 'offline', status: 'waiting_network' }),
          task({ id: 'failed', status: 'failed', error: '上传失败' }),
          task({ id: 'cancelled', status: 'cancelled' }),
        ],
      },
    })

    expect(wrapper.text()).toContain('上传到网盘')
    expect(wrapper.text()).toContain('从网盘下载')
    expect(wrapper.text()).toContain('排队中')
    expect(wrapper.text()).toContain('已暂停')
    expect(wrapper.text()).toContain('等待网络')
    expect(wrapper.text()).toContain('失败')
    expect(wrapper.text()).toContain('已取消')
    expect(wrapper.text()).toContain('上传失败')
    expect(wrapper.find('[data-task-id="upload"] .task-kind-marker').classes()).toContain('cloud_upload')
    expect(wrapper.find('[data-task-id="download"] .task-kind-marker').classes()).toContain('cloud_download')
  })

  it('infers legacy send tasks without exposing receive confirmation actions', () => {
    const wrapper = mount(TransferList, {
      props: {
        tasks: [task({ id: 'legacy-send', kind: undefined, direction: 'send', status: 'pending' })],
      },
    })

    const row = wrapper.get('[data-task-id="legacy-send"]')
    expect(row.text()).toContain('发送到 Office PC')
    expect(row.find('.task-kind-marker').classes()).toContain('lan_send')
    expect(row.findAll('button')).toHaveLength(0)
  })

  it('groups batches and orders active work before newer completed tasks', () => {
    const wrapper = mount(TransferList, {
      props: {
        tasks: [
          task({ id: 'done', status: 'completed', transferredBytes: 100, updatedAt: '2026-07-27T00:10:00Z' }),
          task({ id: 'batch-old', batchId: 'batch-a', fileName: 'one.txt', transferredBytes: 100, status: 'completed', updatedAt: '2026-07-27T00:02:00Z' }),
          task({ id: 'batch-active', batchId: 'batch-a', fileName: 'two.txt', transferredBytes: 50, updatedAt: '2026-07-27T00:03:00Z' }),
        ],
      },
    })

    const entries = wrapper.findAll('.transfer-list > *')
    expect(entries[0].classes()).toContain('batch-group')
    expect(entries[0].findAll('.transfer-row').map(row => row.attributes('data-task-id'))).toEqual([
      'batch-active',
      'batch-old',
    ])
    expect(entries[0].text()).toContain('1/2 完成')
    expect(entries[0].text()).toContain('75%')
    expect(entries[1].attributes('data-task-id')).toBe('done')
  })

  it('allows terminal records to be deleted', async () => {
    const wrapper = mount(TransferList, {
      props: { tasks: [task({ id: 'cancelled', status: 'cancelled' })] },
    })

    await wrapper.get('[data-task-id="cancelled"] .delete-btn').trigger('click')
    expect(wrapper.emitted('delete')).toEqual([['cancelled']])
  })
})
