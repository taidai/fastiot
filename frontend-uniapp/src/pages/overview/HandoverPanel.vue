<template>
  <section class="handover-section">
    <view class="card-title summary-title">交接</view>
    <view v-if="record" class="handover-record">
      <view class="handover-note">{{ record.note }}</view>
      <view class="handover-meta">{{ record.actor || '未记录人员' }} · {{ formatTime(record.updated_at) }}</view>
    </view>
    <view v-else-if="!loading && !loadError" class="side-empty">—</view>
    <view v-if="loading" class="side-empty">正在读取交接记录…</view>
    <view v-if="loadError" class="error" role="alert">{{ loadError }}</view>
    <textarea v-model="note" class="handover-input" maxlength="4000" rows="3" aria-label="交接留言" placeholder="填写本班需要下一班继续关注的事项" :disabled="saving" />
    <view class="handover-actions">
      <button class="save-handover" :disabled="saving || !note.trim()" @click="save">{{ saving ? '保存中…' : '保存交接' }}</button>
      <text>保存后写入服务端</text>
    </view>
    <view v-if="saveError" class="error" role="alert">{{ saveError }}</view>
    <view v-else-if="saved" class="saved-message" role="status">交接已保存。</view>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from "vue"
import { request } from "../../api/client"
import { projectContext } from "../../stores/projects"
import { session } from "../../stores/session"

type Handover = { layer_id: string; note: string; actor?: string; updated_at: string }
const props = defineProps<{ layerId: string }>()
const record = ref<Handover | null>(null)
const note = ref("")
const loading = ref(false)
const saving = ref(false)
const loadError = ref("")
const saveError = ref("")
const saved = ref(false)
let serial = 0
let saveSerial = 0

function errorText(error: unknown) { return error instanceof Error ? error.message : String(error) }
function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false })
}
async function load() {
  const current = ++serial
  saveSerial += 1
  saving.value = false
  loading.value = false
  loadError.value = ""
  saveError.value = ""
  saved.value = false
  record.value = null
  note.value = ""
  if (!props.layerId) return
  loading.value = true
  try {
    const result = await request<{ handover: Handover | null }>(`/overview/handover?layer=${encodeURIComponent(props.layerId)}`)
    if (current === serial) record.value = result.handover
  } catch (error) {
    if (current === serial) loadError.value = `交接记录读取失败：${errorText(error)}`
  } finally {
    if (current === serial) loading.value = false
  }
}
async function save() {
  if (saving.value || !note.value.trim() || !props.layerId) return
  const layerId = props.layerId
  const current = ++saveSerial
  // A GET started before this write cannot replace its newer server result.
  serial++
  loading.value = false
  loadError.value = ""
  saving.value = true
  saveError.value = ""
  saved.value = false
  try {
    const result = await request<{ handover: Handover }>(`/overview/handover?layer=${encodeURIComponent(layerId)}`, "PUT", { note: note.value })
    // A save belongs to the layer it was made in. If the operator moved to
    // another layer while it was in flight, its response must not overwrite
    // the layer now on screen.
    if (current !== saveSerial || layerId !== props.layerId) return
    record.value = result.handover
    note.value = ""
    saved.value = true
  } catch (error) {
    if (current === saveSerial && layerId === props.layerId) saveError.value = `交接保存失败：${errorText(error)}`
  } finally {
    if (current === saveSerial) saving.value = false
  }
}
watch(() => [props.layerId, projectContext.generation, session.token], () => { void load() }, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { serial++; saveSerial++ })
</script>

<style scoped>
.handover-section { margin-top: auto; padding-top: 10px; border-top: 1px solid var(--line); }
.summary-title { margin-top: 0; }
.handover-note { white-space: pre-wrap; overflow-wrap: anywhere; font-size: 12px; line-height: 1.7; }
.handover-meta { margin-top: 4px; color: var(--muted); font-size: 10px; }
.side-empty { padding: 6px 0; color: var(--muted); }
.handover-input { width: 100%; min-height: 66px; margin-top: 8px; padding: 7px 9px; resize: vertical; border: 1px solid var(--line); border-radius: 9px; background: var(--control-bg, rgba(255,255,255,.35)); color: var(--ink); font-size: 11px; line-height: 1.6; }
.handover-actions { display: flex; align-items: center; gap: 8px; margin-top: 6px; }
.handover-actions text { color: var(--muted); font-size: 9px; }
.save-handover { min-height: 30px; padding: 4px 10px; border: 1px solid var(--glass-border); border-radius: 8px; background: var(--control-bg, rgba(255,255,255,.45)); color: var(--ink); font-size: 10.5px; }
.saved-message { margin-top: 6px; color: var(--accent); font-size: 10px; }
.error { margin-top: 6px; padding: 7px 8px; font-size: 10.5px; }
</style>
