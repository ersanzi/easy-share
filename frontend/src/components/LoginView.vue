<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { core } from '../services/core'

defineProps<{ loading: boolean; error: string }>()
const emit = defineEmits<{ (e: 'submit', username: string, password: string): void }>()

const username = ref('')
const password = ref('')
const platformUrl = ref('')
const usernameInput = ref<HTMLInputElement | null>(null)
const passwordInput = ref<HTMLInputElement | null>(null)

// 不预填 admin：公司部署里每个同事用自己账号，预填管理员名等于诱导试默认口令
onMounted(async () => {
  // 聚焦第一个空字段：账号为空聚焦账号，否则聚焦密码
  if (!username.value.trim()) usernameInput.value?.focus()
  else passwordInput.value?.focus()
  // 常显公司服务器地址：登录失败电话支持时，员工一眼能报出连的是谁
  try {
    platformUrl.value = (await core.serviceEndpoints()).platformBaseUrl
  } catch {
    /* 拿不到就空着，不阻塞登录 */
  }
})

const submit = () => {
  if (!username.value.trim() || !password.value) return
  emit('submit', username.value.trim(), password.value)
}
</script>

<template>
  <div class="login-shell">
    <!-- 左侧品牌区：平板底色 + 1px 网格，说明这款产品是什么，不放大图 -->
    <section class="login-intro" aria-label="EasyShare 简介">
      <div class="login-brand">
        <span class="login-mark" aria-hidden="true">
          <svg viewBox="0 0 32 32"><path d="M8.5 12a6.5 6.5 0 0 1 12.6-2.2A5.5 5.5 0 1 1 22.5 20H8a5 5 0 0 1 .5-8Z"/><path d="m12 15 4-4 4 4M16 11v11"/></svg>
        </span>
        <span class="login-wordmark">EasyShare</span>
      </div>

      <div class="login-copy">
        <h1>文件在这台电脑<br>与你的空间之间流动</h1>
        <p>局域网直传、云端网盘与公司知识库，用同一个账号进入。</p>
      </div>

      <ul class="login-points">
        <li>
          <span class="login-point-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
          </span>
          <div><strong>点对点直传</strong><span>同一局域网内直接发送，不经过第三方服务器</span></div>
        </li>
        <li>
          <span class="login-point-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/></svg>
          </span>
          <div><strong>云端网盘</strong><span>文件在「此电脑」里直接访问，不用记地址</span></div>
        </li>
        <li>
          <span class="login-point-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M12 4.5C10.5 3.5 8.5 3 6 3v15c2.5 0 4.5.5 6 1.5 1.5-1 3.5-1.5 6-1.5V3c-2.5 0-4.5.5-6 1.5Z"/><path d="M12 4.5v15"/></svg>
          </span>
          <div><strong>知识问答</strong><span>公司文档检索，答案带引用来源</span></div>
        </li>
      </ul>

      <p class="login-meta">本机服务 · 仅在局域网内可见</p>
    </section>

    <!-- 右侧登录区：扁平白底，不再用悬浮玻璃卡片 -->
    <section class="login-form-panel">
      <div class="login-mobile-brand">
        <span class="login-mark small" aria-hidden="true">
          <svg viewBox="0 0 32 32"><path d="M8.5 12a6.5 6.5 0 0 1 12.6-2.2A5.5 5.5 0 1 1 22.5 20H8a5 5 0 0 1 .5-8Z"/><path d="m12 15 4-4 4 4M16 11v11"/></svg>
        </span>
        <strong>EasyShare</strong>
      </div>

      <div class="login-card">
        <header class="login-header">
          <h2>登录</h2>
          <p>使用公司账号进入你的空间</p>
        </header>

        <form class="login-form" @submit.prevent="submit">
          <label class="login-field">
            <span>账号</span>
            <input ref="usernameInput" v-model="username" type="text" autocomplete="username" placeholder="用户名" />
          </label>
          <label class="login-field">
            <span>密码</span>
            <input ref="passwordInput" v-model="password" type="password" autocomplete="current-password" placeholder="密码" @keyup.enter="submit" />
          </label>

          <p v-if="error" class="login-error" role="alert">{{ error }}</p>

          <button class="login-btn" type="submit" :disabled="loading">
            {{ loading ? '登录中…' : '登录' }}
          </button>
          <p v-if="platformUrl" class="login-server">公司服务器：{{ platformUrl }}</p>
        </form>

        <p class="login-hint">首次使用请联系管理员开通账号</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.login-shell {
  height: calc(100vh - 36px);
  display: grid;
  grid-template-columns: minmax(0, 1.05fr) minmax(420px, .95fr);
  overflow: hidden;
  background: var(--bg);
}
.login-shell input, .login-shell button, .login-shell a { --wails-draggable: no-drag; }

