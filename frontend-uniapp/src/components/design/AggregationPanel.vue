<template>
  <view class="aggregate-panel">
    <view class="panel-heading"><view><strong>聚合定义与聚合点</strong><p class="note">直接复用现有 /aggregate 运行逻辑；窗口、成员质量和滞后均来自真实结果。</p></view><button class="ghost" :disabled="busy || !parent" @click="load">刷新</button></view>
    <view v-if="!parent" class="empty">未选择聚合对象。</view>
    <template v-else>
      <view class="two-col">
        <view class="acc-card aggregate-definition">
          <view class="card-title">聚合定义</view>
          <fieldset class="aggregate-form" :disabled="readOnly || busy">
            <view class="row"><span>算子</span><view class="chips"><button :class="{ active: op === 'sum' }" @click="op = 'sum'">sum</button><button :class="{ active: op === 'avg' }" @click="op = 'avg'">avg</button><button :class="{ active: op === 'min' }" @click="op = 'min'">min</button><button :class="{ active: op === 'max' }" @click="op = 'max'">max</button><button :class="{ active: op === 'count' }" @click="op = 'count'">count</button></view></view>
            <view class="row"><span>窗口</span><view class="chips"><button :class="{ active: window === '' }" @click="window = ''">瞬时</button><button :class="{ active: window === '5m' }" @click="window = '5m'">5m</button><button :class="{ active: window === '15m' }" @click="window = '15m'">15m</button><button :class="{ active: window === '1h' }" @click="window = '1h'">1h</button></view></view>
            <view class="row"><span>质量策略</span><view class="chips"><button :class="{ active: quality === 'ignore' }" @click="quality = 'ignore'">忽略失效</button><button :class="{ active: quality === 'count' }" @click="quality = 'count'">计入失效</button><button :class="{ active: quality === 'degrade' }" @click="quality = 'degrade'">降级</button></view></view>
            <label class="units-field">单位<input v-model="units" placeholder="可选"></label>
            <view class="card-title">成员</view>
            <div v-if="available.length" class="table-wrap"><table class="mini-table aggregate-members"><thead><tr><th>成员</th><th>值</th><th>质量</th></tr></thead><tbody><tr v-for="node in available" :key="node.id"><td><label class="member-check"><input type="checkbox" :checked="members.includes(node.id)" @change="toggleMember(node.id)"> {{ nameOf(node) }}</label></td><td>{{ memberValue(node.id) }}</td><td>{{ memberResult(node.id)?.quality || '—' }}</td></tr></tbody></table></div>
            <view v-else class="empty">当前作用域没有可聚合的数值点。</view>
            <div class="actions"><button class="primary" :disabled="readOnly || busy || !members.length" @click="save">保存并计算</button></div>
          </fieldset>
        </view>
        <view class="acc-card aggregate-result">
          <view class="card-title">聚合点 · {{ displayName(parent) }}</view>
          <template v-if="result?.configured">
            <view class="row"><span>当前值</span><span class="src"><b>{{ aggregateValueText(result.value, result.config.units) }}</b></span></view>
            <view class="row"><span>质量</span><span><span class="chip" :class="result.quality === 'good' ? 'ok' : 'warn'">{{ result.quality }}</span><span v-if="result.partial" class="chip warn">部分有效</span></span></view>
            <view class="row"><span>窗口</span><span class="src">{{ result.window ? `${result.window.start} 至 ${result.window.end} · ${result.window.members} 个成员有样本` : '瞬时 · 随采集周期' }}</span></view>
            <view class="row"><span>有效成员</span><span class="src">{{ result.valid ?? 0 }} / {{ result.total ?? 0 }}</span></view>
            <view class="row"><span>成员</span><span class="src">{{ (result.members || []).map(member => member.name || displayName(member.node_id)).join('、') || '—' }}</span></view>
            <p class="note">窗口值可参与规则，但必须标明滞后；联锁与保护只使用瞬时点。失效成员按质量策略处理，不补零。</p>
          </template>
          <p v-else class="note">先选算子、窗口与质量策略，再计算；这里展示的是节点上的只读聚合点，不是临时统计数字。</p>
        </view>
      </view>
      <p v-if="error" class="error" role="alert">{{ error }}</p><p v-if="notice" class="notice" role="status">{{ notice }}</p>

      <details class="existing-engines">
        <summary>现有计算点与虚拟输入</summary>
        <p class="note">这里只展示现有 calculation / virtual-input 的真实状态，不复制第二套计算引擎。</p>
        <view class="engine-grid">
          <view><strong>计算点</strong><view v-for="item in calculations" :key="item.id" class="engine-row"><span>{{ item.name }}</span><span>{{ aggregateValueText(item.state?.value, item.state?.unit) }} · {{ item.state?.quality || '—' }}</span></view><p v-if="!calculations.length" class="note">无计算点或尚未产生结果。</p></view>
          <view><strong>虚拟输入</strong><view v-for="item in virtualInputs" :key="item.id" class="engine-row"><span>{{ item.name }}（{{ item.source }}）</span><span>{{ item.state?.quality || '—' }}</span></view><p v-if="!virtualInputs.length" class="note">无虚拟输入。</p></view>
        </view>
        <p v-if="auxError" class="error">{{ auxError }}</p>
      </details>
    </template>
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf } from "../../types/api"
import type { Node, Snapshot } from "../../types/api"
import { aggregateValueText, createAggregateConfig } from "./aggregate"
import type { AggregateConfig, AggregateResult } from "./aggregate"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; parent: string; readOnly?: boolean }>(), { readOnly: false })
const result = ref<AggregateResult | null>(null)
const op = ref<AggregateConfig["op"]>("sum")
const window = ref<AggregateConfig["window"]>("")
const quality = ref<NonNullable<AggregateConfig["quality"]>>("count")
const units = ref("")
const members = ref<string[]>([])
const busy = ref(false)
const error = ref("")
const notice = ref("")
const auxError = ref("")
const calculations = ref<{ id: string; name: string; state?: { value?: number; quality?: string; unit?: string } }[]>([])
const virtualInputs = ref<{ id: string; name: string; source: string; state?: { quality?: string } }[]>([])
const nodes = computed(() => props.snapshot?.nodes || [])
const available = computed(() => nodes.value.filter(node => (node.type === "variable" || node.type === "modbusIo") && inScope(node)))
onMounted(load)
watch(() => props.parent, load)
function inScope(node: Node) {
  const byID = new Map(nodes.value.map(item => [item.id, item]))
  let current: Node | undefined = node
  const seen = new Set<string>()
  while (current && !seen.has(current.id)) {
    if (current.id === props.parent) return true
    seen.add(current.id)
    current = byID.get(current.parent)
  }
  return false
}
function displayName(id: string) { const node = nodes.value.find(item => item.id === id); return node ? nameOf(node) : id }
function memberResult(id: string) { return result.value?.members?.find(member => member.node_id === id) || null }
function memberValue(id: string) {
  const member = memberResult(id)
  return member && result.value ? aggregateValueText(member.value, result.value.config.units) : "—"
}
function toggleMember(id: string) { members.value = members.value.includes(id) ? members.value.filter(item => item !== id) : [...members.value, id] }
async function load() {
  if (!props.parent) return
  busy.value = true; error.value = ""; auxError.value = ""
  try {
    const value = await request<AggregateResult>(`/aggregate?group=${encodeURIComponent(props.parent)}`)
    result.value = value
    if (value.configured) {
      op.value = value.config.op; window.value = value.config.window || ""; quality.value = value.config.quality || "count"; units.value = value.config.units || ""; members.value = [...value.config.members]
    }
  } catch (cause) { error.value = cause instanceof Error ? cause.message : "聚合读取失败" }
  finally { busy.value = false }
  try {
    const [calc, inputs] = await Promise.all([
      request<{ calculations: typeof calculations.value }>("/calculations"),
      request<{ inputs: typeof virtualInputs.value }>("/virtual-inputs"),
    ])
    calculations.value = calc.calculations; virtualInputs.value = inputs.inputs
  } catch (cause) { auxError.value = cause instanceof Error ? cause.message : "计算点或虚拟输入读取失败" }
}
async function save() {
  busy.value = true; error.value = ""; notice.value = ""
  try {
    result.value = await request<AggregateResult>("/aggregate", "PUT", createAggregateConfig(props.parent, op.value, members.value, window.value, quality.value, units.value))
    notice.value = "聚合定义已保存；结果为本次真实计算。"
  } catch (cause) { error.value = cause instanceof Error ? cause.message : "聚合保存失败" }
  finally { busy.value = false }
}
</script>

