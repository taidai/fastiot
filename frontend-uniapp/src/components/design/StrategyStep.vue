<template>
  <view class="strategy">
    <view class="strategy-head">
      <view>
        <view class="step-head"><b>第 4 步 · 配策略</b><span>通用决策表，按顺序首命中。</span></view>
        <p class="note">同一行的点位条件须全部满足；时间条件使用站点本地时间。保存草稿不影响运行版本。</p>
      </view>
      <span v-if="selectedRule" class="chip" :class="dirty ? 'warn' : 'ok'">{{ dirty ? '有未保存修改' : '草稿已读取' }}</span>
    </view>

    <view v-if="rules.length > 1" class="rule-tabs" aria-label="决策表列表">
      <button v-for="rule in rules" :key="rule.id" :class="{ active: rule.id === selectedId }"
        :disabled="busy !== '' || (dirty && rule.id !== selectedId)" @click="selectedId = rule.id">
        {{ nameOf(rule) }}
      </button>
    </view>

    <view v-if="!rules.length" class="empty">当前还没有决策表。空数据不以演示规则替代。</view>

    <template v-else-if="selectedRule">
      <view class="rule-summary">
        <view><strong>{{ nameOf(selectedRule) }}</strong><span class="src"> {{ rows.length }} 行 · {{ referencedInputs.length }} 个输入</span></view>
        <view class="chips">
          <span class="chip" :class="published ? 'ok' : ''">{{ published ? '已有发布版本' : '尚未发布' }}</span>
          <span class="chip" :class="enabled ? 'ok' : ''">{{ enabled ? '自动运行已启用' : '自动运行已停用' }}</span>
        </view>
      </view>
      <p v-if="runtimeError" class="error">运行异常：{{ runtimeError }}</p>
      <p v-if="loadError" class="error">{{ loadError }}</p>

      <div class="table-wrap">
        <table v-if="rows.length" class="rule-table">
          <thead><tr><th>#</th><th>条件组（全部满足）</th><th>持续 / 冷却</th><th>动作</th><th>评估</th><th>操作</th></tr></thead>
          <tbody>
            <template v-for="(row, rowIndex) in rows" :key="row.row_id">
              <tr :class="{ hit: result?.row_id === row.row_id }">
                <td class="num">{{ rowIndex + 1 }}</td>
                <td>
                  <view v-if="!row.conditions.length" class="default-label">默认行</view>
                  <view v-for="(condition, conditionIndex) in row.conditions" :key="conditionIndex" class="condition-summary">
                    {{ conditionText(condition, displayName) }}
                  </view>
                  <view v-if="!row.conditions.length && rowIndex < rows.length - 1" class="warning-text">其下规则不会执行</view>
                </td>
                <td class="num">{{ durationText(row.hold_ms) }} / {{ durationText(row.cooldown_ms) }}</td>
                <td><view v-for="(action, actionIndex) in row.actions" :key="actionIndex">{{ displayName(action.action_id) }} = {{ action.value }}</view><span v-if="!row.actions.length" class="src">不执行动作</span></td>
                <td><span v-if="result?.row_id === row.row_id" class="chip ok">本次命中</span><span v-else class="src">{{ lastMatchId === row.row_id ? '最近运行命中' : '—' }}</span></td>
                <td class="row-actions">
                  <button class="ghost" aria-label="上移规则" :disabled="readOnly || busy !== '' || rowIndex === 0" @click="move(rowIndex, -1)">↑</button>
                  <button class="ghost" aria-label="下移规则" :disabled="readOnly || busy !== '' || rowIndex === rows.length - 1" @click="move(rowIndex, 1)">↓</button>
                  <button class="ghost" @click="expandedId = expandedId === row.row_id ? '' : row.row_id">{{ expandedId === row.row_id ? '收起' : (readOnly ? '查看' : '编辑') }}</button>
                  <button class="ghost danger" :disabled="readOnly || busy !== ''" @click="removeRow(rowIndex)">删除</button>
                </td>
              </tr>
              <tr v-if="expandedId === row.row_id" class="editor-row"><td colspan="6">
                <fieldset class="row-editor" :disabled="readOnly || busy !== ''">
                  <view class="editor-section">
                    <view class="editor-title"><strong>条件组</strong><span>无嵌套、无 OR；空条件为默认行</span></view>
                    <view v-for="(condition, conditionIndex) in row.conditions" :key="conditionIndex" class="condition-editor">
                      <select aria-label="条件类型" :value="condition.kind || 'number'" @change="changeConditionKind(condition, $event)">
                        <option value="number">数值</option><option value="bool">布尔</option><option value="enum">枚举</option><option value="time">时间</option>
                      </select>
                      <template v-if="condition.kind === 'time'">
                        <input aria-label="开始时间" type="time" :value="condition.start || ''" @input="condition.start = inputValue($event); changed()">
                        <span>至</span>
                        <input aria-label="结束时间" type="time" :value="condition.end || ''" @input="condition.end = inputValue($event); changed()">
                        <view class="weekdays" aria-label="星期">
                          <label v-for="day in weekdayOptions" :key="day.bit"><input type="checkbox" :checked="Boolean((condition.weekday_mask || 0) & (1 << day.bit))" @change="toggleWeekday(condition, day.bit, $event)">{{ day.label }}</label>
                        </view>
                      </template>
                      <template v-else>
                        <select aria-label="输入点位" :value="condition.point_id" @change="condition.point_id = inputValue($event); changed()">
                          <option v-if="condition.point_id && !availableIds.includes(condition.point_id)" :value="condition.point_id">{{ displayName(condition.point_id) }}（当前未接入）</option>
                          <option v-for="node in available" :key="node.id" :value="businessId(node)">{{ nameOf(node) }}</option>
                        </select>
                        <select aria-label="比较逻辑" :value="condition.operator" @change="condition.operator = inputValue($event); changed()">
                          <option v-for="operator in operatorsFor(condition.kind)" :key="operator.value" :value="operator.value">{{ operator.label }}</option>
                        </select>
                        <select v-if="condition.kind === 'bool'" aria-label="条件值" :value="condition.value" @change="condition.value = inputNumber($event); changed()"><option :value="1">是</option><option :value="0">否</option></select>
                        <input v-else-if="condition.kind === 'enum'" aria-label="枚举值" :value="condition.text || ''" placeholder="枚举文本" @input="condition.text = inputValue($event); changed()">
                        <input v-else aria-label="条件值" type="number" step="any" :value="condition.value" @input="condition.value = inputNumber($event); changed()">
                      </template>
                      <button class="ghost" aria-label="删除条件" @click="row.conditions.splice(conditionIndex, 1); changed()">×</button>
                    </view>
                    <view class="actions"><button class="ghost" :disabled="!available.length" @click="addCondition(row)">＋ 点位条件</button><button class="ghost" @click="addTimeCondition(row)">＋ 时间条件</button></view>
                  </view>

                  <view class="editor-section">
                    <view class="editor-title"><strong>持续与冷却</strong><span>0 表示不等待 / 不冷却</span></view>
                    <view class="timing-inputs">
                      <label>持续确认（秒）<input type="number" min="0" step="0.1" :value="(row.hold_ms || 0) / 1000" @input="setTiming(row, 'hold_ms', $event)"></label>
                      <label>冷却（秒）<input type="number" min="0" step="0.1" :value="(row.cooldown_ms || 0) / 1000" @input="setTiming(row, 'cooldown_ms', $event)"></label>
                    </view>
                  </view>

                  <view class="editor-section">
                    <view class="editor-title"><strong>动作</strong><span>仅数值设定；没有延时或动作窗口字段</span></view>
                    <view v-for="(action, actionIndex) in row.actions" :key="actionIndex" class="action-editor">
                      <select aria-label="输出点位" :value="action.action_id" @change="action.action_id = inputValue($event); changed()">
                        <option v-if="action.action_id && !outputIds.includes(action.action_id)" :value="action.action_id">{{ displayName(action.action_id) }}（当前不可写）</option>
                        <option v-for="node in outputs" :key="node.id" :value="businessId(node)">{{ nameOf(node) }}{{ node.type === 'group' ? '（分配器）' : '' }}</option>
                      </select>
                      <label>设为 <input aria-label="输出值" type="number" step="any" :value="action.value" @input="action.value = inputNumber($event); changed()"></label>
                      <button class="ghost" aria-label="删除动作" @click="row.actions.splice(actionIndex, 1); changed()">×</button>
                    </view>
                    <view v-if="!row.actions.length" class="src">不执行动作</view>
                    <view class="actions"><button class="ghost" :disabled="!outputs.length" @click="addAction(row)">＋ 动作</button></view>
                  </view>
                </fieldset>
              </td></tr>
            </template>
          </tbody>
        </table>
        <view v-else class="empty">这张决策表还没有规则行。可添加条件行，或添加所有前行未命中时执行的默认行。</view>
      </div>

      <view class="actions" v-if="!readOnly">
        <button class="ghost" :disabled="busy !== ''" @click="addRow(false)">＋ 条件行</button>
        <button class="ghost" :disabled="busy !== ''" @click="addRow(true)">＋ 默认行</button>
        <button v-if="dirty" class="link" :disabled="busy !== ''" @click="loadRule">放弃未保存修改</button>
      </view>

      <view class="simulation">
        <view class="editor-title"><strong>用当前草稿模拟</strong><span>后端纯计算，不写设备</span></view>
        <view class="simulation-grid">
          <label v-for="id in referencedInputs" :key="id">{{ displayName(id) }}
            <select v-if="inputKind(id) === 'bool'" :value="inputs[id] ?? 0" @change="inputs[id] = inputNumber($event); result = null"><option :value="1">是</option><option :value="0">否</option></select>
            <input v-else-if="inputKind(id) === 'enum'" :value="textInputs[id] || ''" placeholder="枚举文本" @input="textInputs[id] = inputValue($event); result = null">
            <input v-else type="number" step="any" :value="inputs[id] ?? 0" @input="inputs[id] = inputNumber($event); result = null">
          </label>
          <label>模拟时刻<input type="datetime-local" :value="simAt" @input="simAt = inputValue($event); result = null"></label>
        </view>
        <view class="actions"><button v-if="referencedInputs.length" class="ghost" @click="useLiveInputs">使用当前值</button><button class="primary" :disabled="busy !== ''" @click="simulate">{{ busy === 'simulate' ? '计算中…' : '开始模拟' }}</button></view>
        <p class="note">模拟只计算该时刻的首命中，不推演持续计时或冷却状态；结果不会生成设备命令。</p>
        <view v-if="result" class="sim-result" role="status">
          <strong>{{ result.row_id ? `命中第 ${rowPosition(result.row_id)} 行` : '没有命中规则' }}</strong>
          <span v-if="result.actions?.length">{{ result.actions.map(action => `${displayName(action.action_id)} = ${action.value}${action.blocked ? `（已拦截：${action.reason || '—'}）` : ''}`).join('；') }}</span>
          <span v-else>不执行任何动作</span>
          <span v-for="(check, index) in result.time_checks || []" :key="index">时间条件 {{ timeConditionText(check) }}：{{ check.satisfied ? '满足' : '不满足' }}</span>
          <span v-for="reason in result.blocked || []" :key="reason">跳过：{{ reason }}</span>
        </view>
      </view>

      <view class="publish-bar">
        <view><strong>自动运行</strong><p class="note">{{ published ? '启停只改变当前规则运行授权，不修改草稿。' : '发布有效版本后才能启用。' }} 最近命中：{{ lastMatchText }}</p></view>
        <view class="actions">
          <button class="ghost" :disabled="readOnly || busy !== '' || (!enabled && !published)" @click="setEnabled(!enabled)">{{ enabled ? '停用自动' : '启用自动' }}</button>
          <button class="ghost" :disabled="readOnly || busy !== '' || !dirty" @click="saveDraft">{{ busy === 'save' ? '保存中…' : '保存草稿' }}</button>
          <button class="primary" :disabled="readOnly || busy !== '' || !rows.length" @click="confirmPublish = true">发布当前规则</button>
        </view>
      </view>
      <view v-if="confirmPublish" class="publish-confirm" role="alert">
        <span>发布会将当前 {{ rows.length }} 行草稿设为运行版本{{ enabled ? '，已启用规则会使用新版本' : '，但不会自动启用' }}。</span>
        <button class="ghost" :disabled="busy !== ''" @click="confirmPublish = false">取消</button>
        <button class="primary" :disabled="busy !== ''" @click="publish">{{ busy === 'publish' ? '发布中…' : '确认发布' }}</button>
      </view>
      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
    </template>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf, point } from "../../types/api"
