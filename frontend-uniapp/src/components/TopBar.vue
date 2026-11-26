<template>
  <header class="topbar glass" :class="{ 'no-workbench': !showWorkbench, 'no-actions': !showAppearance && !showBell }">
    <view class="brand">FastIOT<text class="brand-sub">{{ activeLabel }}</text></view>
    <view class="project">项目：<b>{{ currentProject?.name || '未选择项目' }}</b></view>
    <view v-if="showWorkbench" class="workbench" aria-label="工作台">
      <button v-for="item in tabs" :key="item.key" role="button" tabindex="0" :class="{ active: active === item.key }" :aria-current="active === item.key ? 'page' : undefined" @keydown.enter.prevent="$emit('navigate', item.key)" @keydown.space.prevent="$emit('navigate', item.key)" @click="$emit('navigate', item.key)">{{ item.label }}</button>
    </view>
    <button v-if="showEms" class="ems-toggle" role="switch" :aria-checked="displayEmsInstalled === true" :disabled="emsBusy || displayEmsInstalled === null || !canChangeIndustry" title="启用不等于购买、部署或控制授权" @click="emit('toggle-ems')"><text class="ems-check">{{ displayEmsInstalled ? '✓' : '' }}</text><text>EMS 行业包</text></button>
    <view v-if="showAppearance" class="top-meta desktop-only">{{ meta }}</view>
    <button v-if="showAppearance" class="appearance" :aria-expanded="open" @click="open = !open">外观</button>
    <view class="notification-actions">
      <NotificationInbox v-if="session.token" />
      <button v-if="showBell" class="bell" @click="$emit('bell')">告警中心 <text v-if="pending" class="badge">{{ pending }}</text></button>
    </view>
    <view v-if="showAppearance && open" class="appearance-panel glass" role="dialog" aria-label="外观设置">
      <view v-if="themes" class="theme-choices" aria-label="主题">
        <button v-for="item in themeOptions" :key="item.key" class="theme-choice" :class="{ active: theme === item.key }"
          :data-theme="item.key" :aria-pressed="theme === item.key" @click="emit('theme', item.key)">{{ item.label }}</button>
      </view>
      <view class="card-title">透明度 <text class="num">{{ alpha }}%</text></view>
      <slider class="alpha-slider" :value="alpha" min="20" max="85" step="1" aria-label="玻璃透明度" @changing="onAlpha" @change="onAlpha" />
      <button v-if="themes" class="link appearance-reset" @click="emit('reset')">恢复默认</button>
    </view>
  </header>
</template>

<script setup lang="ts">
import NotificationInbox from './NotificationInbox.vue'
import { computed, ref } from "vue"
import { session } from '../stores/session'
import { canAction } from './delivery/contracts'
import { currentProject } from '../stores/projects'
const props = withDefaults(defineProps<{
  active: string
  meta: string
  pending: number
  alpha: number
  theme?: "light" | "dark" | "tint"
  themes?: boolean
  showWorkbench?: boolean
  showAppearance?: boolean
  showBell?: boolean
  showEms?: boolean
  emsInstalled?: boolean | null
  emsBusy?: boolean
}>(), { theme: "light", themes: false, showWorkbench: true, showAppearance: false, showBell: false, showEms: false, emsInstalled: null, emsBusy: false })
const emit = defineEmits<{
  (event: "navigate", key: string): void
  (event: "bell"): void
  (event: "alpha", value: number): void
  (event: "theme", value: "light" | "dark" | "tint"): void
  (event: "reset"): void
  (event: "toggle-ems"): void
}>()
const displayEmsInstalled = computed(() => session.snapshot?.industry?.installed ?? props.emsInstalled)
const canChangeIndustry = computed(() => (canAction(session.snapshot, 'site_settings') && !!currentProject.value && !!session.snapshot?.roots.includes(currentProject.value.root_id)))
const open = ref(false)
function onAlpha(event: Event) {
  const detail = (event as CustomEvent<{ value?: number }>).detail
  const value = Number(detail?.value ?? (event.target as HTMLInputElement).value)
  if (Number.isFinite(value)) emit("alpha", value)
}
const tabs = [
  { key: "overview", label: "总览" },
  { key: "design", label: "设计" },
  { key: "delivery", label: "交付" },
]
const themeOptions = [
  { key: "light" as const, label: "日间" },
  { key: "dark" as const, label: "夜间" },
  { key: "tint" as const, label: "青玻璃" },
]
const activeLabel = computed(() => tabs.find(tab => tab.key === props.active)?.label || "工作台")
</script>

