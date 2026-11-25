<template>
  <view class="batch-access">
    <view class="panel-head"><strong>批量接入 · 设备占位</strong><span>已选 {{ checked.size }} 台未绑定设备；批量只保存映射，验证仍逐台进行。</span><span class="spacer"></span><button class="ghost" @click="emit('close')">退出批量</button></view>
    <div class="table-wrap"><table><thead><tr><th></th><th>设备</th><th>状态</th></tr></thead><tbody>
      <tr v-for="device in devices" :key="device.id"><td><checkbox :checked="checked.has(device.id)" :disabled="device.bound || readOnly || busy !== ''" @click="toggle(device.id)" /></td><td>{{ device.name }}<small>{{ device.id }}</small></td><td><span class="chip" :class="device.bound ? 'ok' : 'warn'">{{ device.bound ? "已绑定" : "未绑定" }}</span></td></tr>
      <tr v-if="!devices.length"><td colspan="3" class="empty">当前没有设备占位；请先在“搭台账”创建设备。</td></tr>
    </tbody></table></div>
    <view class="batch-form">
      <label>通道<select v-model="channelID" :disabled="readOnly || busy !== ''" @change="invalidate"><option v-for="channel in channels" :key="channel.id" :value="channel.id">{{ channelName(channel.id) }}</option></select></label>
      <label>Profile<select v-model="profileID" :disabled="readOnly || busy !== ''" @change="invalidate"><option v-for="profile in usableProfiles" :key="profile.id" :value="profile.id">{{ profile.vendor }} {{ profile.model }}</option></select></label>
      <label>起始站号<input v-model.number="startStation" type="number" min="1" max="247" :disabled="readOnly || busy !== ''" @input="invalidate"></label>
      <button class="ghost" :disabled="readOnly || !checked.size || !channelID || !profileID || busy !== ''" @click="makeDraft">生成批量草稿</button>
    </view>
    <label class="csv-label">CSV 草稿（设备,型号,通道,站号）<textarea v-model="csv" rows="6" :disabled="readOnly || busy !== ''" @input="invalidate"></textarea></label>
    <view class="actions"><button class="ghost" :disabled="readOnly || !csv.trim() || busy !== ''" @click="preview">{{ busy === 'preview' ? '预检中…' : 'CSV 预检' }}</button><button class="primary" :disabled="readOnly || previewedCSV !== csv || !rows.some(row => row.valid) || busy !== ''" @click="apply">{{ busy === 'apply' ? '套用中…' : '批量套用可导入项' }}</button></view>
    <p v-if="error" class="error" role="alert">{{ error }}</p><p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <div v-if="rows.length" class="table-wrap"><table><thead><tr><th>行</th><th>设备</th><th>通道 / 站号</th><th>点位</th><th>结果</th></tr></thead><tbody>
      <tr v-for="row in rows" :key="row.line"><td>{{ row.line }}</td><td>{{ deviceName(row.device_id) || row.device }}</td><td>{{ channelName(row.channel_id) || row.channel }} / {{ row.station || '—' }}</td><td>{{ row.status === 'mapped' ? `${row.mapped} 已映射${row.missing.length ? `，缺 ${row.missing.length}` : ''}` : `${row.created_points} 个待创建` }}</td><td><span class="chip" :class="row.status === 'mapped' ? 'ok' : row.valid ? '' : 'warn'">{{ statusText(row) }}</span><small v-if="row.errors.length">{{ row.errors.join('；') }}</small></td></tr>
    </tbody></table></div>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf } from "../../types/api"