import type { Node, Snapshot } from "../../types/api"
import {
  businessId,
  conditionText,
  createSimulationPayload,
  isWritable,
  moveRows,
  parseDocument,
  timeConditionText,
  withBindings,
} from "./strategy"
import type { Condition, DecisionDocument, Evaluation, Row } from "./strategy"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; rules: Node[]; readOnly?: boolean }>(), { readOnly: false })
const emit = defineEmits<{ (event: "refresh"): void }>()

const selectedId = ref("")
const document = ref<DecisionDocument>(parseDocument())
const expandedId = ref("")
const dirty = ref(false)
const published = ref(false)
const enabled = ref(false)
const busy = ref("")
const error = ref("")
const loadError = ref("")
const notice = ref("")
const confirmPublish = ref(false)
const result = ref<Evaluation | null>(null)
const inputs = ref<Record<string, number>>({})
const textInputs = ref<Record<string, string>>({})
const simAt = ref("")
const weekdayOptions = [{ label: "一", bit: 1 }, { label: "二", bit: 2 }, { label: "三", bit: 3 }, { label: "四", bit: 4 }, { label: "五", bit: 5 }, { label: "六", bit: 6 }, { label: "日", bit: 0 }]

const nodes = computed(() => props.snapshot?.nodes || [])
const available = computed(() => nodes.value.filter(node => node.type === "variable" || node.type === "modbusIo"))
const availableIds = computed(() => available.value.map(businessId))
const outputs = computed(() => [
  ...available.value.filter(isWritable),
  ...nodes.value.filter(node => node.type === "group" && point(node, "easyemsAllocator")?.text),
])
const outputIds = computed(() => outputs.value.map(businessId))
const selectedRule = computed(() => props.rules.find(rule => rule.id === selectedId.value) || null)
const rows = computed(() => document.value.table.rows)
const referencedInputs = computed(() => [...new Set(rows.value.flatMap(row => row.conditions.filter(condition => condition.kind !== "time").map(condition => condition.point_id)))])
const runtimeError = computed(() => selectedRule.value ? point(selectedRule.value, "error")?.text || "" : "")
const lastMatchId = computed(() => selectedRule.value ? point(selectedRule.value, "lastMatch")?.text || "" : "")
const lastMatchText = computed(() => lastMatchId.value ? (rows.value.some(row => row.row_id === lastMatchId.value) ? `第 ${rowPosition(lastMatchId.value)} 行` : lastMatchId.value) : "—")

