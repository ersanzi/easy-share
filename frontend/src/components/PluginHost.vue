<script setup lang="ts">
// PluginHost：插件页容器。插件 UI 跑在 sandbox iframe（opaque origin）中，
// 经 usePlugins 的 postMessage 桥调宿主能力。本组件只负责挂载与登记。
import { computed, onBeforeUnmount, ref } from 'vue'
import { usePlugins } from '../composables/usePlugins'
import type { PluginInfo } from '../types/core'

const props = defineProps<{ info: PluginInfo }>()
const plugin = usePlugins()

const frame = ref<HTMLIFrameElement | null>(null)
// 入口 URL：/plugins/{id}/{entry}；entry 缺省 index.html（目录访问也会回退）。
// key 含版本号：插件原地更新后（目录已原子替换、响应 no-store）iframe 随 key 变化自动重载。
const src = computed(() => `/plugins/${props.info.id}/${props.info.entry || 'index.html'}`)
const frameKey = computed(() => `${props.info.id}:${props.info.version}`)

const onLoad = () => {
  if (frame.value?.contentWindow) plugin.registerFrame(frame.value.contentWindow, props.info)
}

onBeforeUnmount(() => {
  if (frame.value?.contentWindow) plugin.unregisterFrame(frame.value.contentWindow)
})
</script>

<template>
  <div class="plugin-host">
    <iframe
      ref="frame"
      :key="frameKey"
      class="plugin-frame"
      :src="src"
      sandbox="allow-scripts"
      :title="info.name"
      @load="onLoad"
    />
  </div>
</template>

<style scoped>
.plugin-host {
  flex: 1;
  min-height: 0;
  display: flex;
  border-radius: 14px;
  overflow: hidden;
  background: #fff;
}
.plugin-frame {
  flex: 1;
  width: 100%;
  border: 0;
}
</style>
