// @vitest-environment node
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import postcss, { type Node } from 'postcss'

const localCss = resolve(process.cwd(), 'src/style.css')
const css = postcss.parse(readFileSync(existsSync(localCss) ? localCss : resolve(process.cwd(), 'frontend/src/style.css'), 'utf8'))
describe('动效性能和可访问性护栏', () => {
  it('交互过渡不隐式动画布局属性', () => {
    const invalid: string[] = []
    css.walkDecls('transition', declaration => {
      if (/(^|,)\s*(all|width|height|max-height|left|right|top|bottom|margin|padding)\b/.test(declaration.value)) invalid.push(declaration.toString())
    })
    expect(invalid).toEqual([])
  })
  it('无限动画必须位于无减少动态效果偏好的媒体查询内', () => {
    const invalid: string[] = []
    css.walkDecls('animation', declaration => {
      if (!declaration.value.includes('infinite')) return
      let parent: Node | undefined = declaration.parent
      while (parent && !(parent.type === 'atrule' && 'params' in parent && String(parent.params).includes('prefers-reduced-motion: no-preference'))) parent = parent.parent
      if (!parent) invalid.push(declaration.toString())
    })
    expect(invalid).toEqual([])
  })
  it('不靠全局极短动画时长或永久提升图层规避生命周期', () => {
    const invalid: string[] = []
    css.walkDecls(declaration => {
      if (declaration.prop === 'will-change' || /animation-duration/.test(declaration.prop) && /0\.0[01]/.test(declaration.value)) invalid.push(declaration.toString())
    })
    expect(invalid).toEqual([])
  })
})
