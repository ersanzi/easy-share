<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

// 思维导图：真正画出来的放射状导图（对标钉钉听记的思维导图区块）。
//
// 历史：2026-10-12 首次集成 markmap 时页面是空白，当时没深查就降级成了自绘嵌套列表。
// 根因是三条，缺一条就是空白（2026-10-18 实测确认）：
//   1. SVG 没有确定的像素尺寸（flex 容器里 height 落到 0）→ 必须有显式 height
//   2. 必须在挂载后下一帧再 create，容器还没进 DOM/未布局时测到的尺寸是 0
//   3. 数据要经 Transformer.transform 转成 { data }，不能把 markdown 直接塞给 Markmap
// 这三点在这里都用可断言的形态固定住（tick → 测量 → 建图 → fit）。
//
// 依赖体积：markmap-view + d3 约 400KB，只在「打开一条带导图的听记」时用得上，所以**动态 import**。
// 2026-10-18 实测：静态引入把主包从 ~575KB 顶到 975KB，改为按需加载。

const props = defineProps<{
  markdown: string
  /** 过渡动画时长（测试传 0 关掉 d3 过渡，避免无头环境读 transform 矩阵） */
  duration?: number
  /** 库加载器替身（仅测试用；生产走动态 import，见下方 defaultLoader） */
  loader?: () => Promise<{
    lib: typeof import('markmap-lib')
    viewer: typeof import('markmap-view')
  }>
}>()

const host = ref<HTMLDivElement | null>(null)
const svg = ref<SVGSVGElement | null>(null)
const empty = ref(false)
const error = ref('')
const zoom = ref(100)

let view: import('markmap-view').Markmap | null = null
let transformer: import('markmap-lib').Transformer | null = null

// markmap 内部依赖 ResizeObserver（跟随容器尺寸重排）。老 WebView / 测试环境可能没有：
// 缺了会直接抛 ReferenceError 让整张图空白，所以这里垫一个"什么都不做"的实现，
// 并配合下面的 window resize 兜底重排。
const ensureResizeObserver = () => {
  if (typeof globalThis.ResizeObserver === 'function') return
  class NoopResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  ;(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver
}

const nextFrame = () => new Promise<void>(resolve => requestAnimationFrame(() => resolve()))

// markmap 的库加载入口抽成可替换的依赖：一是按需加载（不压主包），
// 二是让组件测试可以注入替身（jsdom 里走真实动态 import 会被外部化，Promise 不 resolve）。
const defaultLoader = () =>
  Promise.all([import('markmap-lib'), import('markmap-view')]).then(([lib, v]) => ({ lib, viewer: v }))

const loadMarkmap = () => (props.loader ? props.loader() : defaultLoader())

// 渲染序号：markdown 变化或重渲染时，旧的那次异步任务必须自行作废。
// 不加这个会出现「A 建了 view → B 判空把 view 置 null → A 继续 setData 报 null.fit」的竞态
// （2026-10-18 实测：onMounted 与 watch 各触发一次时就复现）。
let renderSeq = 0

const render = async () => {
  const seq = ++renderSeq
  const source = (props.markdown || '').trim()
  error.value = ''
  empty.value = !source
  if (!source) {
    view?.destroy?.()
    view = null
    return
  }
  ensureResizeObserver()
  // 关键：等一帧，让样式与布局落定（容器尺寸为 0 时 markmap 会算出 NaN 坐标，画出来就是空白）
  await nextFrame()
  if (seq !== renderSeq) return // 等待期间又有新的渲染请求，本次作废
  if (!svg.value) return
  const box = host.value?.getBoundingClientRect()
  if (!box || box.width < 40 || box.height < 40) {
    error.value = '导图容器尺寸异常，请稍后重试'
    return
  }
  try {
    const { lib, viewer } = await loadMarkmap()
    if (seq !== renderSeq) return
    if (!transformer) transformer = new lib.Transformer()
    const { root } = transformer.transform(source)
    if (!view) {
      view = viewer.Markmap.create(svg.value, {
        // duration 由外部可调：无头/测试环境没有真实 transform 矩阵，d3 过渡插值会抛 baseVal 错误
        duration: props.duration ?? 200,
        maxWidth: 320,
        spacingHorizontal: 90,
        spacingVertical: 12,
        paddingX: 12,
        initialExpandLevel: -1,
      })
    }
    // 再用局部变量兜一层：create 失败时 view 为 null，别让错误在 `view.fit()` 处变成
    // 一句没头没尾的 "reading 'fit'"，掩盖真正的失败点
    const instance = view
    if (!instance) throw new Error('markmap 视图创建失败')
    await instance.setData(root)
    await instance.fit()
    zoom.value = currentZoom()
  } catch (e) {
    // 把内部错误暴露到界面上：当初的"空白"就是异常被吞掉、只看到一片白
    error.value = e instanceof Error ? e.message : String(e)
    console.error('[MindMap] 渲染失败', e, (e as Error)?.stack)
  }
}

const currentZoom = () => {
  // markmap 把 d3-zoom 的变换挂在 svg.__zoom 上（state 里没有 scale 字段，类型定义可查 types.d.ts）
  const node = svg.value as (SVGSVGElement & { __zoom?: { k?: number } }) | null
  return Math.round((node?.__zoom?.k || 1) * 100)
}

const zoomBy = async (factor: number) => {
  if (!view) return
  await view.rescale?.(factor)
  zoom.value = currentZoom()
}

const resetView = async () => {
  if (!view) return
  await view.fit()
  zoom.value = currentZoom()
}

const downloadSvg = () => {
  if (!svg.value) return
  const data = new XMLSerializer().serializeToString(svg.value)
  const blob = new Blob([data], { type: 'image/svg+xml;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = '思维导图.svg'
  anchor.click()
  URL.revokeObjectURL(url)
}

onMounted(render)
// 只监听内容变化，**不要**写成 `watch([() => props.markdown, empty], render)` 这类会在首轮触发的形式：
// 首轮会走「内容为空」分支把正在建图的 view 销毁掉，随后 `view.setData` 报
// "Cannot read properties of null (reading 'fit')" —— 实测踩过。
watch(() => props.markdown, render)
onBeforeUnmount(() => {
  view?.destroy?.()
  view = null
})
</script>

<template>
  <div class="mindmap-wrap">
    <div class="mindmap-toolbar">
      <span class="mindmap-zoom">缩放 {{ zoom }}%</span>
      <button class="text-button" type="button" @click="zoomBy(1.2)">放大</button>
      <button class="text-button" type="button" @click="zoomBy(1 / 1.2)">缩小</button>
      <button class="text-button" type="button" @click="resetView">适应画面</button>
      <button class="text-button" type="button" @click="downloadSvg">导出 SVG</button>
    </div>
    <div ref="host" class="mindmap-host">
      <!-- 导图容器必须有确定高度：这是当初"空白"的头号原因 -->
      <svg ref="svg" class="mindmap-svg" role="img" aria-label="会议思维导图" />
      <p v-if="empty" class="meeting-hint">还没有导图，点「生成导图」用 AI 从转写提炼结构</p>
      <p v-else-if="error" class="meeting-hint meeting-failed">导图渲染失败：{{ error }}</p>
    </div>
    <p class="mindmap-tip">点击节点可折叠/展开，滚轮缩放，拖拽平移</p>
  </div>
</template>