watch(() => props.rules.map(rule => rule.id).join("|"), () => {
  if (!props.rules.some(rule => rule.id === selectedId.value)) selectedId.value = props.rules[0]?.id || ""
}, { immediate: true })
watch(selectedId, () => loadRule(), { immediate: true })

function loadRule() {
  const rule = selectedRule.value
  dirty.value = false
  result.value = null
  error.value = ""
  loadError.value = ""
  notice.value = ""
  confirmPublish.value = false
  expandedId.value = ""
  published.value = !!rule && !!point(rule, "applied")?.text
  enabled.value = !!rule && (point(rule, "enabled")?.value || 0) !== 0
  try {
    document.value = withBindings(parseDocument(rule ? point(rule, "draft")?.text : ""), nodes.value)
  } catch (cause) {
    document.value = withBindings(parseDocument(), nodes.value)
    loadError.value = cause instanceof Error ? `${cause.message}；未覆盖服务端草稿。` : "草稿无法读取"
  }
  resetLiveInputs()
}

function displayName(id: string) {
  const node = nodes.value.find(item => businessId(item) === id)
  return node ? nameOf(node) : id || "—"
}
function durationText(milliseconds?: number) { return milliseconds ? `${milliseconds / 1000}s` : "0s" }
function rowPosition(id: string) { const index = rows.value.findIndex(row => row.row_id === id); return index >= 0 ? index + 1 : "—" }
function inputValue(event: Event) { return (event.target as HTMLInputElement | HTMLSelectElement).value }
function inputNumber(event: Event) {
  const raw = inputValue(event).trim()
  const value = Number(raw)
  return raw && Number.isFinite(value) ? value : 0
}
function changed() {
  dirty.value = true
  result.value = null
  notice.value = ""
  error.value = ""
  confirmPublish.value = false
}
function currentDocument() {
  document.value = withBindings(document.value, nodes.value)
  return document.value
}
function addRow(defaultRow: boolean) {
  const first = available.value[0]
  const id = crypto.randomUUID()
  rows.value.push({
    row_id: id,
    conditions: !defaultRow && first ? [{ point_id: businessId(first), operator: "gt", value: 0 }] : [],
    actions: [],
  })
  expandedId.value = id
  changed()
}
function removeRow(index: number) { rows.value.splice(index, 1); changed() }
function move(index: number, direction: -1 | 1) { document.value.table.rows = moveRows(rows.value, index, direction); changed() }
function addCondition(row: Row) {
  const first = available.value[0]
  if (!first) return
  row.conditions.push({ point_id: businessId(first), operator: "gt", value: 0 })
  changed()
}
function addTimeCondition(row: Row) {
  row.conditions.push({ point_id: "", operator: "", value: 0, kind: "time", start: "08:00", end: "20:00", weekday_mask: 0 })
  changed()
}
function addAction(row: Row) {
  const first = outputs.value[0]
  if (!first) return
  row.actions.push({ action_id: businessId(first), value: 0 })
  changed()
}
function operatorsFor(kind?: Condition["kind"]) {
  return kind === "bool" || kind === "enum"
    ? [{ value: "eq", label: "等于" }, { value: "ne", label: "不等于" }]
    : [{ value: "gt", label: ">" }, { value: "ge", label: "≥" }, { value: "eq", label: "=" }, { value: "ne", label: "≠" }, { value: "le", label: "≤" }, { value: "lt", label: "<" }]
}
function changeConditionKind(condition: Condition, event: Event) {
  const kind = inputValue(event) as NonNullable<Condition["kind"]>
  if (kind === "time") Object.assign(condition, { kind, point_id: "", operator: "", value: 0, text: "", start: condition.start || "08:00", end: condition.end || "20:00", weekday_mask: condition.weekday_mask || 0 })
  else Object.assign(condition, { kind, point_id: condition.point_id || availableIds.value[0] || "", operator: kind === "number" ? "gt" : "eq", value: kind === "bool" ? 0 : condition.value, text: kind === "enum" ? condition.text || "" : "", start: "", end: "", weekday_mask: 0 })
  changed()
}
function toggleWeekday(condition: Condition, bit: number, event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  const mask = condition.weekday_mask || 0
  condition.weekday_mask = checked ? mask | (1 << bit) : mask & ~(1 << bit)
  changed()
}
function setTiming(row: Row, field: "hold_ms" | "cooldown_ms", event: Event) {
  row[field] = Math.max(0, Math.round(inputNumber(event) * 1000))
  changed()
}
function inputKind(id: string): Condition["kind"] {
  return rows.value.flatMap(row => row.conditions).find(condition => condition.point_id === id && condition.kind && condition.kind !== "number")?.kind || "number"
}
function resetLiveInputs() {
  inputs.value = Object.fromEntries(available.value.map(node => [businessId(node), point(node, "value")?.value || 0]))
  textInputs.value = Object.fromEntries(available.value.map(node => [businessId(node), point(node, "value")?.text || ""]))
}
function useLiveInputs() {
  result.value = null
  for (const id of referencedInputs.value) {
    const node = available.value.find(item => businessId(item) === id)
    if (!node) continue
    inputs.value[id] = point(node, "value")?.value || 0
    textInputs.value[id] = point(node, "value")?.text || ""
  }
}
async function simulate() {
  if (!selectedRule.value || busy.value) return
  busy.value = "simulate"
  result.value = null
  error.value = ""
  notice.value = ""
  try {
    result.value = await request<Evaluation>(`/rules/${encodeURIComponent(selectedRule.value.id)}/test`, "POST", createSimulationPayload(currentDocument(), inputs.value, textInputs.value, simAt.value))
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "模拟失败"
  } finally {
    busy.value = ""
  }
}
async function saveDraft() {
  if (!selectedRule.value || busy.value) return
  busy.value = "save"
  error.value = ""
  notice.value = ""
  try {
    await request(`/rules/${encodeURIComponent(selectedRule.value.id)}/draft`, "PUT", currentDocument())
    dirty.value = false
    notice.value = "草稿已保存，运行版本未改变。"
    emit("refresh")
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "保存草稿失败"
  } finally {
    busy.value = ""
  }
}
async function publish() {
  if (!selectedRule.value || busy.value || !rows.value.length) return
  busy.value = "publish"
  error.value = ""
  notice.value = ""
  try {
    await request(`/rules/${encodeURIComponent(selectedRule.value.id)}/publish`, "POST", currentDocument())
    dirty.value = false
    published.value = true
    confirmPublish.value = false
    notice.value = enabled.value ? "发布成功，已启用规则的运行版本已更新。" : "发布成功；规则仍保持停用。"
    emit("refresh")
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "发布失败"
  } finally {
    busy.value = ""
  }
}
async function setEnabled(value: boolean) {
  if (!selectedRule.value || busy.value || (value && !published.value)) return
  busy.value = "enable"
  error.value = ""
  notice.value = ""
  try {
    await request(`/rules/${encodeURIComponent(selectedRule.value.id)}/enable`, "POST", { enabled: value })
    enabled.value = value
    notice.value = value ? "自动运行已启用。" : "自动运行已停用。"
    emit("refresh")
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "启停失败"
  } finally {
    busy.value = ""
  }
}
</script>

