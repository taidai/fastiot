<template>
  <view class="today-executions today-summary" data-testid="today-executions">
    <view v-if="error" class="error" role="alert">{{ error }}</view>
    <view v-for="run in page?.items || []" :key="run.id" class="run">
      <span>{{ shortTime(run.at) }} {{ run.source_name }}</span><span class="r">{{ executionStatus(run.status) }}</span>
    </view>
    <view v-if="page && !page.items.length && !busy" class="run summary-empty">今日暂无已保留执行。</view>
    <p v-if="page" class="summary-line">{{ summaryText }}</p>
    <view class="summary-actions"><button class="ghost" @click="drawerOpen = true">查看全部</button></view>
    <view v-if="busy" role="status">读取执行记录…</view>
    <TodayExecutionsDrawer v-if="drawerOpen" :layer-id="layerId" @close="drawerOpen = false" />
  </view>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { request } from '../../api/client'
import { projectContext } from '../../stores/projects'
import { buildExecutionQuery, executionStatus, executionSummaryText, localDate } from './executions'
import type { ExecutionPage } from './executions'
import { shortTime } from './overview'
import TodayExecutionsDrawer from './TodayExecutionsDrawer.vue'

const props = defineProps<{ layerId: string }>()
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const page = ref<ExecutionPage | null>(null)
const busy = ref(false)
const error = ref('')
const drawerOpen = ref(false)
let serial = 0
async function load() {
  const run = ++serial
  page.value = null; error.value = ''
  if (!props.layerId) return
  busy.value = true
  try {
    const query = buildExecutionQuery(props.layerId, { date: localDate(), timezone: zone, limit: 3 })
    const result = await request<ExecutionPage>(`/overview/executions?${query}`)
    if (run === serial) page.value = result
  } catch (e) { if (run === serial) error.value = `执行记录读取失败：${e instanceof Error ? e.message : String(e)}` }
  finally { if (run === serial) busy.value = false }
}
const summaryText = computed(() => page.value ? executionSummaryText(page.value) : '')
watch(() => [props.layerId, projectContext.generation], () => { void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { serial++ })
</script>

<style scoped>
.today-summary .run { display: flex; justify-content: space-between; gap: 8px; padding: 7px 0; border-top: 1px solid var(--line); font-size: 12px; }
.today-summary .run:first-of-type { border-top: 0; }
.today-summary .summary-empty { color: var(--muted); }
.summary-line { margin: 8px 0 0; color: var(--muted); font-size: 10.5px; line-height: 1.7; }
.summary-actions { margin-top: 8px; }
.error { color: var(--danger); font-size: 11px; }
</style>
