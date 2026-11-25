<template>
  <view class="strategy">
    <view class="strategy-head">
      <view class="step-head"><b>第 4 步 · 配策略</b><span>有序、首命中、可模拟；作用范围必须写清楚。</span></view>
      <span v-if="selectedRule" class="chip" :class="dirty ? 'warn' : 'ok'">{{ definitionRestricted ? '内容受限' : dirty ? '有未保存修改' : selectedRule.parent !== strategyParent ? '继承发布版本' : '草稿已读取' }}</span>
    </view>
    <section class="card glass step-card">
    <view class="panel-head"><h3>{{ strategyNodeName }}</h3><span class="tag">{{ strategyNodeKind }}</span><span class="tag" :title="`${ruleParent} · ${document.revision || ''}`">{{ ruleScopeLabel }}<template v-if="document.revision"> · {{ document.revision.slice(0, 8) }}</template></span></view>
    <view class="acc-grid">
      <view class="acc-card"><view class="card-title">作用范围</view><p class="note">{{ strategyScopeText }}</p><p class="note">仅显式声明“含下级”的发布规则可继承；继承规则只读，需回到来源对象修改。</p></view>
      <view class="acc-card machine-workspace"><StateMachinePanel :snapshot="snapshot" :parent="strategyParent" :read-only="props.readOnly" @refresh="emit('refresh')" /></view>
    </view>

    <view v-if="visibleRules.length > 1" class="rule-tabs" aria-label="决策表列表">
      <button v-for="rule in visibleRules" :key="rule.id" :class="{ active: rule.id === selectedId }"
        :disabled="busy !== '' || (dirty && rule.id !== selectedId)" @click="selectedId = rule.id">
        {{ nameOf(rule) }}
      </button>
    </view>

    <view v-if="!props.readOnly" class="actions">
      <select aria-label="新规则作用范围" v-model="newScope"><option value="" disabled>选择新规则作用范围</option><option value="self">本对象</option><option value="descendants">含下级</option></select>
      <button class="ghost" :disabled="!newScope || busy !== '' || dirty" @click="createRule">＋ 本对象决策表</button>
    </view>
    <view v-if="!visibleRules.length" class="empty">当前作用范围还没有决策表。空数据不以演示规则替代。</view>

    <template v-else-if="selectedRule">
      <view class="rule-summary">
        <label>作用范围
          <select aria-label="规则作用范围" :value="document.scope || ''" :disabled="readOnly || busy !== ''" @change="document.scope = inputValue($event) as 'self' | 'descendants'; changed()">
            <option value="" disabled>请选择作用范围</option><option value="self">本对象</option><option value="descendants">含下级</option>
          </select>
        </label>
        <view><strong>{{ nameOf(selectedRule) }}</strong><span class="src"> {{ rows.length }} 行 · {{ referencedInputs.length }} 个输入</span></view>
        <view class="chips">
          <span class="chip" :class="published ? 'ok' : ''">{{ published ? '已有发布版本' : '尚未发布' }}</span>
          <span class="chip" :class="enabled ? 'ok' : ''">{{ enabled ? '自动运行已启用' : '自动运行已停用' }}</span>
        </view>
      </view>
      <p v-if="runtimeError" class="error">运行异常：{{ runtimeError }}</p>
      <p v-if="loadError" class="error">{{ loadError }}</p>
      <p v-if="ruleAccess.history_restricted || ruleAccess.trace_restricted || ruleAccess.diagnostics_restricted" class="note" role="status">部分历史或运行详情因引用范围或来源限制而受限；当前可见定义保持原样。</p>

      <h5 class="section-title">决策表 · 有序，首命中</h5>
      <div class="table-wrap">
        <table v-if="rows.length" class="mini-table rules-table rule-table">
          <thead><tr><th>#</th><th>条件组</th><th>持续 / 冷却</th><th>时间窗</th><th>动作序列</th><th>评估</th><th>操作</th></tr></thead>
          <tbody>
            <template v-for="(row, rowIndex) in rows" :key="row.row_id">
              <tr :class="{ hit: result?.row_id === row.row_id }">
                <td class="num">{{ rowIndex + 1 }}</td>
                <td>
                  <view v-if="!row.conditions.length" class="default-label">默认行</view>
                  <view v-else class="condition-logic">{{ row.match === 'any' ? '任一满足（OR）' : '全部满足（AND）' }}</view>
                  <view v-for="(condition, conditionIndex) in pointConditions(row)" :key="conditionIndex" class="condition-summary">
                    {{ conditionText(condition, displayName) }}
                  </view>
                  <view v-if="!row.conditions.length && rowIndex < rows.length - 1" class="warning-text">其下规则不会执行</view>
                </td>
                <td class="num">{{ durationText(row.hold_ms) }} / {{ durationText(row.cooldown_ms) }}</td>
                <td>{{ timeWindowSummary(row) }}</td>
                <td><view v-for="(action, actionIndex) in row.actions" :key="actionIndex">{{ actionText(action, displayName) }}</view><span v-if="!row.actions.length" class="src">不执行动作</span></td>
                <td>
                  <span v-if="result?.row_id === row.row_id && result.state === 'fired'" class="chip ok">本次命中</span>
                  <span v-else-if="result?.row_id === row.row_id" class="chip warn">{{ result.state === 'waiting-hold' ? '持续不足' : result.state }}</span>
                  <view v-for="reason in traceFor(row.row_id)?.reasons || []" :key="reason" class="src">{{ reason }}</view>
                  <span v-if="!traceFor(row.row_id)" class="src">{{ lastMatchId === row.row_id ? '最近运行命中' : '—' }}</span>
                </td>
                <td class="row-actions">
                  <button class="ghost" aria-label="上移规则" :disabled="readOnly || busy !== '' || rowIndex === 0" @click="move(rowIndex, -1)">↑</button>
                  <button class="ghost" aria-label="下移规则" :disabled="readOnly || busy !== '' || rowIndex === rows.length - 1" @click="move(rowIndex, 1)">↓</button>
                  <button class="ghost" @click="expandedId = expandedId === row.row_id ? '' : row.row_id">{{ expandedId === row.row_id ? '收起' : (readOnly ? '查看' : '编辑') }}</button>
                  <button class="ghost danger" :disabled="readOnly || busy !== ''" @click="removeRow(rowIndex)">删除</button>
                </td>
              </tr>
              <tr v-if="expandedId === row.row_id" class="editor-row"><td colspan="7">
                <fieldset class="row-editor" :disabled="readOnly || busy !== ''">
                  <view class="editor-section">
                    <view class="editor-title"><strong>条件组</strong><span>同层逻辑，不嵌套；空条件为默认行</span></view>
                    <label class="group-match">条件关系
                      <select aria-label="条件关系" :value="row.match || 'all'" @change="setMatch(row, $event)">
                        <option value="all">全部满足（AND）</option><option value="any">任一满足（OR）</option>
                      </select>
                    </label>
                    <view v-for="(condition, conditionIndex) in pointConditions(row)" :key="conditionIndex" class="condition-editor">
                      <select aria-label="条件类型" :value="condition.kind || 'number'" @change="changeConditionKind(condition, $event)">
                        <option value="number">数值</option><option value="bool">布尔</option><option value="enum">枚举</option>
                      </select>
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
                      <button class="ghost" aria-label="删除条件" @click="removeCondition(row, condition)">×</button>
                    </view>
                    <view class="actions"><button class="ghost" :disabled="!available.length" @click="addCondition(row)">＋ 点位条件</button></view>
                  </view>

                  <view class="editor-section">
                    <view class="editor-title"><strong>持续与冷却</strong><span>0 表示不等待 / 不冷却</span></view>
                    <view class="timing-inputs">
                      <label>持续确认（秒）<input type="number" min="0" step="0.1" :value="(row.hold_ms || 0) / 1000" @input="setTiming(row, 'hold_ms', $event)"></label>
                      <label>冷却（秒）<input type="number" min="0" step="0.1" :value="(row.cooldown_ms || 0) / 1000" @input="setTiming(row, 'cooldown_ms', $event)"></label>
                    </view>
                  </view>

                  <view class="editor-section">
                    <view class="editor-title"><strong>时间窗</strong><span>独立于点位条件；不起时间窗时按常驻处理</span></view>
                    <view v-for="(condition, conditionIndex) in timeConditions(row)" :key="conditionIndex" class="condition-editor">
                      <input aria-label="开始时间" type="time" :value="condition.start || ''" @input="condition.start = inputValue($event); changed()">
                      <span>至</span>
                      <input aria-label="结束时间" type="time" :value="condition.end || ''" @input="condition.end = inputValue($event); changed()">
                      <checkbox-group class="weekdays" aria-label="星期" @change="setWeekdays(condition, $event)">
                        <label v-for="day in weekdayOptions" :key="day.bit"><checkbox :value="String(day.bit)" :checked="Boolean((condition.weekday_mask || 0) & (1 << day.bit))" :disabled="readOnly || busy !== ''" color="var(--accent)" role="checkbox" :aria-label="`周${day.label}`" :aria-checked="Boolean((condition.weekday_mask || 0) & (1 << day.bit))" :tabindex="readOnly || busy !== '' ? -1 : 0" @keydown.space.prevent="toggleWeekday(condition, day.bit)" />{{ day.label }}</label>
                      </checkbox-group>
                      <span class="src">未选星期表示每天</span>
                      <button class="ghost" aria-label="删除时间窗" @click="removeCondition(row, condition)">×</button>
                    </view>
                    <view v-if="!timeConditions(row).length" class="src">常驻</view>
                    <view class="actions"><button class="ghost" @click="addTimeCondition(row)">＋ 时间条件</button></view>
                  </view>

                  <view class="editor-section">
                    <view class="editor-title"><strong>动作序列</strong><span>前一步回读确认后才延时执行下一步；超时为结果未知；带斜率输出不在列表</span></view>
                    <view v-for="(action, actionIndex) in row.actions" :key="actionIndex" class="action-editor">
                      <span class="sequence-index">{{ actionIndex + 1 }}</span>
                      <select aria-label="输出点位" :value="action.action_id" @change="action.action_id = inputValue($event); changed()">
                        <option v-if="action.action_id && !outputIds.includes(action.action_id)" :value="action.action_id">{{ displayName(action.action_id) }}</option>
                        <option v-for="node in outputs" :key="node.id" :value="businessId(node)">{{ nameOf(node) }}{{ node.type === 'group' ? '（分配器）' : '' }}</option>
                      </select>
                      <select v-if="commandRequired[actionNodeID(action)]" aria-label="声明设备命令" :value="document.bindings.actions[action.action_id]?.command_id || ''" @change="setCommand(action, $event)">
                        <option value="">选择型号声明命令</option><option v-for="command in commandsFor(action)" :key="command.id" :value="command.id">{{ command.name }}</option>
                      </select>
                      <label>设为 <input aria-label="输出值" type="number" step="any" :value="action.value" @input="action.value = inputNumber($event); changed()"></label>
                      <label>延时（秒）<input aria-label="动作延时" type="number" min="0" step="0.1" :value="(action.delay_ms || 0) / 1000" @input="setActionTiming(row, action, 'delay_ms', $event)"></label>
                      <label>回读超时（秒）<input aria-label="回读超时" type="number" min="0.1" max="120" step="0.1" :value="(action.readback?.timeout_ms || 5000) / 1000" @input="setReadbackTiming(row, action, 'timeout_ms', $event)"></label>
                      <label>容差 <input aria-label="回读容差" type="number" min="0" step="any" :value="action.readback?.tolerance || 0" @input="setReadbackTiming(row, action, 'tolerance', $event)"></label>
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
        <view v-else class="empty">{{ definitionRestricted ? '规则内容受限，不能把不可见内容当作空规则。' : '这张决策表还没有规则行。可添加条件行，或添加所有前行未命中时执行的默认行。' }}</view>
      </div>

      <view class="actions" v-if="!readOnly">
        <button class="ghost" :disabled="busy !== ''" @click="addRow(false)">＋ 条件行</button>
        <button class="ghost" :disabled="busy !== ''" @click="addRow(true)">＋ 默认行</button>
        <button v-if="dirty" class="link" :disabled="busy !== ''" @click="loadRule">放弃未保存修改</button>
      </view>

      <view class="simulation">
        <view class="editor-title"><strong>用当前草稿模拟</strong><span>后端纯计算，不写设备</span></view>
        <view class="simulation-grid" :aria-busy="busy === 'simulate'">
          <label v-for="id in referencedInputs" :key="id">{{ displayName(id) }}
            <select v-if="inputKind(id) === 'bool'" :disabled="busy !== ''" :value="inputs[id] ?? ''" @change="setSimulationNumber(id, $event)"><option value="">无有效输入</option><option :value="1">是</option><option :value="0">否</option></select>
            <input v-else-if="inputKind(id) === 'enum'" :disabled="busy !== ''" :value="textInputs[id] || ''" placeholder="枚举文本" @input="textInputs[id] = inputValue($event); result = null">
            <input v-else type="number" :disabled="busy !== ''" step="any" :value="inputs[id] ?? ''" placeholder="无有效输入" @input="setSimulationNumber(id, $event)">
          </label>
          <label>模拟时刻<input :disabled="busy !== ''" type="datetime-local" :value="simAt" @input="simAt = inputValue($event); result = null"></label>
          <label>条件已持续（秒）<input :disabled="busy !== ''" type="number" min="0" step="0.1" :value="heldSeconds" @input="heldSeconds = Math.max(0, inputNumber($event)); result = null"></label>
        </view>
        <view class="actions"><button v-if="referencedInputs.length" class="ghost" :disabled="busy !== ''" @click="useLiveInputs">使用当前值</button><button class="primary" :disabled="busy !== '' || definitionRestricted" @click="simulate">{{ busy === 'simulate' ? '计算中…' : '开始模拟' }}</button></view>
        <p class="note">模拟使用上方假设的持续秒数，不读取或改写后台计时；动作只生成计划，不写设备。</p>
        <view v-if="result" class="sim-result" role="status">
          <strong>{{ result.state === 'waiting-hold' ? `第 ${rowPosition(result.row_id || '')} 行等待持续确认` : result.row_id ? `命中第 ${rowPosition(result.row_id)} 行` : '没有命中规则' }}</strong>
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
          <button class="primary" :disabled="readOnly || busy !== '' || !rows.length || dirty || enabled" @click="confirmPublish = true">发布当前规则</button>
        </view>
      </view>
      <view v-if="confirmPublish" class="publish-confirm" role="alert">
        <span>发布将固定已保存的 {{ rows.length }} 行草稿版本，不启用自动运行。修改运行版本前需先停用。</span>
        <button class="ghost" :disabled="busy !== ''" @click="confirmPublish = false">取消</button>
        <button class="primary" :disabled="busy !== ''" @click="publish">{{ busy === 'publish' ? '发布中…' : '确认发布' }}</button>
      </view>
      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
    </template>
    <details class="aggregate-disclosure"><summary>聚合与计算</summary><AggregationPanel :snapshot="snapshot" :parent="strategyParent" :read-only="props.readOnly" /></details>
    <view class="tool-line"><span>策略的输入从哪来</span><button class="ghost" @click="emit('tool', 'aggregate')">聚合计算 →</button><button class="ghost" @click="emit('tool', 'calculations')">计算点 →</button><button class="ghost" @click="emit('tool', 'virtualInputs')">虚拟输入 →</button></view>
    <view class="tool-line"><span>什么算异常</span><button class="ghost" @click="emit('tool', 'alarmTypes')">报警类型 →</button></view>
    <div class="actions"><button class="primary" @click="emit('navigate', 'ui')">下一步：配界面 →</button></div>
    </section>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import { request } from "../../api/client"