import type { Snapshot } from "../../types/api"
import { batchCSV } from "./access"
import type { BatchAccessResponse, BatchBindingRow, EmsChannel, Profile } from "./access"
import { boundBusinessIDs } from "./design"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; channels: EmsChannel[]; profiles: Profile[]; selectedNodeIds?: string[]; readOnly?: boolean }>(), { selectedNodeIds: () => [], readOnly: false })
const emit = defineEmits<{ (event: "refresh"): void; (event: "close"): void }>()
const checked = ref(new Set<string>())
const channelID = ref("")
const profileID = ref("")
const startStation = ref(1)
const csv = ref("")
const rows = ref<BatchBindingRow[]>([])
const previewedCSV = ref("")
const busy = ref("")
const error = ref("")
const notice = ref("")
const boundIDs = computed(() => boundBusinessIDs(props.snapshot?.nodes || []))
const devices = computed(() => (props.snapshot?.nodes || []).filter(node => node.type === "device" && node.parent !== "root").map(node => ({ id: node.id, name: nameOf(node), bound: boundIDs.value.has(node.id) })))
const usableProfiles = computed(() => props.profiles.filter(profile => (profile.point_specs?.length || 0) > 0))
watch(() => props.selectedNodeIds, ids => {
  checked.value = new Set(ids.filter(id => devices.value.some(device => device.id === id && !device.bound)))
}, { immediate: true })
watch([() => props.channels, () => props.profiles], () => {
  if (!channelID.value) channelID.value = props.channels[0]?.id || ""
  if (!profileID.value) profileID.value = usableProfiles.value[0]?.id || ""
}, { immediate: true, deep: true })
function toggle(id: string) {
  const next = new Set(checked.value)
  if (next.has(id)) next.delete(id); else next.add(id)
  checked.value = next
  invalidate()
}
function invalidate() { rows.value = []; previewedCSV.value = ""; notice.value = "" }
function makeDraft() {
  if (!Number.isInteger(startStation.value) || startStation.value < 1 || startStation.value > 247) { error.value = "起始站号必须是 1–247 的整数"; return }
  const selected = devices.value.filter(device => checked.value.has(device.id) && !device.bound)
  csv.value = batchCSV(selected, profileID.value, channelID.value, startStation.value)
  invalidate()
  error.value = ""
}
function channelName(id?: string) { return id ? nameOf(props.snapshot?.nodes.find(node => node.id === id) || { id, type: "", parent: "", points: [] }) : "" }
function deviceName(id?: string) { return id ? devices.value.find(device => device.id === id)?.name || id : "" }
function statusText(row: BatchBindingRow) { return row.status === "mapped" ? "映射已保存，待验证" : row.status === "ready" ? "可导入" : row.status === "blocked" ? "预检阻断" : "套用失败" }
async function run(dryRun: boolean) {
  busy.value = dryRun ? "preview" : "apply"
  error.value = ""
  notice.value = ""
  try {
    const result = await request<BatchAccessResponse>("/access/batch", "POST", { csv: csv.value, dry_run: dryRun })
    rows.value = result.rows || []
    if (dryRun) previewedCSV.value = csv.value
    else {
      previewedCSV.value = ""
      notice.value = `已保存 ${result.applied} 台设备映射；未执行验证，请逐台完成 L1/L2`
      for (const row of rows.value) if (row.status === "mapped" && row.device_id) checked.value.delete(row.device_id)
      checked.value = new Set(checked.value)
      emit("refresh")
    }
  } catch (cause) { error.value = cause instanceof Error ? cause.message : "批量操作失败" } finally { busy.value = "" }
}
async function preview() { await run(true) }
async function apply() { await run(false) }
</script>

<style scoped>
.batch-access { display: flex; flex-direction: column; gap: 10px; padding: 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 12px; background: rgba(255,255,255,.28); }
.panel-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }.panel-head span { color: var(--faint); font-size: 12px; }.spacer { flex: 1; }
td small { display: block; color: var(--faint); font-size: 10px; margin-top: 2px; }
.batch-form { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)); gap: 8px; align-items: end; }.batch-form label, .csv-label { display: flex; flex-direction: column; gap: 4px; color: var(--muted); font-size: 11px; }
.batch-form select, .batch-form input, textarea { width: 100%; border: 1px solid var(--glass-border); border-radius: 8px; padding: 7px 9px; background: rgba(255,255,255,.62); }
.actions { display: flex; gap: 8px; flex-wrap: wrap; }.empty { color: var(--faint); }
@media(max-width:900px){.batch-form{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
