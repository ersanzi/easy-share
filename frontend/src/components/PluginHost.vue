<script setup lang="ts">
// PluginHost：插件页容器。插件 UI 跑在 sandbox iframe（opaque origin）中，
// 经 usePlugins 的 postMessage 桥调宿主能力。本组件只负责挂载与登记。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { usePlugins } from '../composables/usePlugins'
import type { PluginInfo } from '../types/core'

const props = defineProps<{ info: PluginInfo }>()
const plugin = usePlugins()

const frame = ref<HTMLIFrameElement | null>(null)
// 入口 URL：/plugins/{id}/{entry}；entry 缺省 index.html（目录访问也会回退）。
// key 含版本号：插件原地更新后（目录已原子替换、响应 no-store）iframe 随 key 变化自动重载。
const src = computed(() => `/plugins/${props.info.id}/${props.info.entry || 'index.html'}`)
const frameKey = computed(() => `${props.info.id}:${props.info.version}`)

// 登记必须赶在插件脚本执行之前：iframe 的 @load 在页面脚本跑完之后才触发，
// 而插件 app.js 初始化即发起能力调用，等 load 再登记，首批调用会被
// usePlugins 的来源校验丢弃（表现为插件页首开时历史/待办为空）。
// contentWindow 取到的是稳定 WindowProxy，元素插入即可获取，导航后身份不变。
watch(frame, (el, prev) => {
  if (prev?.contentWindow) plugin.unregisterFrame(prev.contentWindow)
  if (el?.contentWindow) plugin.registerFrame(el.contentWindow, props.info)
})

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
