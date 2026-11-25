import type { ObjectDirective } from 'vue'

// 只对实际内容替换做淡入；不延迟状态提交、不保留旧的敏感内容。
const running = new WeakMap<HTMLElement, Animation>()
const cleanups = new WeakMap<HTMLElement, () => void>()

export function stopContentMotion(element: HTMLElement) {
  cleanups.get(element)?.()
  cleanups.delete(element)
  running.get(element)?.cancel()
  running.delete(element)
}

export function animateContentChange(element: HTMLElement) {
  if (typeof element.animate !== 'function' || !element.isConnected) return
  const previous = running.get(element)
  const style = getComputedStyle(element)
  // 连续切换从当前不透明度接上，不从零重新闪一次。
  const opacity = previous ? Number.parseFloat(style.opacity) : 0.88
  stopContentMotion(element)
  const preference = window.matchMedia?.('(prefers-reduced-motion: reduce)')
  const token = preference?.matches ? '--motion-reduced' : '--motion-fast'
  const duration = Number.parseFloat(style.getPropertyValue(token)) || (preference?.matches ? 80 : 120)
  const easing = style.getPropertyValue('--motion-ease').trim() || 'cubic-bezier(0.2, 0, 0, 1)'
  const animation = element.animate([{ opacity }, { opacity: 1 }], { duration, easing })
  running.set(element, animation)
  const stop = () => stopContentMotion(element)
  preference?.addEventListener?.('change', stop, { once: true })
  cleanups.set(element, () => preference?.removeEventListener?.('change', stop))
  void animation.finished.catch(() => undefined).then(() => {
    if (running.get(element) === animation) stopContentMotion(element)
  })
}

export const vContentMotion: ObjectDirective<HTMLElement, unknown> = {
  updated(element, { value, oldValue }) {
    if (!Object.is(value, oldValue)) animateContentChange(element)
  },
  beforeUnmount: stopContentMotion,
}


// v-if 删除时不会再更新旧节点属性；过渡钩子负责退出期隔离键盘与鼠标。
export const presenceMotion = {
  onBeforeLeave(element: Element) { (element as HTMLElement).inert = true },
  onBeforeEnter(element: Element) { (element as HTMLElement).inert = false },
}