import StateMachinePanel from "./StateMachinePanel.vue"
import AggregationPanel from "./AggregationPanel.vue"
import { nameOf, point } from "../../types/api"
import { kindLabel } from "./design"
import type { Node, Snapshot, StateValue } from "../../types/api"
import {
  businessId,
  getRuleAccess,
  rulesForNode,
  referencedDocument,
  liveSimulationInputs,
  ruleInputValue,
  ruleInputNumber,
  ruleWeekdayMask,
  actionText,
  conditionText,
  createSimulationPayload,
  isWritable,
  isRamped,
  moveRows,
  parseDocument,
  timeConditionText,
  withBindings,
} from "./strategy"
import type { Condition, DecisionDocument, Evaluation, Row, Action } from "./strategy"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; rules: Node[]; values?: StateValue[]; selectedNodeId?: string; readOnly?: boolean }>(), { values: () => [], selectedNodeId: "", readOnly: false })
const emit = defineEmits<{ (event: "refresh"): void; (event: "navigate", value: "ui"): void; (event: "tool", value: string): void }>()

type CommandChoice = { id: string; name: string; available: boolean; point_id?: string; value?: number; parameters?: { name: string; point_id?: string }[] }
const commandChoices = ref<Record<string, CommandChoice[]>>({})
const commandRequired = ref<Record<string, boolean>>({})
const selectedId = ref("")
const newScope = ref<"" | "self" | "descendants">("")
const heldSeconds = ref(0)
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
  ...available.value.filter(node => isWritable(node) && !isRamped(node)),
  ...nodes.value.filter(node => node.type === "group" && point(node, "easyemsAllocator")?.text),
])
const outputIds = computed(() => outputs.value.map(businessId))
const defaultParent = computed(() => nodes.value.find(node => node.type === "siteRoot" || node.parent === "root")?.id || nodes.value[0]?.parent || "")
const strategyNode = computed(() => nodes.value.find(node => node.id === props.selectedNodeId) || nodes.value.find(node => node.id === defaultParent.value) || null)
const strategyParent = computed(() => strategyNode.value?.id || defaultParent.value)
const strategyNodeName = computed(() => strategyNode.value ? nameOf(strategyNode.value) : "未选择对象")
const strategyNodeKind = computed(() => strategyNode.value ? kindLabel(strategyNode.value) : "—")
const visibleRules = computed(() => rulesForNode(props.rules, nodes.value, strategyParent.value))
const ruleParent = computed(() => selectedRule.value?.parent || strategyParent.value)
const ruleScopeLabel = computed(() => ruleParent.value === strategyParent.value ? "本层决策表" : `继承 ${nodes.value.find(node => node.id === ruleParent.value) ? nameOf(nodes.value.find(node => node.id === ruleParent.value)!) : ruleParent.value}`)
const strategyScopeText = computed(() => `${ruleScopeLabel.value}；${document.value.scope === 'self' ? '本对象及其声明绑定' : document.value.scope === 'descendants' ? '本对象与下级及其声明绑定' : '尚未声明作用范围，不允许运行'}。`)
const selectedRule = computed(() => visibleRules.value.find(rule => rule.id === selectedId.value) || null)
const ruleAccess = computed(() => getRuleAccess(selectedRule.value))
const definitionRestricted = computed(() => selectedRule.value?.parent === strategyParent.value ? ruleAccess.value.draft_restricted : ruleAccess.value.applied_restricted)
const readOnly = computed(() => props.readOnly || definitionRestricted.value || (!!selectedRule.value && selectedRule.value.parent !== strategyParent.value))
const rows = computed(() => document.value.table.rows)
const referencedInputs = computed(() => [...new Set(rows.value.flatMap(row => row.conditions.filter(condition => condition.kind !== "time").map(condition => condition.point_id)))])
const runtimeError = computed(() => selectedRule.value ? point(selectedRule.value, "error")?.text || "" : "")
const lastMatchId = computed(() => selectedRule.value ? point(selectedRule.value, "lastMatch")?.text || "" : "")
const lastMatchText = computed(() => lastMatchId.value ? (rows.value.some(row => row.row_id === lastMatchId.value) ? `第 ${rowPosition(lastMatchId.value)} 行` : lastMatchId.value) : "—")

