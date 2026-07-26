import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ActivityDrawer from '../ActivityDrawer.vue'
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

describe('ActivityDrawer', () => {
  it('sorts by activity priority, then by latest update', () => {
    const wrapper = mount(ActivityDrawer, {
      props: {
        tasks: [
          task({ id: 'recent', status: 'completed', updatedAt: '2026-07-27T00:09:00Z' }),
          task({ id: 'active-old', updatedAt: '2026-07-27T00:03:00Z' }),
          task({ id: 'attention', status: 'waiting_network', updatedAt: '2026-07-27T00:10:00Z' }),
          task({ id: 'active-new', kind: 'cloud_upload', updatedAt: '2026-07-27T00:08:00Z' }),
        ],
      },
    })

    expect(wrapper.findAll('.activity-item').map(item => item.attributes('data-task-id'))).toEqual([
      'active-new',
      'active-old',
      'attention',
      'recent',
    ])
    expect(wrapper.findAll('.activity-section').map(section => section.attributes('data-section'))).toEqual([
      'active',
      'attention',
      'recent',
    ])
  })

  it('shows at most eight tasks and reports the hidden count', () => {
    const tasks = Array.from({ length: 10 }, (_, index) => task({
      id: `task-${index}`,
      status: 'completed',
      updatedAt: `2026-07-27T00:${String(index).padStart(2, '0')}:00Z`,
    }))
    const wrapper = mount(ActivityDrawer, { props: { tasks } })

    expect(wrapper.findAll('.activity-item')).toHaveLength(8)
    expect(wrapper.findAll('.activity-item')[0].attributes('data-task-id')).toBe('task-9')
    expect(wrapper.text()).toContain('另有 2 项任务')
  })

  it('renders unified task actions, states, progress, speed, and errors', () => {
    const wrapper = mount(ActivityDrawer, {
      props: {
        tasks: [
          task({ id: 'upload', kind: 'cloud_upload', fileName: 'report.docx', status: 'queued' }),
          task({ id: 'download', kind: 'cloud_download', fileName: 'slides.pptx', direction: 'receive', status: 'paused' }),
          task({ id: 'offline', status: 'waiting_network' }),
          task({ id: 'failed', status: 'failed', error: '网络连接已断开' }),
          task({ id: 'cancelled', status: 'cancelled' }),
          task({ id: 'running', speed: 2048, transferredBytes: 50 }),
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
    expect(wrapper.text()).toContain('网络连接已断开')
    expect(wrapper.text()).toContain('2.0 KB/s')
    expect(wrapper.find('[data-task-id="running"] [role="progressbar"]').attributes('aria-valuenow')).toBe('50')
  })

  it('renders an informative empty state', () => {
    const wrapper = mount(ActivityDrawer, { props: { tasks: [] } })

    expect(wrapper.text()).toContain('暂无活动')
    expect(wrapper.findAll('.activity-item')).toHaveLength(0)
    expect(wrapper.text()).toContain('任务状态将由 Core 持久保存')
  })

  it('closes from the button, backdrop, and Escape, and opens the full task list', async () => {
    const wrapper = mount(ActivityDrawer, {
      props: { tasks: [] },
      attachTo: document.body,
    })

    expect(document.activeElement).toBe(wrapper.get('.activity-close').element)
    await wrapper.get('.activity-close').trigger('click')
    await wrapper.get('.activity-backdrop').trigger('click')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await wrapper.get('.activity-drawer-footer button').trigger('click')

    expect(wrapper.emitted('close')).toHaveLength(3)
    expect(wrapper.emitted('viewAll')).toHaveLength(1)
    wrapper.unmount()
  })
})