<style scoped>
.notification-actions { display: flex; align-items: center; gap: 8px; }
.topbar { position: relative; z-index: 30; display: grid; grid-template-columns: 180px minmax(110px,1fr) auto minmax(180px,1.2fr) auto auto; align-items: center; gap: 20px; padding: 12px 18px; }
.topbar.no-workbench { grid-template-columns: 180px 1fr auto auto auto; }
.topbar.no-actions { display: flex; gap: 14px; padding: 9px 18px; }
.no-actions .workbench { margin-left: auto; }
.brand { font-size: 16px; font-weight: 700; letter-spacing: .02em; }
.brand-sub { margin-left: 8px; color: var(--muted); font-size: 11px; font-weight: 400; letter-spacing: .14em; }
.project { position: relative; color: var(--muted); font-size: 12.5px; white-space: nowrap; overflow: visible; }
.ems-toggle { display: flex; align-items: center; gap: 6px; min-height: 0; padding: 5px 10px; border: 1px dashed #b9c8d4; border-radius: 999px; background: rgba(255,255,255,.5); color: var(--muted); font-size: 12px; line-height: normal; }
.ems-check { display: grid; place-items: center; width: 12px; height: 12px; border: 1px solid #8fa9b8; border-radius: 2px; background: #fff; color: #0e7c7b; font-size: 10px; line-height: 1; }
.top-meta { color: var(--muted); font-size: 11px; text-align: right; white-space: nowrap; overflow: visible; }
.workbench { display: flex; gap: 3px; padding: 3px; border: 1px solid var(--glass-border); border-radius: 11px; background: var(--control-bg, rgba(255,255,255,.24)); }
.workbench button { min-height: 24px; border: 0; border-radius: 8px; background: transparent; color: var(--muted); padding: 3px 14px; font-size: 12px; line-height: 1.2; }
.workbench button.active { background: var(--control-active, rgba(255,255,255,.85)); color: var(--accent); font-weight: 600; box-shadow: 0 2px 8px rgba(31,46,58,.1); }
.appearance, .bell { min-height: 0; border: 1px solid var(--glass-border); background: var(--control-bg, rgba(255,255,255,.55)); padding: 6px 12px; font-size: 12px; line-height: normal; color: var(--ink); overflow: visible; backdrop-filter: blur(10px); }
.appearance { display: inline-block !important; border-radius: 10px; color: var(--ink); font-size: 11px; }
.bell { display: flex; align-items: center; gap: 8px; padding: 7px 12px; border-radius: 12px; background: var(--control-bg, rgba(255,255,255,.6)); white-space: nowrap; }
.bell .badge { height: auto; min-width: 0; padding: 0; border-radius: 0; background: transparent; color: var(--amber); font-size: 14px; line-height: normal; }
.appearance-panel { position: absolute; right: 88px; top: calc(100% + 8px); width: 238px; padding: 12px; background: var(--glass-strong); }
.appearance-panel input, .alpha-slider { width: 100%; accent-color: var(--accent); }
.theme-choices { display: flex; gap: 4px; margin-bottom: 12px; }
.theme-choice { flex: 1; min-height: 30px; padding: 4px 8px; border: 1px solid var(--glass-border); border-radius: 8px; background: var(--control-bg, rgba(255,255,255,.2)); color: var(--muted); font-size: 11px; }
.theme-choice.active { border-color: var(--accent); background: var(--control-active, rgba(255,255,255,.5)); color: var(--ink); }
.appearance-reset { min-height: 28px; margin-top: 8px; font-size: 10.5px; }
@media (max-width: 1150px) {
  .topbar.no-workbench { display: grid; grid-template-columns: 1fr auto; gap: 20px; padding: 12px 18px; }
  .no-workbench .project, .no-workbench .top-meta, .no-workbench .appearance { display: none !important; }
}
@media (max-width: 1100px) {
  .topbar { grid-template-columns: 140px minmax(100px,1fr) auto auto auto; gap: 10px; }
  .top-meta { display: none; }
  .workbench button { padding: 4px 11px; }
}
@media (max-width: 760px) {
  .topbar { display: flex; flex-wrap: wrap; gap: 8px; padding: 8px 10px; }
  .brand { margin-right: auto; }
  .project { order: 3; width: 100%; }
  .workbench { order: 4; }
}
</style>
