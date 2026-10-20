// 设计 token 纪律的判定规则（唯一真相源）。
//
// 为什么放这里而不是只写在检查脚本里：这条纪律要能被 `vitest`/`vue-tsc` 挡住，
// 否则「组件里不写字面色值」「中性色必须带蓝相」就永远只是一句自述——
// 本轮之前它已经是自述了三个月，实测攒下 28 种近中性灰。
//
// 手工修复工具在 `scripts/diag/color-literals.mjs`（带 --apply 系列），
// 它负责改文件；**规则以本文件为准**，两边不一致时以测试报错为先。

export type Rgb = { r: number; g: number; b: number }

export type Hsl = { h: number; s: number; l: number }

/** 把 #rgb / #rrggbb 解析成 0-255 三分量 */
export function hexToRgb(hex: string): Rgb {
  const body = hex.replace('#', '')
  const full = body.length === 3 ? body.split('').map(c => c + c).join('') : body
  return {
    r: parseInt(full.slice(0, 2), 16),
    g: parseInt(full.slice(2, 4), 16),
    b: parseInt(full.slice(4, 6), 16),
  }
}

export function rgbToHsl({ r, g, b }: Rgb): Hsl {
  const rn = r / 255, gn = g / 255, bn = b / 255
  const max = Math.max(rn, gn, bn), min = Math.min(rn, gn, bn), d = max - min
  let h = 0
  if (d !== 0) {
    if (max === rn) h = ((gn - bn) / d) % 6
    else if (max === gn) h = (bn - rn) / d + 2
    else h = (rn - gn) / d + 4
    h = Math.round(h * 60)
    if (h < 0) h += 360
  }
  const l = (max + min) / 2
  const s = d === 0 ? 0 : d / (1 - Math.abs(2 * l - 1))
  return { h, s: Math.round(s * 100), l: Math.round(l * 100) }
}

export function hexToHsl(hex: string): Hsl {
  return rgbToHsl(hexToRgb(hex))
}

/** WCAG 2.1 相对亮度 */
export function relativeLuminance(hex: string): number {
  const channels = [hex.slice(1, 3), hex.slice(3, 5), hex.slice(5, 7)].map(pair => {
    const c = parseInt(pair, 16) / 255
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
  })
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

/** WCAG 对比度（1..21） */
export function contrastRatio(foreground: string, background: string): number {
  const l1 = relativeLuminance(foreground)
  const l2 = relativeLuminance(background)
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05)
}

/** 语义色的「文字档 / 浅底档」配对：文字档压在对应浅底上必须过 AA */
export const TEXT_ON_TINT_PAIRS: ReadonlyArray<readonly [string, string]> = [
  ['--green-text', '--green-tint'],
  ['--red-text', '--red-tint'],
  ['--amber-text', '--amber-tint'],
  ['--purple-text', '--purple-tint'],
]

/** AA 正文阈值。图标/色块不算文字，不在这条断言范围内 */
export const AA_NORMAL_TEXT = 4.5

/**
 * 允许留在组件层的近中性字面值（S < 15%）。
 *
 * 白名单不是「懒得改」，而是每一条都有说得出理由的例外：
 *   - 纯白：既可能是底面也可能是字色/描边，语义要人判，不能机械并档；
 *   - 深色模式块：深色主题还没有成体系的档位，并进浅色阶会错色。
 * 新增近中性字面值应当先补 token，再从这里删掉一条。
 */
export const NEUTRAL_ALLOWLIST: Readonly<Record<string, string>> = {
  '#ffffff': '纯白：底面/字色/描边语义要人判',
  '#c7c7cc': '.summary-arrow 装饰图标灰，非文字',
  '#2a2a2d': '深色模式底色（深色档未成体系）',
  '#e7e8eb': '同一行既有文字色又有底色，角色判定不敢二选一',
}

/** 导出物皮肤区（图文海报 / 汇报卡）：色带与后端 docx_export.ACCENTS 同源，不参与本纪律 */
export const EXPORT_THEME_MARKER = '图文海报'

/**
 * 值相等也不算「机械可换」的颜色：纯白与纯黑。
 *
 * `#ffffff` 与 `--bg` 逐位相等，但它可能是文字色、描边色或图标填充——
 * 换成 `var(--bg)` 语义就错了（把「白字」写成「背景色」），
 * 所以这两色只允许人判，不进自动并档。
 */
