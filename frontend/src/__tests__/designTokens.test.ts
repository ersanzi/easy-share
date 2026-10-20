import { describe, expect, it } from 'vitest'
import {
  AA_NORMAL_TEXT,
  TEXT_ON_TINT_PAIRS,
  contrastRatio,
  lintDesignTokens,
  parseRootTokens,
  scanComponentLiterals,
} from '../utils/designTokensLint'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'

// 设计 token 纪律的守卫测试。
//
// 这三条规矩写在 AGENTS.md 与 docs/wps-dingtalk-ui-reverse.md 里三个月了，
// 期间没人拦住新增的字面值（实测攒下 28 种近中性灰）。
// 写进测试才有意义：**以后往 style.css 里塞裸色值、或把文字档改到 AA 以下，CI 就红**。

// 读原文只能用 fs：vitest 会把 CSS（含 `?raw`）stub 成空串，
// 用 import 读会拿到 0 长度而下面所有断言集体空过——那比没有测试更糟。
// cwd 可能是 frontend 也可能是仓库根（取决于从哪跑），所以逐级上溯找文件。
function findStyleCss(): string {
  let dir = process.cwd()
  for (let depth = 0; depth < 5; depth++) {
    for (const candidate of [resolve(dir, 'src/style.css'), resolve(dir, 'frontend/src/style.css')]) {
      if (existsSync(candidate)) return candidate
    }
    const parent = dirname(dir)
    if (parent === dir) break
    dir = parent
  }
  throw new Error(`找不到 style.css（从 ${process.cwd()} 起向上找 5 层）`)
}

const css = readFileSync(findStyleCss(), 'utf8')

describe('设计 token 纪律', () => {
  it('解析得到 token 且扫描到字面值（防止检查本身静默失效）', () => {
    const tokens = parseRootTokens(css)
    const hits = scanComponentLiterals(css)
    // 结构一旦改动（:root 改名、换了写法），下面几条断言会全部空过——
    // 所以先钉住「解析器确实读到了东西」
    expect(Object.keys(tokens).length).toBeGreaterThan(30)
    expect(hits.length).toBeGreaterThan(30)
  })

  it('组件层不得出现与 token 值完全相等的字面色值', () => {
    const { replaceable } = lintDesignTokens(css)
    expect(
      replaceable.map(h => `style.css:${h.line} ${h.hex}`),
      '这些字面值与既有 token 逐位相等，应当写成 var(--…)',
    ).toEqual([])
  })

  it('近中性字面值只能是指定的几处例外', () => {
    const { neutralOffenders } = lintDesignTokens(css)
    expect(
      neutralOffenders.map(h => `style.css:${h.line} ${h.hex}`),
      '中性色必须带 220° 蓝相（AGENTS 硬约束 1）；新增的先补 token，再考虑进白名单',
    ).toEqual([])
  })

  it.each(TEXT_ON_TINT_PAIRS)('文字档 %s 压在 %s 上必须过 WCAG AA', (textToken, tintToken) => {
    const tokens = parseRootTokens(css)
    const text = tokens[textToken]
    const tint = tokens[tintToken]
    expect(text, `缺少 token ${textToken}`).toBeTruthy()
    expect(tint, `缺少 token ${tintToken}`).toBeTruthy()
    const ratio = contrastRatio(text, tint)
    expect(ratio, `${text} on ${tint}`).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
  })

  // 守卫的守卫：一条永远不会红的断言等于没有断言。
  // 这里往 CSS 文本里注入三种真实会发生的回退，要求每种都被抓到。
  describe('注入回退时必须报错', () => {
    // 注入锚点必须**真的存在且唯一**：2026-10-18 排查发现，原先用的 `.sidebar {`
    // 在选择器重构后被删掉了，`replace` 空改，`mutated === css`，这三条自测一直在空过触发。
    // 现在统一用 `inject()` 注入，并在它里面断言锚点存在——锚点消失时**自测本身**先红。
    const ANCHOR = '.meeting-block {'
    const inject = (declaration: string): string => {
      expect(css, `注入锚点 ${ANCHOR} 不存在，自测会空过——请换一个仍存在的选择器`).toContain(ANCHOR)
      const mutated = css.replace(ANCHOR, `${ANCHOR} ${declaration}`)
      expect(mutated, `注入 ${declaration} 后内容没变，说明锚点没命中`).not.toBe(css)
      return mutated
    }

    it('组件层把 token 写回字面值', () => {
      // 不能直接 replace 第一个 'var(--blue)'：那一处在 :root（`--accent: var(--blue)`），
      // 而扫描范围不含 :root，这样这条自测会空过（第一次就踩了这个坑）
      expect(lintDesignTokens(inject('outline: 1px solid #1f69e0;')).replaceable.length).toBeGreaterThan(0)
    })

    it('新增一个不带蓝相的纯灰', () => {
      expect(lintDesignTokens(inject('color: #888888;')).neutralOffenders.length).toBeGreaterThan(0)
    })

    it('把文字档调浅到 AA 以下', () => {
      const tokens = parseRootTokens(css)
      const textToken = TEXT_ON_TINT_PAIRS[0][0]
      const mutated = css.replace(`${textToken}: ${tokens[textToken]}`, `${textToken}: #7fd69a`)
      expect(mutated).not.toBe(css)
      expect(lintDesignTokens(mutated).contrastFailures.length).toBeGreaterThan(0)
    })
  })
})
