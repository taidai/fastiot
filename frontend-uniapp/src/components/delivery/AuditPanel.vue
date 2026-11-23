<template>
  <section class="card glass">
    <view class="step-head"><b>审计</b><span>谁在什么时候做过什么，结果是什么？</span></view>
    <view v-if="!canView" class="empty">当前账号没有审计查看权限；服务端会拒绝读取审计。</view>
    <template v-else>
      <form class="filters" @submit.prevent="load">
        <label>动作<select v-model="action"><option value="">全部动作</option><option v-for="item in actions" :key="item" :value="item">{{ item }}</option></select></label>
        <label>结果<select v-model="result"><option value="">全部结果</option><option value="success">成功</option><option value="failure">失败</option></select></label>
        <button class="ghost" type="button" :disabled="loading" @click="load">{{ loading ? "查询中…" : "查询" }}</button>
      </form>
      <view v-if="error" class="error">{{ error }}</view>
      <view v-else-if="loading && !records.length" class="empty">正在读取审计…</view>
      <view v-else-if="!records.length" class="empty">当前筛选条件下没有审计记录。</view>
      <view v-else class="table-wrap"><table><thead><tr><th>时间</th><th>人</th><th>动作</th><th>对象与详情</th><th>结果</th></tr></thead>
        <tbody><tr v-for="(item, index) in records" :key="`${item.time}-${item.actor}-${index}`"><td class="nowrap">{{ clock(item.time) }}</td><td>{{ item.actor || "—" }}</td><td>{{ item.action || "—" }}</td><td><b class="object">{{ item.object || "—" }}</b><small>{{ item.detail || "—" }}</small></td><td><span class="chip" :class="item.result === 'success' ? 'ok' : 'warn'">{{ item.result === "success" ? "成功" : item.result === "failure" ? "失败" : item.result || "—" }}</span></td></tr></tbody>
      </table></view>
      <p class="note">每次最多读取 200 条；审计记录来自后端，不用当前页面操作临时拼凑。</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from "vue"
import { request } from "../../api/client"
import { clock, deliveryError } from "./contracts"

type AuditRecord = { time: string; actor: string; action: string; object: string; detail: string; result: string }
const props = defineProps<{ canView: boolean }>()
const records = ref<AuditRecord[]>([])
const actions = ref<string[]>([])
const action = ref("")
const result = ref("")
const loading = ref(false)
const error = ref("")
watch(() => props.canView, allowed => { if (allowed) void load() })
async function load() {
  if (!props.canView) return
  loading.value = true
  error.value = ""
  try {
    const query = new URLSearchParams({ limit: "200" })
    if (action.value) query.set("action", action.value)
    if (result.value) query.set("result", result.value)
    const payload = await request<{ records: AuditRecord[]; actions: string[] }>(`/audit?${query.toString()}`)
    records.value = payload.records || []
    actions.value = payload.actions || []
  } catch (cause) {
    records.value = []
    error.value = deliveryError(cause)
  } finally { loading.value = false }
}
onMounted(() => { if (props.canView) void load() })
</script>

<style scoped>
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.step-head b { font-size: 16px; }
.step-head span, .note { color: var(--faint); font-size: 12px; }
.filters { display: flex; align-items: flex-end; gap: 9px; flex-wrap: wrap; margin-bottom: 12px; }
.filters label { display: grid; gap: 5px; color: var(--muted); font-size: 11.5px; }
.filters select { min-width: 150px; min-height: 44px; padding: 6px 9px; border: 1px solid var(--line); border-radius: 9px; background: rgba(255,255,255,.7); }
table { min-width: 760px; }
.nowrap { white-space: nowrap; }
.object { display: block; font-weight: 500; }
td small { display: block; margin-top: 3px; color: var(--faint); }
.note { margin-top: 12px; }
</style>