<style scoped>
.aggregate-panel { display: grid; gap: 12px; min-width: 0; }
.panel-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; }
.note { color: var(--faint); font-size: 12px; line-height: 1.6; }
.two-col { display: grid; grid-template-columns: minmax(0,1fr) minmax(0,1fr); gap: 12px; align-items: start; }
.acc-card { min-width: 0; padding: 11px 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 10px; background: linear-gradient(135deg,rgba(255,255,255,.34),rgba(255,255,255,.06)),rgba(255,255,255,.42); box-shadow: 0 8px 22px rgba(45,83,110,.08),inset 0 1px 0 rgba(255,255,255,.75); }
.card-title { margin: 0 0 8px; color: var(--faint); font-size: 11px; font-weight: 600; letter-spacing: .06em; }
.aggregate-form { display: grid; gap: 10px; border: 0; padding: 0; margin: 0; }
.chips { display: flex; gap: 4px; flex-wrap: wrap; justify-content: flex-end; }
.chips button { border: 1px solid rgba(255,255,255,.7); border-radius: 999px; padding: 4px 10px; background: rgba(255,255,255,.48); color: var(--muted); font-size: 11.5px; }
.chips button.active { border-color: var(--accent); background: rgba(255,255,255,.8); color: var(--accent); }
.units-field { display: flex; align-items: center; justify-content: space-between; gap: 10px; color: var(--muted); font-size: 12px; }
.units-field input { width: 150px; min-height: 34px; border: 1px solid var(--line); border-radius: 8px; padding: 6px 8px; background: rgba(255,255,255,.72); }
.member-check { display: flex; align-items: center; gap: 6px; }
.member-check input { accent-color: var(--accent); }
.actions { display: flex; gap: 8px; margin-top: 4px; }
.existing-engines { border-top: 1px solid var(--line); padding-top: 12px; }
.existing-engines summary { cursor: pointer; font-weight: 600; }
.engine-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 14px; }
.engine-row { display: flex; justify-content: space-between; gap: 10px; padding: 7px 0; border-bottom: 1px solid var(--line); font-size: 12px; }
.notice { margin: 0; color: var(--mint); }.error { margin: 0; }
@media (max-width: 760px) { .two-col { grid-template-columns: 1fr; } }
</style>
