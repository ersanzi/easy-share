import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import DrivePanel from '../DrivePanel.vue'

const status = {
  core: true,
  discovery: true,
  receiver: true,
  webdav: false,
  driveMapped: false,
  cloudEnabled: false,
}

describe('DrivePanel', () => {
  it('allows retrying while WebDAV is stopped because Core starts it first', async () => {
    const wrapper = mount(DrivePanel, { props: { status, mapping: false } })
    const mapButton = wrapper.findAll('button').find(button => button.text().includes('重新连接'))
    expect(mapButton).toBeTruthy()
    expect(mapButton!.attributes('disabled')).toBeUndefined()
    await mapButton!.trigger('click')
    expect(wrapper.emitted('map')).toHaveLength(1)
  })

  it('shows automatic connection progress and prevents duplicate mapping', async () => {
    const wrapper = mount(DrivePanel, { props: { status, mapping: true } })
    const mapButton = wrapper.findAll('button').find(button => button.text().includes('正在连接到资源管理器'))
    expect(mapButton).toBeTruthy()
    expect(mapButton!.attributes('disabled')).toBeDefined()
    await mapButton!.trigger('click')
    expect(wrapper.emitted('map')).toBeUndefined()
    expect(wrapper.text()).toContain('正在连接…')
  })

  it('explains that the mapped drive can be opened from This PC', () => {
    const wrapper = mount(DrivePanel, {
      props: { status: { ...status, webdav: true, driveMapped: true }, mapping: false },
    })
    expect(wrapper.text()).toContain('双击 Z: 进入')
    expect(wrapper.text()).toContain('Z: 已连接')
  })
})
