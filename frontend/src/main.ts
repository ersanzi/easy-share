import { createApp } from 'vue'
import App from './App.vue'
import { core } from './services/core'
import './style.css'

const report = (context: string, value: unknown) => {
  const error = value instanceof Error ? value : new Error(String(value))
  try {
    void Promise.resolve(core.reportError(`${context}: ${error.message}`, error.stack ?? ''))
      .catch(() => undefined)
  } catch {
    // The Wails bridge may not be ready while the page itself is failing.
  }
}

window.addEventListener('error', event => report('window error', event.error ?? event.message))
window.addEventListener('unhandledrejection', event => report('unhandled promise rejection', event.reason))

const app = createApp(App)
app.config.errorHandler = (error, _instance, info) => report(`Vue ${info}`, error)
app.mount('#app')
