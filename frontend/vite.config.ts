import {defineConfig} from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  test: {
    environment: 'jsdom',
    // 思维导图组件依赖 markmap-view → d3。jsdom 没有 SVG 布局引擎（`<g>.transform` 为 undefined），
    // d3 的过渡插值会抛 "reading 'baseVal'"。测试里给 SVG 原型补了恒等变换桩，但
    // **d3 走的是 ESM 副本、桩要打在同一份原型上**才有用——inline 让 vite 参与处理这两个包，
    // 使它们与组件共用同一模块实例（详见 components/__tests__/MindMap.test.ts 顶部注释）。
    server: { deps: { inline: ['markmap-view', 'markmap-lib', 'd3'] } },
  }
})
