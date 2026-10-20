// 思维导图组件回归：证明 markmap 真的画出了节点。
//
// 这条用例的存在理由：2026-10-12 集成 markmap 时画布空白，当时没深查就降级成自绘列表。
// 2026-10-18 定位到三个必要条件（容器有高度 / 挂载后下一帧再建图 / 数据经 Transformer 转换），
// 这里用 jsdom 把「SVG 里真的出现 <g class="markmap-node">」钉成断言，防止再退回去。
// jsdom 没有布局引擎，getBoundingClientRect 恒为 0——所以本用例显式打桩尺寸，
// 这也正是排查当时空白的第一个怀疑点。
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// ── jsdom 的 SVG 布局缺件补齐（必须在 import 组件之前生效）──
// 原因：d3-interpolate 的 parseSvg 在模块加载时就 createElementNS("…svg","g") 并缓存该节点，
// 之后读 `node.transform.baseVal`。jsdom 没有 SVG 布局引擎，`transform` 是 undefined，
// 于是报 "Cannot read properties of undefined (reading 'baseVal')"——看起来像 markmap 的 bug，
// 其实是测试环境缺件。
//
// 坑（实测两次才对）：vitest 的 jsdom 环境把 SVGElement 垫成了中间类，
// 节点真实构造函数是 internals 里的 SVGGElement，`node.constructor === window.SVGElement` 为 false；
// 桩打在 SVGElement.prototype 上不生效。可靠做法是先用 createElementNS 拿到真实构造函数再打桩。
const IDENTITY_TRANSFORM = {
  baseVal: {
    numberOfItems: 1,
    consolidate: () => ({ matrix: { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 } }),
  },
}
// markmap 内部 la() 还会读 `svg.width.baseVal.value` / `svg.height.baseVal.value`（量 viewport），
// jsdom 同样没有。补一个固定尺寸即可，坐标计算不依赖真实布局的绝对值。
const SVG_LENGTH = (value: number) => ({ baseVal: { value } })
const IDENTITY_LENGTH_STUB = {
  baseVal: { value: 720 },
}
for (const tag of ['g', 'svg', 'path', 'line', 'foreignObject']) {
  let node: SVGElement
  try {
    node = document.createElementNS('http://www.w3.org/2000/svg', tag) as SVGElement
  } catch {
    continue // jsdom 不支持这个标签时跳过
  }
  const proto = (node.constructor as { prototype: object }).prototype
  Object.defineProperty(proto, 'transform', { configurable: true, get: () => IDENTITY_TRANSFORM })
  for (const prop of ['width', 'height', 'x', 'y']) {
    if (prop in proto) continue
    Object.defineProperty(proto, prop, {
      configurable: true,
      get: () => (prop === 'height' ? SVG_LENGTH(420) : IDENTITY_LENGTH_STUB),
    })
  }
}

// jsdom 也没有 SVG 的 getScreenCTM / getBBox：markmap 用它换算坐标，
// 缺了会在 fit() 阶段抛错（节点已画进 DOM，但后续重排失败）。
for (const tag of ['g', 'svg']) {
  const node = document.createElementNS('http://www.w3.org/2000/svg', tag) as SVGElement
  const proto = (node.constructor as { prototype: object }).prototype
  Object.defineProperty(proto, 'getScreenCTM', {
    configurable: true,
    value: () => ({ a: 1, b: 0, c: 0, d: 1, e: 0, f: 0, inverse: () => ({ a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 }) }),
  })
  Object.defineProperty(proto, 'getBBox', {
    configurable: true,
    value: () => ({ x: 0, y: 0, width: 200, height: 40 }),
  })
}

import MindMap from '../MindMap.vue'
import * as markmapLib from 'markmap-lib'
import * as markmapView from 'markmap-view'

// 生产代码里 markmap 是**动态 import**（不压主包）。jsdom 环境下 `import()` 会被外部化、
// Promise 不 resolve，所以测试通过组件暴露的 `loader` 注入同一份真实库：
// 断言的仍然是「markmap 真的把节点画进了 SVG」，只是绕开了环境对动态导入的限制。
const loader = async () => ({ lib: markmapLib, viewer: markmapView })

const MARKDOWN = ['# 产品周会', '## 部署', '- 李四跟进', '- 周五前给排期', '## 演示', '- 提前准备'].join('\n')

const flush = (ms = 60) => new Promise(resolve => setTimeout(resolve, ms))

/** 轮询等到条件成立（导出的是动态 import，模块解析是异步的，固定 sleep 会偶发不稳）。 */
async function waitFor(check: () => boolean, timeout = 3000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    if (check()) return true
    await flush(30)
  }
  return false
}

describe('MindMap', () => {
  beforeEach(() => {
    // markmap 内部用 ResizeObserver 监听容器尺寸；jsdom 没实现它（真实浏览器有）。
    // 这不是组件缺陷，但也提醒：老 WebView 同样可能缺，组件侧对缺失做了降级。
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    })
    // jsdom 没有 SVG 布局引擎（原型桩已在文件顶部补好），这里不再重复设置
    // jsdom 不实现布局：给容器一个确定的尺寸，模拟真实浏览器里"有高度"的前提
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      width: 720,
      height: 420,
      top: 0,
      left: 0,
      right: 720,
      bottom: 420,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)
    // rAF 在 jsdom 的 vitest 环境里已存在，这里只是确保同步可控
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation(cb => window.setTimeout(() => cb(0), 0) as unknown as number)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('把 markdown 渲染成真正的 SVG 节点树', async () => {
    // duration: 0 关掉 d3 过渡动画，断言稳定（首帧就是最终坐标）
    const wrapper = mount(MindMap, { props: { markdown: MARKDOWN, duration: 0, loader }, attachTo: document.body })
    const svg = wrapper.find('svg.mindmap-svg')
    expect(svg.exists()).toBe(true)

    // markmap 是动态 import（不压主包），模块解析是异步的——轮询等节点出现
    const rendered = await waitFor(() => svg.element.querySelectorAll('g.markmap-node').length > 0)
    if (!rendered) {
      // 失败时把组件内部的错误文案亮出来，否则只看到 "超时" 无从下手
      console.log('组件状态文案:', wrapper.text())
      console.log('SVG innerHTML 片段:', svg.element.innerHTML.slice(0, 500))
    }
    expect(rendered, '等待 markmap 渲染节点超时').toBe(true)
    // 这一条就是「图真的画出来了」的判据：当年空白时这里恒为 0
    expect(svg.element.querySelectorAll('g.markmap-node').length).toBeGreaterThan(0)
    // 根节点文案来自 markdown 标题
    expect(svg.element.textContent).toContain('产品周会')
    expect(wrapper.text()).not.toContain('导图渲染失败')

    // 卸载后再等一拍，让 markmap 的 rAF/timer 收干净（否则会漏报 unhandled error 污染其它用例）
    wrapper.unmount()
    await flush(150)
  })

  it('空内容给出引导文案而不是崩溃', async () => {
    const wrapper = mount(MindMap, { props: { markdown: '', duration: 0, loader }, attachTo: document.body })
    await flush(150)
    expect(wrapper.text()).toContain('还没有导图')
    wrapper.unmount()
    await flush(150)
  })
})