/* 左侧品牌区：平板底 + 极淡网格，不使用渐变与毛玻璃 */
.login-intro {
  position: relative;
  display: flex;
  flex-direction: column;
  padding: 44px 56px 34px;
  overflow: hidden;
  background: var(--bg-chrome);
  border-right: 1px solid var(--border);
}
.login-intro::before {
  content: "";
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: .5;
  background-image:
    linear-gradient(rgba(var(--line-rgb), .06) 1px, transparent 1px),
    linear-gradient(90deg, rgba(var(--line-rgb), .06) 1px, transparent 1px);
  background-size: 32px 32px;
}
.login-intro > * { position: relative; z-index: 1; }
.login-brand { display: flex; align-items: center; gap: 10px; }
.login-mark {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  flex: 0 0 auto;
  color: white;
  background: var(--blue);
  border: 1px solid var(--blue-dark);
  border-radius: var(--r-lg);
}
.login-mark svg { width: 21px; height: 21px; }
.login-mark.small { width: 30px; height: 30px; border-radius: 8px; }
.login-mark.small svg { width: 17px; height: 17px; }
.login-wordmark { color: var(--ink); font-size: 15px; font-weight: 600; letter-spacing: -.01em; }

.login-copy { margin-top: auto; margin-bottom: 34px; }
.login-copy h1 {
  margin: 0;
  color: var(--ink);
  font-size: 30px;
  line-height: 1.32;
  font-weight: 600;
  letter-spacing: -.025em;
}
.login-copy p { max-width: 420px; margin: 14px 0 0; color: var(--muted); font-size: 13px; line-height: 1.6; }

.login-points { display: grid; gap: 14px; margin: 0; padding: 0; list-style: none; }
.login-points li { display: flex; align-items: flex-start; gap: 11px; }
.login-point-icon {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  flex: 0 0 auto;
  color: var(--graphite);
  background: white;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.login-point-icon svg { width: 17px; height: 17px; }
.login-points strong { display: block; color: var(--ink); font-size: 13px; font-weight: 600; }
.login-points span { display: block; max-width: 320px; margin-top: 2px; color: var(--muted); font-size: 11.5px; line-height: 1.5; }
.login-meta { margin: auto 0 0; color: var(--muted); font-size: 11.5px; }

/* 右侧登录表单：白底、无卡片边框 */
.login-form-panel {
  display: grid;
  place-items: center;
  padding: 40px;
  background: var(--bg);
}
.login-mobile-brand { display: none; align-items: center; gap: 10px; margin-bottom: 28px; }
.login-mobile-brand strong { color: var(--ink); font-size: 16px; font-weight: 600; }
.login-card {
  width: min(360px, 100%);
  animation: login-enter .2s ease-out both;
}
.login-header h2 { margin: 0; color: var(--ink); font-size: 22px; font-weight: 600; letter-spacing: -.02em; }
.login-header p { margin: 5px 0 0; color: var(--muted); font-size: 12.5px; }
.login-form { display: flex; flex-direction: column; gap: 14px; margin-top: 26px; }
.login-field { display: flex; flex-direction: column; gap: 6px; }
.login-field span { color: var(--text); font-size: 12.5px; font-weight: 600; }
.login-field input {
  height: 38px;
  padding: 0 11px;
  color: var(--text);
  background: white;
  border: 1px solid rgba(var(--line-rgb), .18);
  border-radius: 6px;
  font-size: 13.5px;
  outline: none;
  transition: border-color .15s, box-shadow .15s;
}
.login-field input::placeholder { color: var(--faint); }
.login-field input:focus { border-color: var(--blue); box-shadow: 0 0 0 3px rgba(var(--blue-rgb), .15); }
.login-error {
  margin: 0;
  padding: 8px 10px;
  color: var(--red-text);
  background: var(--red-tint);
  border: 1px solid rgba(var(--red-rgb), .18);
  border-radius: 6px;
  font-size: 12.5px;
  line-height: 1.45;
}
.login-btn {
  height: 38px;
  margin-top: 4px;
  color: white;
  background: var(--blue);
  border: 1px solid var(--blue);
  border-radius: 6px;
  font-size: 13.5px;
  font-weight: 600;
  cursor: pointer;
  transition: background .15s, filter .15s, transform .12s;
}
.login-btn:hover:not(:disabled) { background: var(--blue-dark); border-color: var(--blue-dark); }
.login-btn:active:not(:disabled) { transform: scale(.99); }
.login-btn:disabled { opacity: .55; cursor: default; }
.login-server { margin: -2px 0 0; color: var(--muted); font-size: 11.5px; text-align: center; word-break: break-all; }
.login-hint { margin: 20px 0 0; color: var(--muted); font-size: 12px; text-align: center; }

@keyframes login-enter {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: none; }
}

@media (max-width: 1180px) {
  .login-intro { padding: 36px 40px 30px; }
  .login-copy h1 { font-size: 26px; }
}
@media (max-width: 980px) {
  .login-shell { grid-template-columns: 1fr; }
  .login-intro { display: none; }
  .login-form-panel { padding: 28px; }
  .login-mobile-brand { display: flex; }
}
</style>
