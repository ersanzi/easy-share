// 预览专用 vite 配置：只为在浏览器里跑真实界面截图用。
// 根目录是 preview 目录（index.html + mock.ts 打桩），源码从 frontend/src 以 /@fs 绝对路径加载，
// 依赖（vue 等）由 frontend/node_modules 解析——resolve.modules 指向它，否则 /@fs 下的源码找不到 vue。
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const here = path.dirname(fileURLToPath(import.meta.url))
const frontend = path.resolve(here, '../../../../frontend')

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    modules: [path.join(frontend, 'node_modules'), 'node_modules'],
  },
  server: {
    fs: { allow: [here, frontend] },
  },
})
