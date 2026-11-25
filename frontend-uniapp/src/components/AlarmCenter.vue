<template>
  <view class="backdrop" @click="$emit('close')"></view>
  <view class="drawer glass" role="dialog" aria-modal="true" aria-label="告警中心">
    <view class="drawer-head"><strong>告警中心 · 全授权范围 · {{ pending.length }} 条待接手</strong><button class="x" @click="$emit('close')">关闭</button></view>
    <view class="chips alarm-tabs">
      <button :class="{ active: tab === 'pending' }" @click="tab = 'pending'">待接手 {{ pending.length }}</button>
      <button :class="{ active: tab === 'active' }" @click="tab = 'active'">活动 {{ active.length }}</button>
      <button :class="{ active: tab === 'history' }" @click="tab = 'history'">历史 {{ history.length }}</button>
    </view>
    <view v-if="error" class="error" role="alert">{{ error }}</view>
    <view v-for="alarm in visible" :key="alarm.id" class="alarm">
      <view class="title">{{ alarm.title }}</view>
      <view class="meta">{{ severityText(alarm.severity) }} · {{ stateText(alarm) }} · {{ timeText(alarm.updated_at || alarm.created_at) }}</view>
      <view v-if="alarm.detail" class="detail">{{ alarm.detail }}</view>
      <view class="meta">{{ alarm.count ? `异常观测 ${alarm.count} 次` : '异常观测次数未记录' }} · 首次 {{ timeText(alarm.created_at) }}</view>
      <view v-if="alarm.acked_by" class="meta">接手：{{ alarm.acked_by }} · {{ timeText(alarm.acked_at) }}<text v-if="alarm.ack_note"> · {{ alarm.ack_note }}</text></view>
      <view v-if="alarm.input_state === 'unknown'" class="detail">观测状态未知，尚未确认恢复。</view>
      <view v-if="alarm.target?.mapping_changed" class="detail">点位绑定已改变，当前不能跳转到原设备的数据。</view>
      <view class="actions">
        <button v-if="alarm.active && alarm.state === 'active' && alarm.permissions?.ack !== false" class="ghost" :disabled="!!busyId" @click="$emit('ack', alarm.id)">{{ busyId === alarm.id ? '处理中…' : '确认接手' }}</button>
        <button v-if="!alarm.active && alarm.state === 'resolved' && alarm.permissions?.maintain !== false" class="ghost" :disabled="!!busyId" @click="$emit('closeAlarm', alarm.id)">关闭告警</button>
        <button v-if="alarm.device_id && !alarm.target?.mapping_changed" class="ghost" @click="$emit('locate', alarm.target?.device_id || alarm.device_id)">定位到对象</button>
        <button v-if="alarm.point_id && !alarm.target?.mapping_changed" class="ghost" @click="$emit('history', alarm.point_id)">看历史</button>
        <button class="ghost" :aria-expanded="eventId === alarm.id" @click="eventId = eventId === alarm.id ? '' : alarm.id">处置记录</button>
      </view>
      <view v-if="eventId === alarm.id" class="occurrence-events">
        <view class="meta">发生记录 {{ alarm.id }} · 条件 {{ alarm.key }}</view>
        <view v-if="eventsLoading" class="meta">正在读取处置记录…</view>
        <view v-else-if="eventsError" class="error" role="alert">{{ eventsError }}</view>
        <view v-for="event in events" :key="event.revision" class="meta">{{ transitionText(event.transition) }} · {{ timeText(event.time) }}<text v-if="event.acked_by && event.transition === 'acknowledged'"> · {{ event.acked_by }} {{ event.ack_note }}</text></view>
      </view>
    </view>
    <view v-if="!visible.length && !error" class="empty">{{ tab === 'history' ? '没有已结束的告警记录。' : tab === 'active' ? '没有活动告警。' : '没有待接手告警。' }}</view>
    <view class="note">确认接手不等于设备恢复；关闭告警也不代表现场动作已完成。</view>
  </view>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue"
