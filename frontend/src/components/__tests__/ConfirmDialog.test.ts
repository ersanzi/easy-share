import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ConfirmDialog from '../ConfirmDialog.vue'

// ConfirmDialog 用 Teleport 渲染到 body，断言直接落在 document 上
const mountDialog = (props: Record<string, unknown> = {}) => mount(ConfirmDialog, {
  props: { open: true, title: '删除文件', message: '确定删除「a.txt」？', ...props },
  attachTo: document.body,
})

const dialogEl = () => document.querySelector('[role="dialog"]') as HTMLElement

describe('ConfirmDialog', () => {
  it('renders title/message and marks the dialog as modal', () => {
    const wrapper = mountDialog({ confirmText: '删除', danger: true })
    expect(dialogEl()).toBeTruthy()
    expect(dialogEl().getAttribute('aria-modal')).toBe('true')
    expect(dialogEl().getAttribute('aria-labelledby')).toBe('confirm-dialog-title')
    expect(dialogEl().getAttribute('aria-describedby')).toBe('confirm-dialog-message')
    expect(dialogEl().textContent).toContain('删除文件')
    expect(dialogEl().textContent).toContain('确定删除「a.txt」？')
    expect(document.querySelector('.confirm-submit')?.classList.contains('danger')).toBe(true)
    wrapper.unmount()
  })

  it('emits confirm and closes on the confirm button', async () => {
    const wrapper = mountDialog()
    ;(document.querySelector('.confirm-submit') as HTMLButtonElement).click()
    expect(wrapper.emitted('confirm')).toHaveLength(1)
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false])
    wrapper.unmount()
  })

  it('emits cancel from the cancel button and the backdrop', async () => {
    const wrapper = mountDialog()
    ;(document.querySelector('.confirm-actions .secondary-button') as HTMLButtonElement).click()
    ;(document.querySelector('.confirm-overlay') as HTMLElement).dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(wrapper.emitted('cancel')).toHaveLength(2)
    wrapper.unmount()
  })

  it('emits cancel on the Escape key', async () => {
    const wrapper = mountDialog()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('cancel')).toHaveLength(1)
    wrapper.unmount()
  })

  it('focuses the cancel button when opened', async () => {
    const wrapper = mountDialog()
    await new Promise(resolve => setTimeout(resolve))
    expect(document.activeElement?.classList.contains('secondary-button')).toBe(true)
    wrapper.unmount()
  })
  it('关闭的确认框不处理其他页面的 Escape', async () => {
    const wrapper = mountDialog({ open: false })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('cancel')).toBeUndefined()
    wrapper.unmount()
  })

  it('Tab 在对话框内循环，退出将焦点还给发起按钮', async () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const wrapper = mountDialog()
    await wrapper.vm.$nextTick()
    const cancel = document.querySelector('.confirm-actions .secondary-button') as HTMLButtonElement
    const submit = document.querySelector('.confirm-submit') as HTMLButtonElement
    expect(document.activeElement).toBe(cancel)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, cancelable: true }))
    expect(document.activeElement).toBe(submit)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', cancelable: true }))
    expect(document.activeElement).toBe(cancel)
    await wrapper.setProps({ open: false })
    expect(document.activeElement).toBe(trigger)
    wrapper.unmount()
    trigger.remove()
  })
})
