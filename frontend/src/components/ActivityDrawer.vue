<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { TransferTask } from '../types/core'
import {
  compareTasks,
  formatBytes,
  formatTaskDuration,
  taskActionLabel,
  taskAmountLabel,
  taskKind,
  taskKindLabel,
  taskProgress,
  taskSection,
  taskStatusLabel,
} from '../utils/tasks'

const props = withDefaults(defineProps<{
  tasks: TransferTask[]
  limit?: number
}>(), {
  limit: 8,
})

const emit = defineEmits<{
  close: []
  viewAll: []
}>()

const closeButton = ref<HTMLButtonElement | null>(null)
const visibleTasks = computed(() => [...props.tasks]
  .sort(compareTasks)
  .slice(0, Math.max(1, props.limit)))

const sectionDefinitions = [
  { key: 'active' as const, title: '正在进行', hint: '上传、下载与传输' },
  { key: 'attention' as const, title: '需要处理', hint: '等待确认或遇到问题' },
  { key: 'recent' as const, title: '最近完成', hint: '最近的任务结果' },
]

const sections = computed(() => sectionDefinitions
  .map(section => ({
    ...section,
    tasks: visibleTasks.value.filter(task => taskSection(task) === section.key),
  }))
  .filter(section => section.tasks.length > 0))

const activeCount = computed(() => props.tasks.filter(task => taskSection(task) === 'active').length)
const attentionCount = computed(() => props.tasks.filter(task => taskSection(task) === 'attention').length)
const failureCount = computed(() => props.tasks.filter(task => task.status === 'failed').length)
const hiddenCount = computed(() => Math.max(0, props.tasks.length - visibleTasks.value.length))

const close = () => emit('close')
const handleKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') close()
}

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
  closeButton.value?.focus()
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <div class="activity-backdrop" @click.self="close">
    <aside class="activity-drawer" role="dialog" aria-modal="true" aria-labelledby="activity-title">
      <header class="activity-drawer-header">
        <div>
          <span class="section-label">全局活动</span>
          <h2 id="activity-title">活动</h2>
          <p>离开当前页面，任务也会继续运行。</p>
        </div>
        <button ref="closeButton" class="activity-close" type="button" aria-label="关闭活动抽屉" @click="close">×</button>
      </header>

      <div class="activity-summary" aria-label="任务摘要">
        <div>
          <strong>{{ activeCount }}</strong>
          <span>进行中</span>
        </div>
        <div :class="{ warning: attentionCount > 0 }">
          <strong>{{ attentionCount }}</strong>
          <span>需处理</span>
        </div>
        <div :class="{ failed: failureCount > 0 }">
          <strong>{{ failureCount }}</strong>
          <span>失败</span>
        </div>
      </div>

      <div v-if="!sections.length" class="activity-empty">
        <span aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M5 7h14v12H5z"/><path d="M8 4h8M9 11h6M9 15h4"/></svg>
        </span>
        <strong>暂无活动</strong>
        <p>发送、接收或上传文件后，进度会出现在这里。</p>
      </div>

      <div v-else class="activity-sections">
        <section v-for="section in sections" :key="section.key" class="activity-section" :data-section="section.key">
          <header>
            <strong>{{ section.title }}</strong>
            <span>{{ section.hint }}</span>
          </header>

          <article
            v-for="item in section.tasks"
            :key="item.id"
            class="activity-item"
            :data-task-id="item.id"
          >
            <div :class="['activity-kind-icon', taskKind(item)]" :aria-label="taskKindLabel(item)">
              <svg v-if="taskKind(item) === 'cloud_upload' || taskKind(item) === 'cloud_download'" viewBox="0 0 24 24">
                <path d="M6 18a4 4 0 0 1-.7-7.94A6.5 6.5 0 0 1 17.9 10 4 4 0 0 1 18 18H6z"/>
                <path :d="taskKind(item) === 'cloud_upload' ? 'M12 16V9m0 0-2.5 2.5M12 9l2.5 2.5' : 'M12 9v7m0 0-2.5-2.5M12 16l2.5-2.5'"/>
              </svg>
              <svg v-else-if="taskKind(item) === 'sync'" viewBox="0 0 24 24">
                <path d="M19 8a7 7 0 0 0-12-2L4 9m0 0V4m0 5h5M5 16a7 7 0 0 0 12 2l3-3m0 0v5m0-5h-5"/>
              </svg>
              <svg v-else-if="taskKind(item) === 'parse'" viewBox="0 0 24 24">
                <path d="M6 3h8l4 4v14H6zM14 3v5h5M9 12h6M9 16h4"/>
              </svg>
              <svg v-else viewBox="0 0 24 24">
                <path d="M5 5h14v14H5z"/>
                <path :d="taskKind(item) === 'lan_send' ? 'M12 16V8m0 0-3 3m3-3 3 3' : 'M12 8v8m0 0-3-3m3 3 3-3'"/>
              </svg>
            </div>

            <div class="activity-item-content">
              <div class="activity-item-heading">
                <div>
                  <strong>{{ item.fileName }}</strong>
                  <span>{{ taskActionLabel(item) }} · {{ taskAmountLabel(item) }}</span>
                </div>
                <span :class="['task-status', item.status]">{{ taskStatusLabel(item.status) }}</span>
              </div>

              <div class="activity-progress" role="progressbar" :aria-valuenow="taskProgress(item)" aria-valuemin="0" aria-valuemax="100">
                <i :style="{ width: `${taskProgress(item)}%` }" />
              </div>

              <div class="activity-meta">
                <span>{{ taskProgress(item) }}%</span>
                <span v-if="item.speed > 0 && item.status === 'running'" class="speed-active">{{ formatBytes(item.speed) }}/s</span>
                <span v-if="formatTaskDuration(item)">用时 {{ formatTaskDuration(item) }}</span>
                <span v-if="item.error" class="activity-error" :title="item.error">{{ item.error }}</span>
              </div>
            </div>
          </article>
        </section>
      </div>

      <footer class="activity-drawer-footer">
        <span v-if="hiddenCount">另有 {{ hiddenCount }} 项任务</span>
        <span v-else>{{ tasks.length ? '任务状态由 Core 持久保存' : '任务状态将由 Core 持久保存' }}</span>
        <button class="primary-button compact" type="button" @click="emit('viewAll')">查看全部任务</button>
      </footer>
    </aside>
  </div>
</template>