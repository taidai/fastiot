<template>
  <view class="table-wrap" :class="{ compact }">
    <table>
      <thead><tr><th>{{ compact ? '时间' : '观测时间' }}</th><th>值</th><th>单位</th><th>质量</th><th v-if="!compact">来源</th></tr></thead>
      <tbody v-if="records.length">
        <tr v-for="(record, index) in records" :key="`${record.point_id}-${record.source_time || record.received_time}-${index}`">
          <td class="num time">{{ timeText(record) }}</td>
          <td class="num">{{ historyValueText(record) }}</td>
          <td>{{ record.unit || "—" }}</td>
          <td :class="{ off: record.quality && !['good', '有效'].includes(record.quality) }">
            {{ qualityText(record.quality) }}<view v-if="record.reason" class="reason">{{ record.reason }}</view>
          </td>
          <td v-if="!compact">{{ sourceText(record.source) }}</td>
        </tr>
      </tbody>
      <tbody v-else><tr class="placeholder-row"><td :colspan="compact ? 4 : 5">—</td></tr></tbody>
    </table>
  </view>
</template>

<script setup lang="ts">
import type { HistoryRecord } from "../types/api"
import { historyTimeText, historyValueText, qualityText } from "../pages/history/history"
const props = defineProps<{ records: HistoryRecord[]; compact?: boolean }>()
function timeText(record: HistoryRecord) {
  if (!props.compact) return historyTimeText(record)
  const value = record.source_time || record.received_time
  if (!value) return "—"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString("zh-CN", { hour12: false })
}
function sourceText(source: string) {
  return source === "modbus_runtime" ? "运行历史" : source === "siot" ? "点位历史" : source || "—"
}
</script>

<style scoped>
.time { white-space: nowrap; }
.off { color: var(--amber); }
.reason { margin-top: 2px; color: var(--faint); font-size: 10px; }
.placeholder-row td { height: 44px; color: var(--faint); text-align: center; }
</style>