watch(() => visibleRules.value.map(rule => rule.id).join("|"), () => {
  if (!visibleRules.value.some(rule => rule.id === selectedId.value)) selectedId.value = visibleRules.value[0]?.id || ""
}, { immediate: true })
watch([selectedId, strategyParent], () => loadRule(), { immediate: true })
watch(definitionRestricted, () => loadRule())
watch(() => selectedRule.value && selectedRule.value.parent !== strategyParent.value ? point(selectedRule.value, 'applied')?.text : '', () => { if (!dirty.value) loadRule() })
watch(() => `${selectedId.value}|${outputs.value.map(node => node.id).join('|')}`, loadCommands, { immediate: true })

async function loadCommands() {
  const ruleID = selectedId.value
  commandChoices.value = {}
  commandRequired.value = {}
  const units = new Map<string, Node[]>()
  for (const node of outputs.value.filter(node => node.type === 'modbusIo')) {
    const server = point(node, 'id')?.value
    if (!server) continue
    const url = `/modbus/${encodeURIComponent(node.parent)}/units/${server}/model`
    units.set(url, [...(units.get(url) || []), node])
  }
  for (const [url, points] of units) {
    try {
      const view = await request<{ model?: { profile_id?: string }; commands: CommandChoice[] }>(url)
      if (selectedId.value !== ruleID) return
      for (const node of points) {
        commandRequired.value[node.id] = !!view.model?.profile_id
        commandChoices.value[node.id] = (view.commands || []).filter(command => command.available && ((command.parameters || []).filter(parameter => parameter.point_id === node.id).length === 1 || command.point_id === node.id))
      }
    } catch { if (selectedId.value === ruleID) for (const node of points) commandRequired.value[node.id] = true }
  }
}
function actionNodeID(action: Action) { return document.value.bindings.actions[action.action_id]?.node_id || '' }
function commandsFor(action: Action) { return commandChoices.value[actionNodeID(action)] || [] }
function setCommand(action: Action, event: Event) {
  const binding = document.value.bindings.actions[action.action_id]
  if (!binding) return
  const choice = commandsFor(action).find(command => command.id === inputValue(event))
  const node = nodes.value.find(node => node.id === binding.node_id)
  const alias = choice ? `${node ? businessId(node) : binding.node_id}::${choice.id}` : (node ? businessId(node) : binding.node_id)
  const parameter = choice?.parameters?.find(parameter => parameter.point_id === binding.node_id)?.name || ''
  document.value.bindings.actions[alias] = { ...binding, command_id: choice?.id || '', parameter }
  action.action_id = alias
  if (choice && !parameter && choice.value !== undefined) action.value = choice.value
  changed()
}

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
  if (definitionRestricted.value) {
    document.value = parseDocument()
    loadError.value = '规则定义引用超出当前范围或来源不可确认，内容受限。'
    inputs.value = {}; textInputs.value = {}
    return
  }
  try {
    document.value = withBindings(parseDocument(rule ? point(rule, rule.parent === strategyParent.value ? "draft" : "applied")?.text : ""), nodes.value)
  } catch (cause) {
    document.value = withBindings(parseDocument(), nodes.value)
    loadError.value = cause instanceof Error ? `${cause.message}；未覆盖服务端草稿。` : "草稿无法读取"
  }
  resetLiveInputs()
}

