<template>
  <section class="card glass preflight-panel">
    <view class="preflight-head">
      <view class="step-head"><b>预检与门槛</b><span>展开查看条件、对象与下一步</span></view>
      <view class="preflight-actions">
        <button class="ghost" aria-controls="delivery-gate-details" :aria-expanded="expanded" @click="expanded = !expanded">{{ expanded ? "收起门槛" : "展开门槛" }}</button>
        <button class="ghost" :disabled="!canRun || busy" @click="$emit('run')">{{ busy ? "预检中…" : "运行预检" }}</button>
      </view>
    </view>
    <view class="snapshot-line">
      <span>实时门槛{{ report?.generated_at ? ` · ${clock(report.generated_at)}` : "" }}</span>
      <span>最近预检：{{ latest ? `${statusText(latest.status)} · ${clock(latest.generated_at)} · ${latest.operator || "—"}` : "尚未运行" }}</span>
    </view>
    <view v-if="error" class="error">{{ error }}</view>
    <view v-if="report" class="summary" :class="{ expanded }">
      <span class="chip" :class="report.status">总状态：{{ statusText(report.status) }}</span>
      <span class="chip ok">通过 {{ report.summary?.pass || 0 }}</span>
      <span class="chip warn">注意 {{ report.summary?.warn || 0 }}</span>
      <span class="chip fail">阻断 {{ report.summary?.fail || 0 }}</span>
    </view>
    <view v-else-if="!error" class="empty">正在读取实时门槛…</view>
    <view v-if="expanded" id="delivery-gate-details">
      <template v-if="report">
        <view v-if="report.gates?.length" class="gates">
          <view v-for="gate in report.gates" :key="gate.key" class="gate" :class="gate.status">
            <view class="gate-title"><b>{{ gate.title }}</b><span class="chip" :class="gate.status">{{ statusText(gate.status) }}</span></view>
            <view class="gate-detail">{{ gate.detail || "—" }}</view>
            <view v-if="gate.object" class="gate-object">对象：{{ gate.object }}</view>
            <view v-if="gate.next_step" class="next-step">下一步：{{ gate.next_step }}</view>
            <button v-if="gate.repair_node && gate.repair_step" class="ghost" @click="$emit('repair', gate)">去修复 →</button>
            <view v-if="gate.informational" class="informational">仅提示，不计入总状态</view>
          </view>
        </view>
        <view v-else class="empty">后端没有返回门槛明细。</view>
      </template>
      <p class="note">运行预检会保存可追溯快照并写审计；它不改变工程配置，不部署，也不启动控制。</p>
    </view>
  </section>
</template>

<script setup lang="ts">
import { ref } from "vue"
import { clock, statusText } from "./contracts"

type Gate = { key: string; title: string; status: string; detail: string; object?: string; next_step?: string; repair_node?: string; repair_step?: string; informational?: boolean }
type Report = { id?: string; generated_at?: string; operator?: string; status: string; summary: Record<string, number>; gates: Gate[] }
defineProps<{ report: Report | null; latest: Report | null; error: string; busy: boolean; canRun: boolean }>()
defineEmits<{ (event: "run"): void; (event: "repair", gate: Gate): void }>()
const expanded = ref(false)
</script>

<style scoped>
.preflight-panel { padding: 12px 16px; }
.preflight-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 8px; }
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.step-head b { font-size: 14px; }
.step-head span, .snapshot-line { color: var(--faint); font-size: 12px; }
.preflight-actions { display: flex; flex: none; gap: 7px; }
.preflight-actions .ghost { min-height: 32px; padding: 4px 10px; font-size: 12px; }
.snapshot-line { display: flex; justify-content: space-between; flex-wrap: wrap; gap: 6px 16px; margin-bottom: 9px; }
.summary { display: flex; flex-wrap: wrap; gap: 7px; }
.summary.expanded { margin-bottom: 12px; }
.chip.fail { color: var(--danger); background: rgba(253,235,232,.8); }
.gates { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 9px; }
.gate { min-width: 0; padding: 12px; border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.38); }
.gate.fail { border-color: rgba(178,58,50,.32); background: rgba(253,241,238,.52); }
.gate.warn { border-color: rgba(164,99,27,.25); }
.gate-title { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.gate-title b { font-size: 13px; }
.gate-detail { margin-top: 7px; font-size: 12.5px; line-height: 1.6; }
.gate-object, .next-step, .informational { margin-top: 4px; color: var(--faint); font-size: 11.5px; line-height: 1.6; overflow-wrap: anywhere; }
.next-step { color: var(--muted); }
.note { margin: 12px 0 0; color: var(--faint); font-size: 12px; line-height: 1.7; }
@media (max-width: 900px) { .gates { grid-template-columns: 1fr; } }
@media (max-width: 560px) { .preflight-head { align-items: flex-start; flex-direction: column; } }
</style>
