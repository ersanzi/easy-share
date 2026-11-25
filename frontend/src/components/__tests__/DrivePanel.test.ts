import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import DrivePanel from '../DrivePanel.vue'

const status = {
  core: true,
  discovery: true,
  receiver: true,
  webdav: false,
  cloudEnabled: false,
}

describe('DrivePanel', () => {
  it('keeps the focused action usable while share status changes rapidly', async () => {
    const wrapper = mount(DrivePanel, { props: { status }, attachTo: document.body })
    const button = wrapper.get('.drive-actions button')
    ;(button.element as HTMLButtonElement).focus()
    await wrapper.setProps({ status: { ...status, webdav: true } })
    expect(wrapper.get('.drive-actions button').element).toBe(button.element)
    expect(document.activeElement).toBe(button.element)
    await button.trigger('click')
    expect(wrapper.emitted('stop')).toHaveLength(1)
    await wrapper.setProps({ status })
    await button.trigger('click')
    expect(wrapper.emitted('start')).toHaveLength(1)
    wrapper.unmount()
  })

  it('offers to start the share when WebDAV is stopped', async () => {
    const wrapper = mount(DrivePanel, { props: { status } })
    const startButton = wrapper.findAll('button').find(button => button.text().includes('启动共享'))
    expect(startButton).toBeTruthy()
    await startButton!.trigger('click')
    expect(wrapper.emitted('start')).toHaveLength(1)
  })

  it('offers to stop the share when WebDAV is running', async () => {
    const wrapper = mount(DrivePanel, { props: { status: { ...status, webdav: true } } })
    const stopButton = wrapper.findAll('button').find(button => button.text().includes('停止共享'))
    expect(stopButton).toBeTruthy()
    await stopButton!.trigger('click')
    expect(wrapper.emitted('stop')).toHaveLength(1)
  })

  it('explains that the share is opened from This PC without a drive letter', () => {
    const wrapper = mount(DrivePanel, { props: { status: { ...status, webdav: true } } })
    expect(wrapper.text()).toContain('此电脑')
    expect(wrapper.text()).toContain('无需映射盘符')
  })
})
