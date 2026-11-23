<template>
  <aside class="ledger glass" :class="{ collapsed }">
    <button v-if="collapsed" class="ledger-toggle collapsed-toggle" :aria-expanded="false" @click="collapsed = false">
      <text>台账</text><text>{{ rows.length }}</text>
    </button>
    <template v-else>
      <view class="ledger-title"><text>台账</text><text>{{ String(filtered.length).padStart(2, '0') }}</text><button class="collapse" aria-label="收起台账" :aria-expanded="true" @click="collapsed = true">‹</button></view>
      <view class="search"><input v-model="query" placeholder="搜索对象 / 设备" aria-label="搜索台账对象或设备" /></view>
      <button v-for="row in filtered" :key="row.key" class="node" :class="{ active: selected === row.key }"
        @click="$emit('select', row.key)">
        <text class="index">{{ String(row.index).padStart(2, '0') }}</text>
        <text class="kind">{{ row.kindLabel }} ·</text>
        <text class="name">{{ row.name }}</text>
        <text v-if="row.count" class="count">{{ row.count }}</text>
      </button>
      <view v-if="!filtered.length" class="empty">没有匹配的已接入对象。</view>
      <view class="ledger-note">设备 {{ devices.length }} · 告警 {{ activeAlarmCount }}<br>仅显示已绑定设备；点位与规则不作为客户层。</view>
    </template>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import { nameOf, session } from "../stores/session"
import type { EmsAlarm, Node as ApiNode } from "../types/api"
import { boundDevicesForLayer, deviceKey, isBusinessLayer } from "../pages/overview/overview"
import type { LedgerDevice } from "../pages/overview/overview"

const props = withDefaults(defineProps<{ selected: string; devices?: LedgerDevice[]; alarms?: EmsAlarm[] }>(), {
  devices: () => [],
  alarms: () => [],
})
const emit = defineEmits<{
  (event: "select", id: string): void
  (event: "collapse", value: boolean): void
}>()
const query = ref("")
const collapsed = ref(false)
watch(collapsed, value => emit("collapse", value))

type Row = { key: string; name: string; kindLabel: string; depth: number; count: number; index: number }
const activeAlarmCount = computed(() => props.alarms.filter(alarm => alarm.active).length)
const rows = computed<Row[]>(() => {
  if (!session.snapshot) return []
  const result: Row[] = []
  const walk = (node: ApiNode, depth: number, keepEmpty = false) => {
    if (!isBusinessLayer(node)) return
    const kind = node.type === "site" ? "现场" : ["energyUnit", "energy_unit", "unit"].includes(node.type) ? "单元" : ["device", "modbus_device"].includes(node.type) ? "设备" : "区域"
    const count = boundDevicesForLayer(node.id, session.snapshot!.nodes, props.devices).length
    if (!keepEmpty && count === 0) return
    result.push({ key: node.id, name: nameOf(node), kindLabel: kind, depth, count: 0, index: result.length + 1 })
    session.snapshot!.nodes.filter(child => child.parent === node.id).forEach(child => walk(child, depth + 1))
    props.devices.filter(device => device.business_node_id === node.id).forEach(device => {
      const id = deviceKey(device)
      const alarmCount = (props.alarms || []).filter(alarm => alarm.active && alarm.device_id === id).length
      result.push({ key: `device:${id}`, name: device.name, kindLabel: "设备", depth: depth + 1, count: alarmCount, index: result.length + 1 })
    })
  }
  session.snapshot.roots.forEach(id => {
    const root = session.snapshot!.nodes.find(node => node.id === id)
    if (root) walk(root, 0, true)
  })
  return result
})
const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? rows.value.filter(row => `${row.name} ${row.kindLabel}`.toLowerCase().includes(q)) : rows.value
})
</script>

<style scoped>
.ledger { display: flex; flex-direction: column; padding: 16px 0; align-self: stretch; overflow: auto; }
.ledger.collapsed { padding: 8px; align-self: start; }
.ledger-toggle { display: flex; align-items: center; justify-content: space-between; width: 100%; min-height: 36px; padding: 6px 10px; border: 0; background: transparent; color: var(--muted); font-size: 11px; }
.collapsed-toggle { flex-direction: column; gap: 5px; }
.ledger-title { display: flex; align-items: center; padding: 0 16px 8px; color: var(--muted); font-size: 10px; letter-spacing: .14em; }
.ledger-title text:nth-child(2) { margin-left: auto; }
.collapse { min-width: 24px; min-height: 24px; margin-left: 8px; padding: 0; border: 0; background: transparent; color: var(--muted); line-height: 1; }
.search { padding: 0 12px 10px; }
.search input { width: 100%; height: 32px; border: 1px solid rgba(255,255,255,.7); border-radius: 8px; padding: 5px 9px; background: rgba(255,255,255,.38); font-size: 11px; }
.node { display: flex; align-items: center; width: 100%; min-height: 0; gap: 5px; border: 0; border-left: 3px solid transparent; background: transparent; color: var(--ink); padding: 9px 18px; text-align: left; font-size: 12.5px; line-height: 1.4; overflow: visible; border-radius: 0; }
.node.active { border-left-color: var(--accent); color: #172c3b; background: rgba(255,255,255,.65); font-weight: 600; }
.index { flex: none; color: var(--muted); font-size: 10px; font-variant-numeric: tabular-nums; }
.kind { flex: none; color: var(--muted); font-size: 11px; }
.node.active .kind { color: #172c3b; }
.name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.count { margin-left: auto; color: var(--amber); font-size: 11px; }
.ledger-note { margin: 14px 18px 2px; padding-top: 10px; border-top: 1px solid var(--line); color: var(--muted); font-size: 10.5px; line-height: 1.9; }
</style>
