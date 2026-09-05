<script setup lang="ts">
import { onMounted, ref } from 'vue'

defineProps<{ loading: boolean; error: string }>()
const emit = defineEmits<{ (e: 'submit', username: string, password: string): void }>()

const username = ref('')
const password = ref('')
const passwordInput = ref<HTMLInputElement | null>(null)

// 不预填 admin：公司部署里每个同事用自己账号，预填管理员名等于诱导试默认口令
onMounted(() => passwordInput.value?.focus())

const submit = () => {
  if (!username.value.trim() || !password.value) return
  emit('submit', username.value.trim(), password.value)
}
</script>

<template>
  <div class="login-shell">
    <div class="login-card">
      <div class="login-brand">
        <div class="login-icon" aria-hidden="true">
          <svg viewBox="0 0 32 32"><path d="M8.5 12a6.5 6.5 0 0 1 12.6-2.2A5.5 5.5 0 1 1 22.5 20H8a5 5 0 0 1 .5-8Z"/><path d="m12 15 4-4 4 4M16 11v11"/></svg>
        </div>
        <div class="login-titles">
          <strong>EasyShare</strong>
          <span>登录以进入你的空间</span>
        </div>
      </div>

      <form class="login-form" @submit.prevent="submit">
        <label class="login-field">
          <span>账号</span>
          <input v-model="username" type="text" autocomplete="username" placeholder="用户名" />
        </label>
        <label class="login-field">
          <span>密码</span>
          <input ref="passwordInput" v-model="password" type="password" autocomplete="current-password" placeholder="密码" @keyup.enter="submit" />
        </label>

        <p v-if="error" class="login-error" role="alert">{{ error }}</p>

        <button class="login-btn" type="submit" :disabled="loading">
          {{ loading ? '登录中…' : '登录' }}
        </button>
      </form>

      <p class="login-hint">首次使用请联系管理员开通账号</p>
    </div>
  </div>
</template>

<style scoped>
.login-shell {
  height: 100vh;
  display: grid;
  place-items: center;
  background: linear-gradient(160deg, #eef2f8, #e3e9f2);
}
.login-card {
  width: 340px;
  padding: 32px 28px 24px;
  background: #fff;
  border: 1px solid var(--border);
  border-radius: 16px;
  box-shadow: 0 12px 40px rgba(30, 40, 60, 0.12);
}
.login-brand { display: flex; align-items: center; gap: 12px; margin-bottom: 24px; }
.login-icon {
  width: 44px; height: 44px; flex: none;
  display: grid; place-items: center; border-radius: 12px;
  background: linear-gradient(140deg, #4c9bff, var(--blue));
  color: #fff;
}
.login-icon svg { width: 26px; height: 26px; fill: none; stroke: currentColor; stroke-width: 1.9; stroke-linecap: round; stroke-linejoin: round; }
.login-titles { display: flex; flex-direction: column; }
.login-titles strong { font-size: 18px; font-weight: 700; }
.login-titles span { font-size: 12px; color: var(--muted); }
.login-form { display: flex; flex-direction: column; gap: 14px; }
.login-field { display: flex; flex-direction: column; gap: 6px; }
.login-field span { font-size: 12px; color: var(--muted); }
.login-field input {
  height: 40px; padding: 0 12px;
  border: 1px solid var(--border); border-radius: 10px;
  font-size: 14px; outline: none; transition: border-color 0.15s;
}
.login-field input:focus { border-color: var(--blue); }
.login-error { margin: 0; color: var(--red); font-size: 13px; }
.login-btn {
  height: 42px; margin-top: 4px; border: none; border-radius: 10px;
  background: var(--blue); color: #fff; font-size: 15px; font-weight: 600; cursor: pointer;
  transition: background 0.15s;
}
.login-btn:hover:not(:disabled) { background: var(--blue-dark); }
.login-btn:disabled { opacity: 0.6; cursor: default; }
.login-hint { margin: 18px 0 0; text-align: center; font-size: 12px; color: var(--muted); }

@media (prefers-color-scheme: dark) {
  .login-shell { background: linear-gradient(160deg, #1c1f26, #14161b); }
  .login-card { background: #23262d; }
  .login-field input { background: #1a1c22; color: #f5f5f7; }
}
</style>
