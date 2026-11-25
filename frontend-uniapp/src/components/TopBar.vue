<template>
  <header class="topbar glass" :class="{ 'no-workbench': !showWorkbench, 'no-actions': !showAppearance && !showBell }">
    <view class="brand">FastIOT<text class="brand-sub">{{ activeLabel }}</text></view>
    <view v-if="PREVIEW_MODE && active !== 'overview'" class="project" @keydown.esc="projectOpen = false">
      <button class="project-button" :aria-expanded="projectOpen" aria-haspopup="menu" @click="projectOpen = !projectOpen">项目：<b>{{ previewProjectName }}</b> ▾</button>
      <view v-if="projectOpen" class="project-menu" role="menu">
        <button v-for="item in previewProjects" :key="item.id" role="menuitem" @click="chooseProject(item.id)">{{ item.name }}</button>
        <hr><button role="menuitem" @click="newProject">＋ 新建项目</button><button role="menuitem" @click="projectOpen = false; emit('import-project')">导入项目包…</button>
      </view>
    </view>
    <view v-else class="project">项目<text v-if="!showWorkbench"> </text><text v-else>：</text><text class="project-name">{{ project }}</text><text v-if="showWorkbench" class="project-caret">▾</text></view>
    <view v-if="showWorkbench" class="workbench" aria-label="工作台">
      <button v-for="item in tabs" :key="item.key" :class="{ active: active === item.key }" :aria-current="active === item.key ? 'page' : undefined" @click="$emit('navigate', item.key)">{{ item.label }}</button>
    </view>
    <button v-if="showEms" class="ems-toggle" role="switch" :aria-checked="emsInstalled === true" :disabled="emsBusy || emsInstalled === null" @click="emit('toggle-ems')"><text class="ems-check">{{ emsInstalled ? '✓' : '' }}</text><text>EMS 行业包</text></button>
    <view v-if="showAppearance" class="top-meta desktop-only">{{ meta }}</view>
    <button v-if="showAppearance" class="appearance" :aria-expanded="open" @click="open = !open">外观</button>
    <button v-if="showBell" class="bell" @click="$emit('bell')">告警中心 <text v-if="pending" class="badge">{{ pending }}</text></button>
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
import { computed, ref } from "vue"
import { PREVIEW_MODE } from '../api/source'
import { previewWorkspace, previewProjects, selectPreviewProject, createPreviewProject } from '../api/preview/workspace'
import { selectDeliveryPreviewProject } from '../api/preview/delivery'
const props = withDefaults(defineProps<{
  active: string
  project: string
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
  (event: "project-change", id: string): void
  (event: "new-project"): void
  (event: "import-project"): void
}>()
const open = ref(false)
const projectOpen = ref(false)
const previewProjectName = computed(() => previewProjects.find(item => item.id === previewWorkspace.projectId)?.name || props.project)
function chooseProject(id: string) { selectDeliveryPreviewProject(id); selectPreviewProject(id); projectOpen.value = false; emit('project-change', id) }
function newProject() { const project = createPreviewProject(); selectDeliveryPreviewProject(project.id); projectOpen.value = false; emit('new-project') }
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
const activeLabel = computed(() => PREVIEW_MODE ? "原型预览" : tabs.find(tab => tab.key === props.active)?.label || "工作台")
</script>

<style scoped>
.topbar { position: relative; z-index: 30; display: grid; grid-template-columns: 180px minmax(110px,1fr) auto minmax(180px,1.2fr) auto auto; align-items: center; gap: 20px; padding: 12px 18px; }
.topbar.no-workbench { grid-template-columns: 180px 1fr auto auto auto; }
.topbar.no-actions { display: flex; gap: 14px; padding: 9px 18px; }
.no-actions .workbench { margin-left: auto; }
.brand { font-size: 16px; font-weight: 700; letter-spacing: .02em; }
.brand-sub { margin-left: 8px; color: var(--muted); font-size: 11px; font-weight: 400; letter-spacing: .14em; }
.project { position: relative; color: var(--muted); font-size: 12.5px; white-space: nowrap; overflow: visible; }
.project-button { min-height: 32px; margin: 0; padding: 6px 11px; border: 1px solid var(--glass-border); border-radius: 10px; background: rgba(255,255,255,.78); font-size: 12.5px; line-height: normal; }
.project-menu { position: absolute; top: calc(100% + 6px); left: 0; z-index: 40; min-width: 210px; padding: 6px; background: var(--glass-strong, #fff); border: 1px solid var(--line); border-radius: 12px; box-shadow: 0 18px 40px rgba(31,46,58,.16); }
.project-menu button { display: block; width: 100%; margin: 0; min-height: 34px; padding: 8px 10px; border: 0; border-radius: 8px; background: none; font-size: 13px; line-height: normal; text-align: left; }
.project-menu button:hover { background: #f1f7fa; }
.project-menu hr { border: 0; border-top: 1px solid var(--line); margin: 6px 4px; }
.project-name { color: var(--ink); font-weight: 600; }
.project-caret { margin-left: 6px; color: var(--muted); font-size: 11px; }
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