<style scoped>
.strategy { display: grid; gap: 12px; min-width: 0; }
.strategy-head, .rule-summary, .publish-bar, .editor-title { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.step-head b { font-size: 16px; }
.step-head span, .note, .src { color: var(--faint); font-size: 12px; }
.note { margin: 5px 0 0; line-height: 1.65; }
.rule-tabs { display: flex; gap: 6px; overflow-x: auto; padding-bottom: 3px; }
.rule-tabs button { flex: none; border: 1px solid var(--glass-border); border-radius: 999px; padding: 6px 13px; background: rgba(255,255,255,.48); color: var(--muted); }
.rule-tabs button.active { border-color: var(--accent); color: var(--accent); background: rgba(255,255,255,.82); }
.rule-summary { flex-wrap: wrap; }
.rule-summary strong { font-size: 17px; }
.rule-table { min-width: 900px; }
.rule-table td { vertical-align: top; }
.rule-table tr.hit td { background: rgba(230,243,238,.46); }
.condition-summary { margin-bottom: 3px; }
.default-label { color: var(--accent); }
.warning-text, .danger { color: var(--danger); font-size: 11px; }
.row-actions { white-space: nowrap; }
.row-actions .ghost { padding: 4px 7px; margin: 0 0 4px 3px; }
.editor-row td { padding: 0; background: rgba(241,248,251,.58); }
.row-editor { border: 0; padding: 14px; margin: 0; }
.editor-section + .editor-section { border-top: 1px solid var(--line); margin-top: 14px; padding-top: 14px; }
.editor-title { align-items: baseline; margin-bottom: 9px; font-size: 12px; }
.editor-title span { color: var(--faint); font-weight: 400; }
.condition-editor, .action-editor { display: flex; align-items: center; gap: 7px; flex-wrap: wrap; margin-bottom: 8px; }
.condition-editor select, .condition-editor input, .action-editor select, .action-editor input,
.simulation-grid select, .simulation-grid input, .timing-inputs input { min-height: 40px; border: 1px solid var(--line); border-radius: 8px; padding: 6px 9px; background: rgba(255,255,255,.7); }
.condition-editor input[type="checkbox"] { min-height: auto; }
.weekdays { display: flex; gap: 5px; flex-wrap: wrap; }
.weekdays label { display: flex; align-items: center; gap: 2px; color: var(--muted); font-size: 12px; }
.timing-inputs { display: flex; gap: 12px; flex-wrap: wrap; }
.timing-inputs label, .simulation-grid label, .action-editor label { display: flex; align-items: center; gap: 7px; color: var(--muted); font-size: 12px; }
.action-editor label input { width: 120px; }
.simulation { border: 1px solid rgba(255,255,255,.72); border-radius: 12px; padding: 13px; background: rgba(255,255,255,.34); }
.simulation-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 9px; }
.simulation-grid label { align-items: stretch; flex-direction: column; }
.sim-result { display: grid; gap: 5px; margin-top: 12px; border: 1px solid rgba(35,112,95,.25); border-radius: 10px; padding: 11px 12px; background: rgba(238,248,243,.72); font-size: 12.5px; }
.publish-bar { align-items: flex-start; border-top: 1px solid var(--line); padding-top: 12px; }
.publish-bar .actions { justify-content: flex-end; margin-top: 0; }
.publish-confirm { display: flex; align-items: center; justify-content: flex-end; gap: 9px; flex-wrap: wrap; border: 1px solid #eeddb9; border-radius: 10px; padding: 10px 12px; background: #fdf7ea; color: #6f4a12; font-size: 12.5px; }
.publish-confirm span { flex: 1; min-width: 260px; }
.notice { margin: 0; color: var(--mint); font-size: 12.5px; }
@media (max-width: 760px) {
  .strategy-head, .publish-bar { align-items: flex-start; flex-direction: column; }
  .publish-bar .actions { justify-content: flex-start; }
  .condition-editor, .action-editor { align-items: stretch; }
}
</style>
