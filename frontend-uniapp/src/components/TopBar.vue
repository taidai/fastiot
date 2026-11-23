<template>
  <header class="topbar glass">
    <view class="brand">FastIOT<text class="brand-sub">{{ activeLabel }}</text></view>
    <view class="project">项目 <text class="project-name">{{ project }}</text></view>
    <view class="workbench" aria-label="工作台">
      <button v-for="item in tabs" :key="item.key" :class="{ active: active === item.key }" :aria-current="active === item.key ? 'page' : undefined" @click="$emit('navigate', item.key)">{{ item.label }}</button>
    </view>
    <view class="top-meta desktop-only">{{ meta }}</view>
    <button class="appearance" :aria-expanded="open" @click="open = !open">外观</button>
    <button class="bell" @click="$emit('bell')">告警中心 <text v-if="pending" class="badge">{{ pending }}</text></button>
    <view v-if="open" class="appearance-panel glass">
      <view class="card-title">透明度 <text class="num">{{ alpha }}%</text></view>
      <input type="range" min="20" max="85" :value="alpha" @input="onAlpha" />
    </view>
  </header>
</template>

<script setup lang="ts">
import { computed, ref } from "vue"
const props = defineProps<{
  active: string
  project: string
  meta: string
  pending: number
  alpha: number
}>()
const emit = defineEmits<{
  (event: "navigate", key: string): void
  (event: "bell"): void
  (event: "alpha", value: number): void
}>()
const open = ref(false)
function onAlpha(event: Event) {
  const value = Number((event.target as HTMLInputElement).value)
  emit("alpha", value)
}
const tabs = [
  { key: "overview", label: "总览" },
  { key: "design", label: "设计" },
  { key: "delivery", label: "交付" },
]
const activeLabel = computed(() => tabs.find(item => item.key === props.active)?.label || "总览")
</script>

<style scoped>
.topbar { position: relative; z-index: 30; display: grid; grid-template-columns: 180px minmax(110px,1fr) auto minmax(180px,1.2fr) auto auto; align-items: center; gap: 20px; padding: 12px 18px; }
.brand { font-size: 16px; font-weight: 700; letter-spacing: .02em; }
.brand-sub { margin-left: 8px; color: var(--muted); font-size: 11px; font-weight: 400; letter-spacing: .14em; }
.project { color: var(--muted); font-size: 12.5px; white-space: nowrap; overflow: visible; }
.project-name { color: var(--ink); font-weight: 600; }
.top-meta { color: var(--muted); font-size: 11px; text-align: right; white-space: nowrap; overflow: visible; }
.workbench { display: flex; gap: 3px; padding: 3px; border: 1px solid rgba(255,255,255,.65); border-radius: 11px; background: rgba(255,255,255,.24); }
.workbench button { min-height: 24px; border: 0; border-radius: 8px; background: transparent; color: var(--muted); padding: 3px 14px; font-size: 12px; line-height: 1.2; }
.workbench button.active { background: rgba(255,255,255,.85); color: var(--accent); font-weight: 600; box-shadow: 0 2px 8px rgba(31,46,58,.1); }
.appearance, .bell { min-height: 0; border: 1px solid var(--glass-border); background: rgba(255,255,255,.55); padding: 6px 12px; font-size: 12px; line-height: normal; color: var(--ink); overflow: visible; backdrop-filter: blur(10px); }
.appearance { display: inline-block !important; border-radius: 10px; color: #172c3b; font-size: 11px; }
.bell { display: flex; align-items: center; gap: 8px; padding: 7px 12px; border-radius: 12px; background: rgba(255,255,255,.6); white-space: nowrap; }
.bell .badge { height: auto; min-width: 0; padding: 0; border-radius: 0; background: transparent; color: var(--amber); font-size: 14px; line-height: normal; }
.appearance-panel { position: absolute; right: 88px; top: calc(100% + 8px); width: 220px; padding: 12px; background: var(--glass-strong); }
.appearance-panel input { width: 100%; accent-color: var(--accent); }
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
