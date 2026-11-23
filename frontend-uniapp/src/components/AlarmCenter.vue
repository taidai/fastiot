<template>
  <view class="backdrop" @click="$emit('close')"></view>
  <view class="drawer glass" role="dialog" aria-modal="true" aria-label="告警中心">
    <view class="drawer-head"><strong>告警中心 · {{ pending.length }} 条待接手</strong><button class="x" @click="$emit('close')">关闭</button></view>
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
      <view class="actions">
        <button v-if="alarm.active && alarm.state === 'active' && alarm.permissions?.ack !== false" class="ghost" :disabled="!!busyId" @click="$emit('ack', alarm.id)">{{ busyId === alarm.id ? '处理中…' : '确认接手' }}</button>
        <button v-if="!alarm.active && alarm.state === 'resolved' && alarm.permissions?.maintain !== false" class="ghost" :disabled="!!busyId" @click="$emit('closeAlarm', alarm.id)">关闭告警</button>
        <button v-if="alarm.device_id" class="ghost" @click="$emit('locate', alarm.device_id)">定位到对象</button>
        <button v-if="alarm.point_id" class="ghost" @click="$emit('history', alarm.point_id)">看历史</button>
      </view>
    </view>
    <view v-if="!visible.length" class="empty">{{ tab === 'history' ? '没有已结束的告警记录。' : tab === 'active' ? '没有活动告警。' : '没有待接手告警。' }}</view>
    <view class="note">确认接手不等于设备恢复；关闭告警也不代表现场动作已完成。</view>
  </view>
</template>

<script setup lang="ts">
import { computed, ref } from "vue"
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
.note { margin-top: 14px; color: var(--faint); font-size: 11px; line-height: 1.7; }
.error { margin-bottom: 10px; }
</style>
