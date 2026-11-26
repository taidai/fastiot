<template>
  <section class="rebind-panel">
    <view class="card-title">现场连接重绑 <span>仅本次导入的通道</span></view>
    <view v-if="error" class="error" role="alert">{{ error }}</view>
    <view v-if="notice" class="notice" role="status">{{ notice }}</view>
    <view v-if="loading" class="empty">正在读取导入通道…</view>
    <view v-else-if="!scopedChannels.length" class="empty">本次导入没有可重绑的通道；不会改用其他 Site 的通道。</view>
    <template v-else>
      <view class="channel-list">
        <button v-for="channel in scopedChannels" :key="channel.id" class="channel" :class="{ active: selectedID === channel.id }" @click="selectChannel(channel.id)">
          <b>{{ channelName(channel.id) }}</b><span>{{ (channel.protocol || "TCP").toUpperCase() }} · {{ channel.address || "地址未绑定" }}</span>
        </button>
      </view>
      <button v-if="!form" class="primary" :disabled="!canConfigure" @click="openForm">编辑现场连接</button>
      <view v-if="form" class="editor">
        <view class="form-grid">
          <label>通道名称<input v-model="form.description" /></label>
          <label>协议<select v-model="form.protocol"><option value="TCP">Modbus TCP</option><option value="RTU">Modbus RTU</option></select></label>
          <label v-if="form.protocol === 'TCP'">TCP 目标地址<input v-model="form.uri" placeholder="127.0.0.1:1502" /></label>
          <template v-else>
            <label>串口<input v-model="form.port" /></label>
            <label>波特率<input v-model="form.baud" /></label>
            <label>数据位<select v-model.number="form.data_bits"><option :value="5">5</option><option :value="6">6</option><option :value="7">7</option><option :value="8">8</option></select></label>
            <label>校验位<select v-model="form.parity"><option value="N">无</option><option value="E">偶校验</option><option value="O">奇校验</option></select></label>
            <label>停止位<select v-model.number="form.stop_bits"><option :value="1">1</option><option :value="2">2</option></select></label>
          </template>
          <label>轮询周期（ms）<input v-model.number="form.poll_period" type="number" min="100" /></label>
          <label>超时（ms）<input v-model.number="form.timeout" type="number" min="100" /></label>
        </view>
        <view class="actions"><button class="primary" :disabled="busy || !canConfigure" @click="save">{{ busy ? "保存中…" : "保存现场连接" }}</button><button class="ghost" :disabled="busy" @click="form = null">取消</button></view>
      </view>
    </template>
    <p v-if="!canConfigure" class="note">当前账号没有工程配置权限，不能保存重绑。</p>
    <p class="note">通道列表和保存目标都按本次导入节点过滤；既有 Site 的通道不会成为回退选择。Profile 与点映射仍按包内恢复结果核对。</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf, point } from "../../types/api"
import type { Snapshot } from "../../types/api"
import { canAction, deliveryError } from "./contracts"

type Channel = { id: string; node_id?: string; protocol?: string; address?: string }
type ChannelForm = {
  description: string; protocol: "TCP" | "RTU"; uri: string; port: string; baud: string
  data_bits: number; parity: string; stop_bits: number; poll_period: number; timeout: number; disabled: boolean
}
const props = defineProps<{ snapshot: Snapshot | null; nodeIds: string[] }>()
const emit = defineEmits<{ (event: "saved", channelID: string): void }>()
const channels = ref<Channel[]>([])
const selectedID = ref("")
const form = ref<ChannelForm | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref("")
const notice = ref("")
const scope = computed(() => new Set(props.nodeIds))
const scopedChannels = computed(() => channels.value.filter(channel => scope.value.has(channel.node_id || channel.id)))
const canConfigure = computed(() => canAction(props.snapshot, "configure"))