import { request } from "../api/client"
import { projectContext } from "../stores/projects"
import { session } from "../stores/session"
import type { EmsAlarm } from "../types/api"
const props = defineProps<{ alarms: EmsAlarm[]; busyId?: string; error?: string }>()
defineEmits<{
  (event: "close"): void
  (event: "ack", id: string): void
  (event: "closeAlarm", id: string): void
  (event: "history", pointId: string): void
  (event: "locate", deviceId: string): void
}>()
const tab = ref<"pending" | "active" | "history">("pending")
type AlarmEvent = { revision: number; transition: string; time: string; acked_by?: string; ack_note?: string }
const eventId = ref("")
const events = ref<AlarmEvent[]>([])
const eventsLoading = ref(false)
const eventsError = ref("")
let eventSerial = 0
async function loadEvents() {
  const serial = ++eventSerial
  const id = eventId.value
  events.value = []; eventsError.value = ""; eventsLoading.value = false
  if (!id) return
  eventsLoading.value = true
  try {
    const result = await request<{ events: AlarmEvent[] }>(`/alarm-events?occurrence_id=${encodeURIComponent(id)}`)
    if (serial === eventSerial) events.value = result.events
  } catch (error) {
    if (serial === eventSerial) eventsError.value = `处置记录读取失败：${error instanceof Error ? error.message : String(error)}`
  } finally { if (serial === eventSerial) eventsLoading.value = false }
}
watch(() => [eventId.value, props.alarms.find(alarm => alarm.id === eventId.value)?.revision], () => { void loadEvents() })
watch(() => [projectContext.generation, session.token], () => { eventSerial++; eventId.value = ''; events.value = []; eventsError.value = '' }, { flush: 'sync' })
onBeforeUnmount(() => { eventSerial++ })
function transitionText(value: string) {
  return ({ raised: '触发', changed: '观测更新', acknowledged: '已接手', resolved: '条件恢复', closed: '已关闭', input_unknown: '观测未知', retired: '配置停用', maintenance: '维护抑制', released: '结束维护' } as Record<string, string>)[value] || value
}
const pending = computed(() => props.alarms.filter(alarm => alarm.active && alarm.state === "active"))
const active = computed(() => props.alarms.filter(alarm => alarm.active))
const history = computed(() => props.alarms.filter(alarm => !alarm.active || alarm.state === "resolved" || alarm.state === "closed"))
const visible = computed(() => tab.value === "pending" ? pending.value : tab.value === "active" ? active.value : history.value)
function severityText(value: string) { return ({ info: "提示", warning: "预警", critical: "严重" } as Record<string, string>)[value] || value }
function stateText(alarm: EmsAlarm) {
  if (alarm.state === "acknowledged") return "已接手，仍活动"
  return ({ active: "待接手", resolved: "已恢复", closed: "已关闭" } as Record<string, string>)[alarm.state] || alarm.state
}
function timeText(value?: string) {
  if (!value) return "—"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false })
}
</script>

<style scoped>
.backdrop { position: fixed; inset: 0; z-index: 39; background: rgba(23,44,59,.16); backdrop-filter: blur(2px); }
.drawer { position: fixed; top: 16px; right: 16px; bottom: 16px; z-index: 40; width: 400px; max-width: 92vw; padding: 18px; overflow: auto; background: var(--glass-strong); }
.drawer-head { display: flex; justify-content: space-between; align-items: center; font-size: 13px; margin-bottom: 12px; }
.x { border: 1px solid var(--glass-border); background: rgba(255,255,255,.5); border-radius: 8px; padding: 4px 8px; font-size: 11px; color: var(--muted); }
.alarm-tabs { margin-bottom: 10px; }
.alarm { border-top: 1px solid var(--line); padding: 10px 0; font-size: 12.5px; }
.title { font-weight: 600; }
.meta { margin-top: 3px; color: var(--faint); font-size: 10.5px; }
.detail { margin-top: 5px; color: var(--muted); line-height: 1.5; }
.actions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
.occurrence-events { margin-top: 8px; padding-left: 8px; border-left: 2px solid var(--line); overflow-wrap: anywhere; }
.note { margin-top: 14px; color: var(--faint); font-size: 11px; line-height: 1.7; }
.error { margin-bottom: 10px; }
</style>