export const ROLE_AMBIGUOUS_VALUES: ReadonlySet<string> = new Set(['#ffffff', '#000000'])

export type TokenMap = Record<string, string>

/** 解析 :root 里的 token；`var()` 引用会顺着解开 */
export function parseRootTokens(css: string): TokenMap {
  const lines = css.split('\n')
  const start = lines.findIndex(line => line.startsWith(':root {'))
  if (start < 0) throw new Error('style.css 里找不到 :root 块')
  let end = start
  while (end < lines.length && lines[end].trim() !== '}') end++

  const raw = new Map<string, string>()
  for (let i = start; i <= end; i++) {
    const m = lines[i].match(/^\s*(--[a-z0-9-]+):\s*([^;]+);/i)
    if (m) raw.set(m[1], m[2].trim())
  }
  const out: TokenMap = {}
  for (const name of raw.keys()) {
    const value = resolveToken(raw, name, new Set())
    if (value) out[name] = value
  }
  return out
}

function resolveToken(raw: Map<string, string>, name: string, seen: Set<string>): string | null {
  const value = raw.get(name)
  if (value === undefined || seen.has(name)) return null
  seen.add(name)
  const hex = value.match(/^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/)
  if (hex) return normalizeHex('#' + hex[1])
  const ref = value.match(/^var\((--[a-z0-9-]+)\)$/)
  return ref ? resolveToken(raw, ref[1], seen) : null
}

export function normalizeHex(hex: string): string {
  const body = hex.replace('#', '')
  const full = body.length === 3 ? body.split('').map(c => c + c).join('') : body
  return ('#' + full).toLowerCase()
}

export type LiteralHit = { hex: string; line: number; neutral: boolean }

/** 扫组件层（:root 之外、导出物皮肤之前）的字面色值 */
export function scanComponentLiterals(css: string): LiteralHit[] {
  const lines = css.split('\n')
  const rootStart = lines.findIndex(line => line.startsWith(':root {'))
  let rootEnd = rootStart
  while (rootEnd < lines.length && lines[rootEnd].trim() !== '}') rootEnd++
  const exportStart = lines.findIndex(line => line.includes(EXPORT_THEME_MARKER))
  const stop = exportStart > 0 ? exportStart : lines.length

  const hits: LiteralHit[] = []
  const re = /#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})\b/g
  for (let i = rootEnd + 1; i < stop; i++) {
    const line = lines[i]
    if (line.trimStart().startsWith('/*') || line.includes('--p-')) continue
    for (const m of line.matchAll(re)) {
      const hex = normalizeHex(m[0])
      hits.push({ hex, line: i + 1, neutral: hexToHsl(hex).s < 15 })
    }
  }
  return hits
}

export type LintResult = {
  /** 与某个 token 值逐位相等、却仍写成字面值的地方——纯机械可换 */
  replaceable: LiteralHit[]
  /** 近中性字面值里不在白名单中的（违反「中性色必须带蓝相」） */
  neutralOffenders: LiteralHit[]
  /** 对比度不达 AA 的文字档/浅底档配对 */
  contrastFailures: Array<{ text: string; tint: string; ratio: number }>
}

export function lintDesignTokens(css: string): LintResult {
  const tokens = parseRootTokens(css)
  const byValue = new Map<string, string>()
  for (const [name, value] of Object.entries(tokens)) {
    if (!byValue.has(value)) byValue.set(value, name)
  }
  const hits = scanComponentLiterals(css)
  const replaceable = hits.filter(
    hit => byValue.has(hit.hex) && !ROLE_AMBIGUOUS_VALUES.has(hit.hex),
  )
  const neutralOffenders = hits.filter(
    hit => hit.neutral && !(hit.hex in NEUTRAL_ALLOWLIST),
  )

  const contrastFailures: LintResult['contrastFailures'] = []
  for (const [textToken, tintToken] of TEXT_ON_TINT_PAIRS) {
    const text = tokens[textToken]
    const tint = tokens[tintToken]
    if (!text || !tint) {
      contrastFailures.push({ text: textToken, tint: tintToken, ratio: 0 })
      continue
    }
    const ratio = contrastRatio(text, tint)
    if (ratio < AA_NORMAL_TEXT) contrastFailures.push({ text, tint, ratio })
  }
  return { replaceable, neutralOffenders, contrastFailures }
}
