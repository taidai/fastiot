<template>
  <view class="page" :class="appearance.theme === 'light' ? '' : `theme-${appearance.theme}`">
    <TopBar active="overview" meta="历史数据" :pending="unackedAlarms.length" :alpha="appearance.alpha"
      :theme="appearance.theme" themes :show-workbench="false" show-appearance show-bell @navigate="navigate" @bell="alarmOpen = true" @alpha="setAlpha" @theme="setTheme" @reset="resetAppearance" />
    <view class="history glass">
      <HistoryPanel :point-options="pointOptions" :initial-point="point" :refresh-token="refreshToken" @back="back" @update:point="point = $event" />
    </view>
    <AlarmCenter v-if="alarmOpen" :alarms="session.alarms" :busy-id="alarmBusy" :error="alarmError" @close="alarmOpen = false"
      @ack="runAlarm('ack', $event)" @close-alarm="runAlarm('close', $event)" @history="selectHistoryPoint" @locate="goDevice" />
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue"
import "../overview/theme.css"
import { onHide, onLoad, onShow } from "@dcloudio/uni-app"
import TopBar from "../../components/TopBar.vue"
import AlarmCenter from "../../components/AlarmCenter.vue"
import HistoryPanel from "./HistoryPanel.vue"
import type { HistoryPointOption } from "./history"
import { acknowledgeAlarm, closeAlarm, requireAuth, session, startPolling, stopPolling } from "../../stores/session"
import { isDerivedInput } from '../../components/design/strategy'
import { businessName, unitOf } from "../overview/overview"
import { DEFAULT_APPEARANCE, applyAppearanceDocument, normalizeAppearance } from "../overview/appearance"
import type { Appearance, AppearanceTheme } from "../overview/appearance"

const point = ref("")
const refreshToken = ref(0)
const alarmOpen = ref(false)
const alarmBusy = ref("")
const alarmError = ref("")
function storedAppearance(): Appearance {
  const stored = uni.getStorageSync("fastiot-overview-appearance")
  if (stored) {
    try { return normalizeAppearance(typeof stored === "string" ? JSON.parse(stored) : stored) }
    catch { return { ...DEFAULT_APPEARANCE } }
  }
  return normalizeAppearance({ alpha: Number(uni.getStorageSync("fastiot-overview-alpha") || 42) })
}
const appearance = ref<Appearance>(storedAppearance())
function persistAppearance() { uni.setStorageSync("fastiot-overview-appearance", JSON.stringify(appearance.value)) }
function setAlpha(value: number) { appearance.value = normalizeAppearance({ ...appearance.value, alpha: value }); persistAppearance(); applyAppearanceDocument(appearance.value) }
function setTheme(value: AppearanceTheme) { appearance.value = normalizeAppearance({ ...appearance.value, theme: value }); persistAppearance(); applyAppearanceDocument(appearance.value) }
function resetAppearance() { appearance.value = { ...DEFAULT_APPEARANCE }; persistAppearance(); applyAppearanceDocument(appearance.value) }

const unackedAlarms = computed(() => session.alarms.filter(alarm => alarm.active && alarm.state === "active"))
const pointOptions = computed<HistoryPointOption[]>(() => (session.snapshot?.nodes || []).filter(node => ["variable", "modbusIo", "opcuaPoint", "mqttSub"].includes(node.type) || isDerivedInput(node)).map(node => ({
  id: node.id,
  label: businessName(node),
  unit: unitOf(node),
})))
function back() { uni.navigateBack() }
function navigate(key: string) { if (key !== "overview") uni.navigateTo({ url: `/pages/${key}/index` }); else uni.redirectTo({ url: "/pages/overview/index" }) }
async function runAlarm(action: "ack" | "close", id: string) {
  alarmBusy.value = id
  alarmError.value = ""
  try { await (action === "ack" ? acknowledgeAlarm(id) : closeAlarm(id)) }
  catch (error) { alarmError.value = error instanceof Error ? error.message : String(error) }
  finally { alarmBusy.value = "" }
}
function selectHistoryPoint(id: string) { point.value = id; alarmOpen.value = false }
function goDevice(id: string) { alarmOpen.value = false; uni.redirectTo({ url: `/pages/overview/index?device=${encodeURIComponent(id)}` }) }

onLoad(options => { point.value = (options?.point as string) || "" })
onMounted(() => {
  if (!requireAuth()) return
  applyAppearanceDocument(appearance.value)
})
onShow(() => {
  if (requireAuth()) { startPolling(); refreshToken.value += 1 }
})
onHide(() => { stopPolling() })
</script>

<style scoped>
.page { min-height: 100vh; padding: 16px; overflow-x: hidden; }
.history { margin-top: 14px; padding: 18px 20px; min-width: 0; }
@media (max-width: 760px) { .page { padding: 8px; } .history { padding: 14px; } }
</style>
