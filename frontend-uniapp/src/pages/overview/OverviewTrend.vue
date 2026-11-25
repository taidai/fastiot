<template>
  <view class="overview-trend daily-trend">
    <view class="bcap">24 小时{{ label }}<text v-if="unit">（{{ unit }}）</text></view>
    <view v-if="trend.hasData" class="hour-bars" role="group" aria-label="最近 24 个小时桶；空桶不补值">
      <button v-for="(bucket, index) in trend.buckets" :key="bucket.time.toISOString()" class="hour-bar" :class="{ 'has-data': bucket.record }"
        :aria-label="bucketText(bucket)" :title="bucketText(bucket)" @mouseenter="selected = index" @mouseleave="selected = -1"
        @focus="selected = index" @blur="selected = -1" @click="emit('select')">
        <span v-if="bucket.record" class="bar-fill" :class="{ negative: bucket.negative }"
          :style="{ height: `${bucket.height}%`, bottom: `${100 - bucket.baseline - (bucket.negative ? bucket.height : 0)}%` }"></span>
      </button>
    </view>
    <view v-else class="bars-empty">—</view>
    <view v-if="selected >= 0" class="trend-tooltip" role="status">{{ bucketText(trend.buckets[selected]) }}</view>
  </view>
</template>

<script setup lang="ts">
import { computed, ref } from "vue"
import type { HistoryRecord } from "../../types/api"
import { buildHourlyTrend, qualityLabel } from "./overview"
import type { HourlyTrendBucket } from "./overview"

const props = defineProps<{ records: HistoryRecord[]; start: Date; end: Date; unit?: string; label: string }>()
const emit = defineEmits<{ (event: "select"): void }>()
const selected = ref(-1)
const trend = computed(() => buildHourlyTrend(props.records, props.start, props.end))
function bucketText(bucket: HourlyTrendBucket) {
  const time = bucket.time.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false })
  if (!bucket.record || bucket.value === undefined) return `${time} · 无数据`
  const value = Number(bucket.value.toPrecision(12))
  return `${time} · ${value}${props.unit ? ` ${props.unit}` : ""} · ${qualityLabel(bucket.record.quality)}`
}
</script>

<style scoped>
.overview-trend { position: relative; padding-left: 14px; border-left: 1px solid var(--line); }
.bcap { margin-bottom: 6px; color: var(--muted); font-size: 10px; }
.hour-bars { display: flex; align-items: stretch; gap: 2px; height: 54px; }
.hour-bar { position: relative; flex: 1; min-width: 0; min-height: 0; padding: 0; border: 0; border-radius: 2px; background: transparent; }
.hour-bar::after { content: ""; position: absolute; right: 0; bottom: 0; left: 0; height: 1px; background: var(--line); }
.bar-fill { position: absolute; right: 0; left: 0; min-height: 0; border-radius: 2px; background: var(--accent); opacity: .58; }
.bar-fill.negative { opacity: .35; }
.hour-bar:hover .bar-fill, .hour-bar:focus-visible .bar-fill { opacity: .95; }
.hour-bar:focus-visible { outline-offset: 1px; }
.bars-empty { display: flex; align-items: center; justify-content: center; height: 54px; color: var(--muted); }
.trend-tooltip { position: absolute; z-index: 4; right: 0; bottom: 62px; max-width: 260px; padding: 6px 9px; border: 1px solid var(--glass-border); border-radius: 8px; background: var(--glass-strong); box-shadow: var(--glass-shadow); color: var(--ink); font-size: 10px; pointer-events: none; }
</style>