function displayName(id: string) {
  const binding = document.value.bindings.actions[id] || document.value.bindings.points[id]
  const node = nodes.value.find(item => businessId(item) === id || item.id === binding?.node_id)
  return node ? nameOf(node) : id || "—"
}
function durationText(milliseconds?: number) { return milliseconds ? `${milliseconds / 1000}s` : "0s" }
function pointConditions(row: Row) { return row.conditions.filter(condition => condition.kind !== "time") }
function timeConditions(row: Row) { return row.conditions.filter(condition => condition.kind === "time") }
function removeCondition(row: Row, condition: Condition) {
  const index = row.conditions.indexOf(condition)
  if (index >= 0) row.conditions.splice(index, 1)
  changed()
}
function timeWindowSummary(row: Row) {
  const windows = row.conditions.filter(condition => condition.kind === "time")
  return windows.length ? windows.map(timeConditionText).join("；") : "常驻"
}
function traceFor(rowID: string) { return result.value?.rows?.find(row => row.row_id === rowID) }
function setMatch(row: Row, event: Event) { row.match = inputValue(event) as "all" | "any"; changed() }
function rowPosition(id: string) { const index = rows.value.findIndex(row => row.row_id === id); return index >= 0 ? index + 1 : "—" }
function inputValue(event: Event) { return ruleInputValue(event) }
function inputNumber(event: Event) { return ruleInputNumber(event) }
function changed() {
  dirty.value = true
  result.value = null
  notice.value = ""
  error.value = ""
  confirmPublish.value = false
}
function currentDocument() {
  document.value = withBindings(document.value, nodes.value)
  return referencedDocument(document.value)
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
  row.actions.push({ action_id: businessId(first), value: 0, delay_ms: 0, readback: { timeout_ms: 5000, tolerance: 0 } })
  ensureSequence(row)
  changed()
}
function ensureSequence(row: Row) {
  if (!row.actions.some(action => action.delay_ms || action.readback)) return
  for (const action of row.actions) action.readback ||= { timeout_ms: 5000, tolerance: 0 }
}
function setActionTiming(row: Row, action: Action, field: "delay_ms", event: Event) {
  action[field] = Math.max(0, Math.round(inputNumber(event) * 1000))
  ensureSequence(row)
  changed()
}
function setReadbackTiming(row: Row, action: Action, field: "timeout_ms" | "tolerance", event: Event) {
  action.readback ||= { timeout_ms: 5000, tolerance: 0 }
  const value = inputNumber(event)
  if (field === "timeout_ms") action.readback.timeout_ms = Math.min(120000, Math.max(100, Math.round(value * 1000)))
  else action.readback.tolerance = Math.max(0, value)
  ensureSequence(row)
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
function setWeekdays(condition: Condition, event: unknown) {
  if (readOnly.value || busy.value) return
  condition.weekday_mask = ruleWeekdayMask(event)
  changed()
}
function toggleWeekday(condition: Condition, bit: number) {
  if (readOnly.value || busy.value) return
  condition.weekday_mask = (condition.weekday_mask || 0) ^ (1 << bit)
  changed()
}
function setTiming(row: Row, field: "hold_ms" | "cooldown_ms", event: Event) {
  row[field] = Math.max(0, Math.round(inputNumber(event) * 1000))
  changed()
}
function inputKind(id: string): Condition["kind"] {
  return rows.value.flatMap(row => row.conditions).find(condition => condition.point_id === id && condition.kind && condition.kind !== "number")?.kind || "number"
}
function resetLiveInputs() { useLiveInputs() }
function useLiveInputs() {
  result.value = null
  const values = liveSimulationInputs(currentDocument(), props.values)
  inputs.value = values.inputs
  textInputs.value = values.textInputs
}
function setSimulationNumber(id: string, event: Event) {
  const raw = inputValue(event).trim()
  const value = Number(raw)
  if (!raw || !Number.isFinite(value)) delete inputs.value[id]
  else inputs.value[id] = value
  result.value = null
}
function editContext() { return `${selectedId.value}|${strategyParent.value}|${document.value.revision || ''}` }
async function createRule() {
  if (!newScope.value || busy.value || dirty.value) return
  const context = editContext()
  busy.value = "create"
  error.value = ""
  try {
    const created = await request<{ id: string }>("/rules", "POST", { parent: strategyParent.value, description: `${strategyNodeName.value}规则`, document: { ...parseDocument(), scope: newScope.value } })
    if (context !== editContext()) { emit('refresh'); return }
    selectedId.value = created.id
    emit("refresh")
  } catch (cause) { if (context === editContext()) error.value = cause instanceof Error ? cause.message : "创建失败" }
  finally { busy.value = "" }
}
async function simulate() {
  if (!selectedRule.value || busy.value) return
  const context = editContext()
  busy.value = "simulate"
  result.value = null
  error.value = ""
  notice.value = ""
  try {
    const evaluation = await request<Evaluation>(`/rules/${encodeURIComponent(selectedRule.value.id)}/test`, "POST", createSimulationPayload(currentDocument(), inputs.value, textInputs.value, simAt.value, Math.round(heldSeconds.value * 1000)))
    if (context === editContext()) result.value = evaluation
  } catch (cause) {
    if (context === editContext()) error.value = cause instanceof Error ? cause.message : "模拟失败"
  } finally {
    busy.value = ""
  }
}
async function saveDraft() {
  if (!selectedRule.value || busy.value) return
  const context = editContext()
  busy.value = "save"
  error.value = ""
  notice.value = ""
  try {
    const saved = await request<{ revision: string }>(`/rules/${encodeURIComponent(selectedRule.value.id)}/draft`, "PUT", currentDocument())
    if (context !== editContext()) { emit('refresh'); return }
    document.value.revision = saved.revision
    dirty.value = false
    notice.value = "草稿已保存，运行版本未改变。"
    emit("refresh")
  } catch (cause) {
    if (context === editContext()) error.value = cause instanceof Error ? cause.message : "保存草稿失败"
  } finally {
    busy.value = ""
  }
}
async function publish() {
  if (!selectedRule.value || busy.value || !rows.value.length) return
  const context = editContext()
  busy.value = "publish"
  error.value = ""
  notice.value = ""
  try {
    await request(`/rules/${encodeURIComponent(selectedRule.value.id)}/publish`, "POST", currentDocument())
    if (context !== editContext()) { emit('refresh'); return }
    dirty.value = false
    published.value = true
    confirmPublish.value = false
    notice.value = "发布成功；规则仍保持停用。"
    emit("refresh")
  } catch (cause) {
    if (context === editContext()) error.value = cause instanceof Error ? cause.message : "发布失败"
  } finally {
    busy.value = ""
  }
}
async function setEnabled(value: boolean) {
  if (!selectedRule.value || busy.value || (value && !published.value)) return
  const context = editContext()
  busy.value = "enable"
  error.value = ""
  notice.value = ""
  try {
    await request(`/rules/${encodeURIComponent(selectedRule.value.id)}/enable`, "POST", { enabled: value })
    if (context !== editContext()) { emit('refresh'); return }
    enabled.value = value
    notice.value = value ? "自动运行已启用。" : "自动运行已停用。"
    emit("refresh")
  } catch (cause) {
    if (context === editContext()) error.value = cause instanceof Error ? cause.message : "启停失败"
  } finally {
    busy.value = ""
  }
}
</script>

<style scoped>
.tool-line { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-top: 12px; color: var(--faint); font-size: 12px; }
.tool-line uni-button { margin: 0; min-height: 25px; padding: 2px 8px; font-size: 11px; }
.strategy { display: grid; gap: 12px; min-width: 0; }
.strategy-head, .rule-summary, .publish-bar, .editor-title { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.step-head b { font-size: 16px; }
.step-head span, .note, .src { color: var(--faint); font-size: 12px; }
.note { margin: 5px 0 0; line-height: 1.65; }
.panel-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.panel-head h3 { margin: 0; font-size: 17px; }
.section-title { margin: 14px 0 6px; color: var(--faint); font-size: 12px; letter-spacing: .06em; }
.acc-grid { display: grid; grid-template-columns: minmax(0,1fr) minmax(0,1fr); gap: 10px; margin-top: 12px; align-items: start; }
.acc-card { min-width: 0; padding: 11px 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 10px; background: linear-gradient(135deg,rgba(255,255,255,.34),rgba(255,255,255,.06)),rgba(255,255,255,.42); box-shadow: 0 8px 22px rgba(45,83,110,.08),inset 0 1px 0 rgba(255,255,255,.75); }
.card-title { margin: 0 0 8px; color: var(--faint); font-size: 11px; letter-spacing: .06em; }
.aggregate-disclosure { border-top: 1px solid var(--line); padding-top: 12px; }
.aggregate-disclosure summary { cursor: pointer; font-weight: 600; }
.aggregate-disclosure[open] summary { margin-bottom: 12px; }
.rule-tabs { display: flex; gap: 6px; overflow-x: auto; padding-bottom: 3px; }
.rule-tabs button { flex: none; border: 1px solid var(--glass-border); border-radius: 999px; padding: 6px 13px; background: rgba(255,255,255,.48); color: var(--muted); }
.rule-tabs button.active { border-color: var(--accent); color: var(--accent); background: rgba(255,255,255,.82); }
.group-match { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; color: var(--muted); font-size: 12px; }
.condition-logic { margin-bottom: 5px; color: var(--accent); font-size: 11px; }
.sequence-index { display: inline-grid; place-items: center; width: 22px; height: 22px; border-radius: 50%; background: rgba(35,112,95,.1); color: var(--accent); font-size: 11px; }
.rule-summary { flex-wrap: wrap; }
.rule-summary strong { font-size: 17px; }
.rule-table { min-width: 0; }
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
.condition-editor select, .condition-editor input, .action-editor select, .action-editor input, .group-match select,
.simulation-grid select, .simulation-grid input, .timing-inputs input { min-height: 40px; border: 1px solid var(--line); border-radius: 8px; padding: 6px 9px; background: rgba(255,255,255,.7); }
.condition-editor input[type="checkbox"] { min-height: auto; }
.weekdays { display: flex; gap: 5px; flex-wrap: wrap; }
.weekdays label { display: flex; align-items: center; gap: 2px; color: var(--muted); font-size: 12px; }
.timing-inputs { display: flex; gap: 12px; flex-wrap: wrap; }
.timing-inputs label, .simulation-grid label, .action-editor label { display: flex; align-items: center; gap: 7px; color: var(--muted); font-size: 12px; }
.action-editor label input { width: 120px; }
.simulation { padding: 0; }
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
  .acc-grid { grid-template-columns: 1fr; }
  .publish-bar .actions { justify-content: flex-start; }
  .condition-editor, .action-editor { align-items: stretch; }
  .rule-table { min-width: 900px; }
}
</style>
