<script setup lang="ts">
// PluginMarket：插件中心（官方自营商城 + 已装管理）。
// 商城数据来自控制面 /easyshare/plugins（匿名）；安装 = 下载 zip（SHA256 校验）→
// 解压登记（Go 侧完成）；已装管理与设置页同一套动作（启停/卸载，内置不可动）。
import { computed, onMounted, ref } from 'vue'
import { core } from '../services/core'
import { usePlugins } from '../composables/usePlugins'
import type { MarketItem } from '../types/core'

const pluginSys = usePlugins()

const tab = ref<'market' | 'installed'>('market')
const market = ref<MarketItem[]>([])

// 进入插件中心即清除启动检查的红点（页面内「更新」按钮继续引导）
pluginSys.clearUpdateNotices()
const loading = ref(false)
const loadError = ref('')
const busyId = ref('') // 正在安装/更新的插件 ID

const load = async () => {
  loading.value = true
  loadError.value = ''
  try {
    market.value = await core.pluginMarketList()
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

const installedById = computed(() => {
  const map = new Map(pluginSys.plugins.value.map(p => [p.id, p]))
  return map
})

const install = async (item: MarketItem) => {
  if (!item.asset || busyId.value) return
  busyId.value = item.id
  loadError.value = ''
  try {
    // 两段式：先预览（下载校验但不落成安装），有需确认的权限时弹确认框
    const preview = await core.pluginPreviewFromMarket(item.asset.id, item.asset.sha256, item.asset.sizeBytes)
    if (preview.newPermissions.length) {
      const permList = preview.newPermissions.map(p => `· ${permLabel(p)}`).join('\n')
      const action = preview.isUpdate
        ? `更新到 v${preview.version}`
        : `安装 v${preview.version}`
      const ok = window.confirm(
        `${action}「${preview.name}」需要以下权限：\n\n${permList}\n\n是否继续？`,
      )
      if (!ok) return
    }
    // 同意过的权限集合传给安装器：包内新增权限超出该集合会被 Go 侧拒绝（防静默扩权）
    await core.pluginInstallFromMarket(item.asset.id, item.asset.sha256, item.asset.sizeBytes, preview.newPermissions)
    await pluginSys.refreshPlugins()
    await load() // 刷新 updateAvailable 标记
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    busyId.value = ''
  }
}

// 权限的中文展示名（与 internal/plugin 的权限常量对应）
const permLabel = (perm: string): string => ({
  storage: '本地数据存储',
  'clipboard.read': '读取剪切板历史',
  'clipboard.write': '写入剪切板',
  'clipboard.events': '剪切板变化通知',
  notification: '系统通知',
  'drive.upload': '上传到个人云盘',
}[perm] ?? perm)

const togglePlugin = async (id: string, disabled: boolean) => {
  await pluginSys.setDisabled(id, disabled)
}
const removePlugin = async (id: string) => {
  if (!window.confirm('确定卸载该插件？其私有数据将被清除。')) return
  await pluginSys.uninstall(id)
}

const isIconPath = (icon: string) => !!icon && /[/.]/.test(icon)
const formatBytes = (n: number) => {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
</script>

<template>
  <div class="plugin-market">
    <header class="workspace-header">
      <div>
        <span class="section-label">扩展</span>
        <h1>插件中心</h1>
        <p>按需安装官方插件；内置插件随程序提供，不可卸载。</p>
      </div>
      <div class="market-tabs">
        <button :class="['chip', tab === 'market' ? 'active' : '']" type="button" @click="tab = 'market'">
          商城（{{ market.length }}）
        </button>
        <button :class="['chip', tab === 'installed' ? 'active' : '']" type="button" @click="tab = 'installed'">
          已安装（{{ pluginSys.plugins.value.length }}）
        </button>
      </div>
    </header>

    <p v-if="loadError" class="update-error market-error">{{ loadError }}</p>

    <!-- 商城 -->
    <div v-if="tab === 'market'" class="market-grid">
      <p v-if="loading" class="market-hint">正在加载商城…</p>
      <p v-else-if="!market.length && !loadError" class="market-hint">
        商城暂无插件。官方插件将通过控制面上架，用 scripts/publish-plugin.ps1 发布。
      </p>
      <div v-for="item in market" :key="item.id" class="card market-card">
        <div class="market-card-head">
          <span class="plugin-row-icon" aria-hidden="true">
            <img v-if="isIconPath(item.icon)" :src="`/plugins/${item.id}/${item.icon}`" alt="">
            <template v-else>{{ item.icon || '🧩' }}</template>
          </span>
          <div class="market-card-title">
            <strong>{{ item.name }}</strong>
            <small>v{{ item.version }}<template v-if="item.author"> · {{ item.author }}</template></small>
          </div>
          <span v-if="installedById.has(item.id) && !item.updateAvailable" class="plugin-tag gray">已安装</span>
        </div>
        <p class="market-card-desc">{{ item.description }}</p>
        <p v-if="item.notes" class="market-card-notes">更新说明：{{ item.notes }}</p>
        <div class="market-card-foot">
          <small class="market-card-meta">
            {{ item.publishedAt }}<template v-if="item.asset"> · {{ formatBytes(item.asset.sizeBytes) }}</template>
          </small>
          <button
            v-if="item.asset"
            class="primary-button compact"
            type="button"
            :disabled="busyId === item.id || (!!installedById.get(item.id)?.builtin && item.updateAvailable)"
            @click="install(item)"
          >
            {{ busyId === item.id ? '安装中…' : item.updateAvailable ? '更新' : installedById.has(item.id) ? '重新安装' : '安装' }}
          </button>
        </div>
      </div>
    </div>

    <!-- 已安装 -->
    <div v-else class="card market-installed">
      <p v-if="!pluginSys.plugins.value.length" class="market-hint">还没有安装任何插件。</p>
      <div v-for="p in pluginSys.plugins.value" :key="p.id" class="plugin-row">
        <span class="plugin-row-icon" aria-hidden="true">
          <img v-if="isIconPath(p.icon)" :src="`/plugins/${p.id}/${p.icon}`" alt="">
          <template v-else>{{ p.icon || '🧩' }}</template>
        </span>
        <div class="plugin-row-body">
          <strong>{{ p.name }} <span class="plugin-tag">v{{ p.version }}</span> <span v-if="p.builtin" class="plugin-tag">内置</span></strong>
          <small>{{ p.description }}</small>
        </div>
        <div class="plugin-row-actions">
          <button
            :class="['plugin-switch', !p.disabled ? 'on' : '', p.builtin ? 'disabled' : '']"
            type="button"
            :aria-label="p.disabled ? '启用插件' : '禁用插件'"
            :disabled="p.builtin"
            @click="togglePlugin(p.id, !p.disabled)"
          />
          <button v-if="!p.builtin" class="secondary-button danger compact" type="button" @click="removePlugin(p.id)">
            卸载
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.plugin-market { display: flex; flex-direction: column; gap: 16px; }
.market-tabs { display: flex; gap: 8px; }
.market-tabs .chip {
  border: 1px solid var(--border); background: white; border-radius: 999px;
  padding: 6px 16px; font-size: 12px; color: var(--muted); cursor: pointer;
}
.market-tabs .chip.active { background: var(--blue); border-color: var(--blue); color: #fff; }
.market-error { margin: 0; }
.market-hint { color: var(--muted); font-size: 13px; }
.market-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 14px; align-items: start; }
.market-card { display: flex; flex-direction: column; gap: 10px; padding: 16px; }
.market-card-head { display: flex; align-items: center; gap: 10px; }
.market-card-title { flex: 1; min-width: 0; }
.market-card-title strong { display: block; font-size: 14px; }
.market-card-title small { color: var(--muted); font-size: 11px; }
.market-card-desc { margin: 0; color: var(--text); font-size: 12px; line-height: 1.6; }
.market-card-notes { margin: 0; color: var(--muted); font-size: 11px; }
.market-card-foot { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-top: auto; }
.market-card-meta { color: var(--muted); font-size: 11px; }
.market-installed { padding: 6px 16px; }
</style>
