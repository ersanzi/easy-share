import { reactive } from 'vue'

// 全局确认对话框状态：App.vue 挂载一个 ConfirmDialog 实例，任何位置（含
// 不在组件树内的 composable，如 useEasyShare）都可 await confirm(...) 获取结果。
export interface ConfirmOptions {
  title: string
  message: string
  confirmText?: string
  danger?: boolean
}

const state = reactive({
  open: false,
  title: '',
  message: '',
  confirmText: '确认',
  danger: false,
})

let resolver: ((value: boolean) => void) | null = null

export function useConfirm() {
  // 再次调用时若上一对话框仍未关闭，按「取消」了结旧请求，避免 Promise 悬挂
  const confirm = (opts: ConfirmOptions): Promise<boolean> => {
    if (resolver) resolver(false)
    state.title = opts.title
    state.message = opts.message
    state.confirmText = opts.confirmText ?? '确认'
    state.danger = opts.danger ?? false
    state.open = true
    return new Promise(resolve => { resolver = resolve })
  }

  const settle = (value: boolean) => {
    state.open = false
    resolver?.(value)
    resolver = null
  }

  return { confirmState: state, confirm, settle }
}
