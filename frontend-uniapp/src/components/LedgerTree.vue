<template>
  <aside class="ledger glass">
    <view class="ledger-title"><text>台账</text><text>{{ String(rows.length).padStart(2, '0') }}</text></view>
    <view v-for="row in rows" :key="row.key" class="node" :class="{ active: selected === row.key }" role="button" tabindex="0"
      @click="$emit('select', row.key)" @keydown.enter="$emit('select', row.key)" @keydown.space.prevent="$emit('select', row.key)">
      <text class="index">{{ String(row.index).padStart(2, '0') }}</text>{{ row.kindLabel }} · {{ row.name }}
      <text v-if="row.count" class="count">{{ row.count }}</text>
    </view>
    <view v-if="!rows.length" class="empty">没有已接入对象。</view>
    <view class="ledger-note">设备 {{ devices.length }} · 告警 {{ activeAlarmCount }}<br>仅显示已绑定设备；点位与规则不作为客户层。</view>
  </aside>
</template>

<script setup lang="ts">
import { computed } from "vue"
import { nameOf, session } from "../stores/session"
import type { EmsAlarm, Node as ApiNode } from "../types/api"
import { boundDevicesForLayer, deviceKey, isBusinessLayer } from "../pages/overview/overview"
import type { LedgerDevice } from "../pages/overview/overview"
import { memberRolesOf } from "./design/members"

const props = withDefaults(defineProps<{ selected: string; devices?: LedgerDevice[]; alarms?: EmsAlarm[] }>(), {
  devices: () => [],
  alarms: () => [],
})
defineEmits<{
  (event: "select", id: string): void
}>()

type Row = { key: string; name: string; kindLabel: string; depth: number; count: number; index: number }
const activeAlarmCount = computed(() => props.alarms.filter(alarm => alarm.active).length)
const rows = computed<Row[]>(() => {
  if (!session.snapshot) return []
  const result: Row[] = []
  const walk = (node: ApiNode, depth: number, keepEmpty = false) => {
    if (!isBusinessLayer(node)) return
    const kind = node.type === "site" ? "现场" : ["energyUnit", "energy_unit", "unit"].includes(node.type) ? "单元" : ["device", "modbus_device"].includes(node.type) ? "设备" : "区域"
    const count = boundDevicesForLayer(node.id, session.snapshot!.nodes, props.devices, memberRolesOf(node).flatMap(member => member.node_id ? [member.node_id] : [])).length
    if (!keepEmpty && count === 0) return
    result.push({ key: node.id, name: nameOf(node), kindLabel: keepEmpty ? '项目' : kind, depth, count, index: result.length + 1 })
    session.snapshot!.nodes.filter(child => child.parent === node.id).forEach(child => walk(child, depth + 1))
    // A business Device is already the canonical selectable row, not a second
    // device below itself. Container bindings still need a runtime-device row.
    if (!keepEmpty && kind === '设备') return
    props.devices.filter(device => device.business_node_id === node.id).forEach(device => {
      const id = deviceKey(device)
      const alarmCount = (props.alarms || []).filter(alarm => alarm.active && alarm.device_id === id).length
      result.push({ key: `device:${id}`, name: device.name, kindLabel: "设备", depth: depth + 1, count: alarmCount, index: result.length + 1 })
    })
  }
  session.snapshot.roots.forEach(id => {
    const root = session.snapshot!.nodes.find(node => node.id === id)
    if (!root) return
    // The project root is the container the Sites live in, not a customer
    // layer: the prototype starts the ledger at the Site.
    session.snapshot!.nodes.filter(child => child.parent === root.id).forEach(child => walk(child, 0))
  })
  return result
})
</script>

<style scoped>
.ledger { display: flex; flex-direction: column; padding: 16px 0; align-self: stretch; overflow: auto; }
.ledger-title { display: flex; justify-content: space-between; padding: 0 18px 10px; color: var(--muted); font-size: 10px; letter-spacing: .18em; }
.node { display: block; width: 100%; min-height: 0; border: 0; border-left: 3px solid transparent; background: transparent; color: var(--ink); padding: 9px 18px; text-align: left; font-size: 12.5px; line-height: 1.4; overflow: visible; border-radius: 0; cursor: pointer; }
.node.active { border-left-color: var(--accent); color: #172c3b; background: rgba(255,255,255,.65); font-weight: 600; }
.index { display: inline; color: var(--muted); font-size: 10.5px; font-variant-numeric: tabular-nums; margin-right: 8px; }
.count { display: inline; float: right; color: var(--amber); font-size: 11px; }
.ledger-note { margin: 14px 18px 2px; padding-top: 10px; border-top: 1px solid var(--line); color: var(--muted); font-size: 10.5px; line-height: 1.9; }
@media (max-width: 760px) {
  .ledger { position: sticky; top: 8px; z-index: 6; flex-direction: row; margin-bottom: 10px; padding: 6px; overflow-x: auto; border-radius: 14px; }
  .ledger-title, .ledger-note { display: none; }
  .node { width: auto; height: 50px; flex: none; border-left: 0; border-bottom: 2px solid transparent; padding: 8px 10px; white-space: nowrap; font-size: 12px; }
  .node.active { border-bottom-color: var(--accent); background: transparent; }
}
</style>