watch(() => props.nodeIds.join(","), () => { void load() }, { immediate: true })
async function load() {
  loading.value = true
  error.value = ""
  try {
    const result = await request<{ channels: Channel[] }>("/ems/channels")
    channels.value = result.channels || []
    if (!scopedChannels.value.some(channel => channel.id === selectedID.value)) {
      selectedID.value = scopedChannels.value[0]?.id || ""
      form.value = null
    }
  } catch (cause) {
    channels.value = []
    error.value = deliveryError(cause)
  } finally { loading.value = false }
}
function channelName(id: string) {
  const node = props.snapshot?.nodes.find(item => item.id === id)
  return node ? nameOf(node) : id
}
function selectChannel(id: string) {
  selectedID.value = id
  form.value = null
  notice.value = ""
  error.value = ""
}
function openForm() {
  const channel = scopedChannels.value.find(item => item.id === selectedID.value)
  if (!channel) return
  const node = props.snapshot?.nodes.find(item => item.id === channel.id)
  form.value = {
    description: node ? nameOf(node) : channel.id,
    protocol: (node ? point(node, "protocol")?.text : channel.protocol)?.toUpperCase() === "RTU" ? "RTU" : "TCP",
    uri: node ? point(node, "uri")?.text || channel.address || "" : channel.address || "",
    port: node ? point(node, "port")?.text || "COM1" : "COM1",
    baud: node ? point(node, "baud")?.text || String(point(node, "baud")?.value || 9600) : "9600",
    data_bits: node ? point(node, "dataBits")?.value || 8 : 8,
    parity: node ? point(node, "parity")?.text || "N" : "N",
    stop_bits: node ? point(node, "stopBits")?.value || 1 : 1,
    poll_period: node ? point(node, "pollPeriod")?.value || 1000 : 1000,
    timeout: node ? point(node, "timeout")?.value || 1000 : 1000,
    disabled: node ? (point(node, "disabled")?.value || 0) !== 0 : false,
  }
  notice.value = ""
  error.value = ""
}
async function save() {
  const current = form.value
  if (!current || !selectedID.value || !scope.value.has(selectedID.value)) { error.value = "保存目标不在本次导入范围内"; return }
  if (!current.description.trim()) { error.value = "通道名称不能为空"; return }
  if (current.protocol === "TCP" && !current.uri.trim()) { error.value = "TCP 目标地址不能为空"; return }
  if (current.protocol === "RTU" && !current.port.trim()) { error.value = "RTU 串口不能为空"; return }
  busy.value = true
  error.value = ""
  notice.value = ""
  try {
    await request(`/modbus/${encodeURIComponent(selectedID.value)}`, "PUT", { ...current, description: current.description.trim() })
    form.value = null
    notice.value = "现场连接已保存"
    await load()
    emit("saved", selectedID.value)
  } catch (cause) { error.value = deliveryError(cause) } finally { busy.value = false }
}
</script>

<style scoped>
.rebind-panel { display: grid; gap: 10px; min-width: 0; padding: 12px; border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.28); }
.card-title { font-size: 12px; font-weight: 600; }
.card-title span { margin-left: 6px; color: var(--faint); font-weight: 400; }
.channel-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
.channel { display: grid; gap: 4px; min-width: 0; padding: 9px 10px; border: 1px solid var(--line); border-radius: 9px; background: rgba(255,255,255,.45); text-align: left; }
.channel.active { border-color: var(--accent); }
.channel b, .channel span { overflow-wrap: anywhere; }
.channel b { font-size: 12.5px; }
.channel span, .note { color: var(--faint); font-size: 11.5px; }
.editor { padding: 12px; border: 1px solid var(--line); border-radius: 10px; background: rgba(255,255,255,.32); }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.form-grid label { display: grid; gap: 5px; min-width: 0; color: var(--muted); font-size: 12px; }
.form-grid input, .form-grid select { width: 100%; min-width: 0; min-height: 40px; padding: 7px 9px; border: 1px solid var(--line); border-radius: 8px; background: rgba(255,255,255,.7); }
.actions { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 12px; }
.notice { padding: 8px 10px; border-radius: 8px; background: rgba(230,243,238,.8); color: var(--mint); font-size: 12px; }
.note { margin: 0; line-height: 1.7; }
@media (max-width: 560px) { .channel-list, .form-grid { grid-template-columns: 1fr; } }
</style>
