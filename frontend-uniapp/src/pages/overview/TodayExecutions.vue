<template>
 <view class="today-executions" data-testid="today-executions">
  <view class="filters"><input aria-label="执行日期" type="text" placeholder="YYYY-MM-DD" v-model="date" /><select aria-label="执行来源" v-model="source"><option value="">全部来源</option><option value="rule">规则</option><option value="machine">状态机</option></select><button class="ghost" :disabled="busy" @click="load(true)">查询</button></view>
  <small>日期按 {{ zone }}；仅统计保留且当前可见的执行，不代表全日总数。</small>
  <view v-if="error" class="error" role="alert">{{ error }}</view>
  <template v-if="page">
   <p class="coverage">覆盖不完整：规则仅保留采集启用后的执行事实，旧来源可能仅有最新检查；状态机为已有事件账本。未捕获、已删除来源和无执行身份检查不计为执行。</p>
   <p v-if="page.restricted">部分历史引用已不在当前权限范围，未展示。</p>
   <view v-for="run in page.items" :key="run.id" class="alarm-mini execution-row" :data-execution-id="run.execution_id">
    <view><b>{{ run.source_name }}</b> · {{ executionStatus(run.status) }}</view>
    <small>{{ new Date(run.at).toLocaleString() }} · {{ run.source_type === 'rule' ? '规则' : '状态机' }} {{ run.source_revision }}</small>
    <view v-if="run.reason">{{ run.reason }}</view>
    <details><summary>执行依据与动作</summary><small>来源 {{ run.source_id }}<br />执行 {{ run.execution_id || '无执行身份（不计数）' }}</small>
     <view v-for="action in run.actions" :key="action.id || action.output" class="action"><b>{{ action.output }}</b> · {{ actionStatus(action) }}<view v-if="action.reason">{{ action.reason }}</view><small>动作 {{ action.id || '未记录运行身份' }}<template v-if="action.target"><br />目标 {{ action.target }}</template><br v-if="action.command_id" /><template v-if="action.command_id">命令 {{ action.command_id }} · 命令账本 {{ action.canonical_status }}（不改写过程结果）</template><br v-if="action.notification_id" /><template v-if="action.notification_id">通知 {{ action.notification_id }}</template></small></view>
    </details>
   </view>
   <view v-if="!page.items.length && !busy" class="empty">此范围、日期暂无可见的已保留记录</view>
   <view class="pagination"><button class="ghost" :disabled="busy || offset === 0" @click="move(-1)">上一页</button><span>{{ page.items.length ? offset + 1 : 0 }}–{{ page.items.length ? offset + page.items.length : 0 }} / {{ page.total }} 条 · {{ page.execution_count }} 次已保留执行</span><button class="ghost" :disabled="busy || offset + page.items.length >= page.total" @click="move(1)">下一页</button></view>
  </template>
  <view v-if="busy" role="status">读取执行记录…</view>
 </view>
</template>
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { request } from '../../api/client'
import { PREVIEW_MODE } from '../../api/source'
import { projectContext } from '../../stores/projects'
import { executionStatus, actionStatus } from './executions'
import type { ExecutionPage } from './executions'
const props = defineProps<{ layerId: string }>()
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const now = new Date()
const date = ref(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`)
const source = ref(''), offset = ref(0), busy = ref(false), error = ref('')
const page = ref<ExecutionPage | null>(null)
const limit = 5
let serial = 0
async function load(reset = false) {
 const run = ++serial
 if (reset) offset.value = 0
 page.value = null; error.value = ''; busy.value = false
 if (PREVIEW_MODE || !props.layerId) return
 busy.value = true
 try {
  const query = new URLSearchParams({ layer: props.layerId, date: date.value, timezone: zone, source: source.value, offset: String(offset.value), limit: String(limit) })
  const result = await request<ExecutionPage>(`/overview/executions?${query}`)
  if (run === serial) page.value = result
 } catch (e) { if (run === serial) error.value = `执行记录读取失败：${e instanceof Error ? e.message : String(e)}` }
 finally { if (run === serial) busy.value = false }
}
function move(direction: number) { offset.value = Math.max(0, offset.value + direction * limit); void load() }
watch(() => [props.layerId, projectContext.generation, source.value, date.value], () => { void load(true) }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { serial++ })
</script>
<style scoped>
.filters,.pagination{display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin:10px 0}.filters input,.filters select{font:inherit;color:inherit;background:transparent;border:1px solid #b8c2c4;border-radius:6px;padding:6px;min-width:0}.filters input{height:36px;line-height:24px}.today-executions small,.coverage{font-size:11px;opacity:.75;overflow-wrap:anywhere}.execution-row{display:block;padding:10px 0;border-bottom:1px solid #c7cecf}.action{margin-top:8px}.pagination{font-size:11px}.today-executions summary{cursor:pointer;margin-top:6px}.error{color:#a33}
</style>
