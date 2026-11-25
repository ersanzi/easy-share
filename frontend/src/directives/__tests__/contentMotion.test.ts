import { afterEach, describe, expect, it, vi } from 'vitest'
import { animateContentChange, stopContentMotion, presenceMotion } from '../contentMotion'

const pending = () => {
  let resolve!: () => void
  const finished = new Promise<void>(done => { resolve = done })
  return { finished, cancel: vi.fn(), resolve }
}
const fixture = () => {
  const element = document.createElement('div')
  element.style.setProperty('--motion-fast', '120ms')
  document.body.append(element)
  const animate = vi.fn()
  element.animate = animate
  return { element, animate }
}
afterEach(() => { document.body.replaceChildren(); vi.unstubAllGlobals() })

describe('内容状态过渡', () => {
  it('连续替换只留下最后一段，旧动画完成不能取消后来动画', async () => {
    const { element, animate } = fixture()
    const first = pending(), second = pending()
    animate.mockReturnValueOnce(first).mockReturnValueOnce(second)
    animateContentChange(element)
    element.style.opacity = '0.94'
    animateContentChange(element)
    expect(first.cancel).toHaveBeenCalledOnce()
    expect(animate.mock.calls[1][0][0].opacity).toBe(0.94)
    first.resolve()
    await Promise.resolve(); await Promise.resolve()
    expect(second.cancel).not.toHaveBeenCalled()
    stopContentMotion(element)
    expect(second.cancel).toHaveBeenCalledOnce()
  })
  it('减少动态效果只使用短淡入，不引入位移或缩放', () => {
    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() })))
    const { element, animate } = fixture()
    animate.mockReturnValue(pending())
    animateContentChange(element)
    expect(animate.mock.calls[0][1].duration).toBe(80)
    expect(animate.mock.calls[0][0]).toEqual([{ opacity: 0.88 }, { opacity: 1 }])
    stopContentMotion(element)
  })
  it('运行中改变系统偏好时结束过渡并清理监听器', () => {
    const listeners = new Map<string, () => void>()
    const remove = vi.fn()
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: (key: string, fn: () => void) => listeners.set(key, fn), removeEventListener: remove }))
    const { element, animate } = fixture()
    const animation = pending()
    animate.mockReturnValue(animation)
    animateContentChange(element)
    listeners.get('change')!()
    expect(animation.cancel).toHaveBeenCalledOnce()
    expect(remove).toHaveBeenCalled()
  })
  it('离开文档和不支持动画的宿主可立即完成业务变化', () => {
    const detached = document.createElement('div')
    detached.animate = vi.fn()
    animateContentChange(detached)
    expect(detached.animate).not.toHaveBeenCalled()
    const staticElement = document.createElement('div')
    document.body.append(staticElement)
    expect(() => animateContentChange(staticElement)).not.toThrow()
  })
  it('退出时马上禁用交互，重新进入后恢复', () => {
    const element = document.createElement('div')
    presenceMotion.onBeforeLeave(element)
    expect(element.inert).toBe(true)
    presenceMotion.onBeforeEnter(element)
    expect(element.inert).toBe(false)
  })
})
