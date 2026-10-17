<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = withDefaults(defineProps<{
  open: boolean
  title: string
  message: string
  confirmText?: string
  danger?: boolean
}>(), {
  confirmText: '确认',
  danger: false,
})

const emit = defineEmits<{
  confirm: []
  cancel: []
  'update:open': [value: boolean]
}>()

const cancelButton = ref<HTMLButtonElement | null>(null)
const confirmButton = ref<HTMLButtonElement | null>(null)

const confirm = () => {
  emit('update:open', false)
  emit('confirm')
}
const cancel = () => {
  emit('update:open', false)
  emit('cancel')
}

const handleKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') cancel()
}

// 打开时焦点进入对话框：普通操作聚焦「取消」（防误触回车即执行），
// danger 操作同样聚焦「取消」——红色确认按钮需明确点击才能触发。
const focusInitial = async () => {
  await nextTick()
  cancelButton.value?.focus()
}
watch(() => props.open, open => { if (open) void focusInitial() })

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
  if (props.open) void focusInitial()
})
onBeforeUnmount(() => document.removeEventListener('keydown', handleKeydown))
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="confirm-overlay" @click.self="cancel">
      <div
        class="confirm-card"
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-dialog-title"
        aria-describedby="confirm-dialog-message"
      >
        <h2 id="confirm-dialog-title" class="confirm-title">{{ title }}</h2>
        <p id="confirm-dialog-message" class="confirm-message">{{ message }}</p>
        <div class="confirm-actions">
          <button ref="cancelButton" class="secondary-button compact" type="button" @click="cancel">取消</button>
          <button
            ref="confirmButton"
            :class="['confirm-submit compact', { danger }]"
            type="button"
            @click="confirm"
          >{{ confirmText }}</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
