<template>
  <view class="access-step">
    <view class="step-head">
      <b>第 3 步 · 接设备</b><span>型号 → 通道 → 点位 → 验证；数据来自当前后端。</span>
      <span class="spacer"></span>
      <button class="ghost" :disabled="directoryLoading" @click="reload">{{ directoryLoading ? "读取中…" : "刷新" }}</button>
      <button class="ghost" @click="ledgerOpen = !ledgerOpen">设备台账 {{ devices.length }}</button>
    </view>

    <p v-if="directoryError" class="error" role="alert">{{ directoryError }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <p v-if="!canConfigure" class="note">当前账号可查看接入状态，但没有配置权限；写操作已停用。</p>

    <view v-if="ledgerOpen" class="ledger-panel">
      <view class="panel-head">
        <strong>设备台账</strong><span class="src">一台物理站号设备一行；未验证不等于未绑定。</span>
        <span class="spacer"></span>
        <button class="ghost" :disabled="!channels.length || busy !== ''" @click="startNewUnit">接入新站号</button>
      </view>
      <div v-if="devices.length" class="table-wrap">
        <table class="ledger-table">
          <thead><tr><th>设备</th><th>通道 / 站号</th><th>Profile</th><th>点位</th><th>通信</th><th>验证</th><th></th></tr></thead>
          <tbody><tr v-for="device in devices" :key="deviceKey(device)" :class="{ selected: deviceKey(device) === selectedKey }">
            <td><strong>{{ device.business_node_name || device.name }}</strong><small>{{ device.name }}</small></td>
            <td>{{ device.channel_name || device.channel_id }} / {{ device.server_id }}</td>
            <td>{{ device.profile_id ? `${device.vendor || ""} ${device.model || device.profile_id}` : "未绑定" }}</td>
            <td>{{ device.point_count }}</td>
            <td><span class="chip" :class="healthClass(device.health)">{{ healthText(device.health) }}</span></td>
            <td><span class="chip" :class="device.verified ? 'ok' : 'warn'">{{ device.verified ? "已验证" : "未验证" }}</span></td>
            <td><button class="ghost" :disabled="busy !== ''" @click="openDevice(device)">打开接入 →</button></td>
          </tr></tbody>
        </table>
      </div>
      <view v-else-if="!directoryLoading && !ledgerLoadFailed" class="empty">当前没有设备台账记录。没有数据不以演示设备替代；可从已有通道读取一个站号开始接入。</view>
      <view v-else-if="ledgerLoadFailed" class="empty">设备台账暂不可用，请根据上方错误重试；当前状态不能视为无设备。</view>
    </view>

    <view class="pipeline" aria-label="设备接入步骤">
      <button v-for="item in stages" :key="item.key" :disabled="busy !== ''" :class="{ active: stage === item.key }" @click="stage = item.key">
        {{ item.key === 'points' ? `核对点位 ${mappedCount}/${profilePoints.length}` : item.label }}
      </button>
    </view>

    <view v-if="modelLoading" class="empty">正在读取 {{ selectedChannelID }} / 站号 {{ selectedServerID }} 的设备模型…</view>

    <template v-else-if="stage === 'profile'">
      <view class="acc-grid">
        <view class="acc-card">
          <view class="card-title">1 · 选型号 <span>{{ profileID ? '待保存选择可预览差异' : '尚未选择' }}</span></view>
          <label>设备 Profile
            <select :value="profileID" :disabled="!canConfigure || busy !== '' || !selectedChannelID" @change="selectProfile(inputValue($event))">
              <option value="">未选择</option>
              <option v-for="profile in availableProfiles" :key="profile.id" :value="profile.id">
                {{ profile.vendor }} {{ profile.model }}{{ profile.custom ? "（自定义）" : "" }}{{ profile.verified ? "" : "（未现场验证）" }}
              </option>
            </select>
          </label>
          <view v-if="selectedProfile" class="profile-summary">
            <view class="row"><span>声明点位</span><span>{{ profilePoints.length }} 个</span></view>
            <view class="row"><span>命名命令</span><span>{{ selectedProfile.command_specs?.length || 0 }} 个</span></view>
            <view class="row"><span>Profile 状态</span><span class="chip" :class="selectedProfile.verified ? 'ok' : 'warn'">{{ selectedProfile.verified ? "已现场验证" : "未现场验证" }}</span></view>
            <p v-if="selectedProfile.verification" class="note">依据：{{ selectedProfile.verification }}</p>
            <p v-for="note in selectedProfile.notes || []" :key="note" class="note">{{ note }}</p>
          </view>
          <view v-if="model?.profile_errors?.length" class="error">自定义 Profile 未加载：{{ model.profile_errors.join("；") }}</view>
          <view class="actions">
            <button class="ghost" :disabled="!canConfigure || !profileID || !selectedChannelID || busy !== ''" @click="previewProfile">{{ busy === 'preview' ? '预览中…' : '预览 Profile 匹配' }}</button>
            <button class="primary" :disabled="!canConfigure || !applyPreview || !selectedChannelID || busy !== ''" @click="applyProfile">{{ busy === 'apply' ? '应用中…' : '应用 Profile' }}</button>
          </view>
          <p class="note">先预览同名点匹配与语义差异，再应用。应用只保存 Profile 与映射，不代表验证通过。</p>
        </view>
        <view class="acc-card">
          <view class="card-title">当前对象</view>
          <view class="row"><span>通道 / 站号</span><span>{{ selectedChannelID || "—" }} / {{ selectedServerID || "—" }}</span></view>
          <view class="row"><span>设备名称</span><span>{{ config.name || `站号 ${selectedServerID}` }}</span></view>
          <view class="row"><span>业务节点</span><span>{{ selectedDevice?.business_node_name || config.business_node_id || "未绑定" }}</span></view>
          <view class="row"><span>已保存 Profile</span><span>{{ model?.model?.profile_id || "—" }}</span></view>
          <view v-if="config.verification_required && !config.verified" class="warning-box">Profile 已更换，控制命令等待重新验证；保存前请核对差异。</view>
        </view>
      </view>
      <view v-if="applyPreview" class="preview-panel">
        <view class="panel-head"><strong>匹配差异</strong><span class="src">完全 {{ applyPreview.exact }} · 可换算 {{ applyPreview.scaled }} · 不等价 {{ applyPreview.mismatched }} · 缺失 {{ applyPreview.missing.length }}</span></view>
        <div class="table-wrap"><table><thead><tr><th>Profile 变量</th><th>物理点</th><th>结论</th><th>差异 / 原因</th></tr></thead>
          <tbody><tr v-for="point in applyPreview.points" :key="point.name"><td>{{ point.name }}</td><td>{{ pointName(point.point_id) }}</td><td><span class="chip" :class="verdictClass(point.verdict)">{{ verdictText(point.verdict) }}</span></td><td>{{ point.reason || "—" }}</td></tr></tbody>
        </table></div>
        <view v-if="applyPreview.blocked.length" class="warning-box"><strong>应用后仍不可用的控制：</strong><view v-for="reason in applyPreview.blocked" :key="reason">{{ reason }}</view></view>
      </view>
    </template>

    <template v-else-if="stage === 'channel'">
      <view class="acc-grid">
        <view class="acc-card">
          <view class="card-title">2 · 选通道 <button class="link" :disabled="!canConfigure" @click="openChannelForm(false)">＋ 新增通道</button></view>
          <button v-for="channel in channels" :key="channel.id" class="pick" :disabled="busy !== ''" :class="{ active: channel.id === selectedChannelID }" @click="selectChannel(channel.id)">
            <strong>{{ channelName(channel.id) }}</strong>
            <small>{{ (channel.protocol || "tcp").toUpperCase() }} · {{ channel.address || "—" }} · {{ channel.devices?.length || 0 }} 台设备 · {{ channel.persisted === false ? "未持久化" : "已持久化" }}</small>
          </button>
          <view v-if="!channels.length && !directoryLoading && !channelLoadFailed" class="empty">还没有通道。请新增真实 Modbus 通道；不会生成演示连接。</view>
          <view v-else-if="channelLoadFailed" class="empty">通道列表暂不可用，请根据上方错误重试；当前状态不能视为无通道。</view>
          <view v-if="selectedChannelID" class="station-form">
            <label>站号（1–247）<input v-model.number="serverInput" type="number" min="1" max="247"></label>
            <button class="ghost" :disabled="busy !== ''" @click="readStation">读取此站号</button>
            <button class="ghost" :disabled="!canConfigure" @click="openChannelForm(true)">编辑当前通道</button>
          </view>
        </view>
        <view class="acc-card">
          <view class="card-title">本站连接</view>
          <view class="row"><span>协议 / 地址</span><span>{{ selectedChannel?.protocol?.toUpperCase() || "—" }} · {{ selectedChannel?.address || "—" }}</span></view>
          <view class="row"><span>轮询周期</span><span>{{ selectedChannel?.period_ms ? `${selectedChannel.period_ms} ms` : "—" }}</span></view>
          <view class="row"><span>当前设备通信</span><span class="chip" :class="healthClass(selectedDevice?.health)">{{ healthText(selectedDevice?.health) }}</span></view>
          <view class="row"><span>成功率 / 平均延迟</span><span>{{ selectedDevice && selectedDevice.success_rate > 0 ? `${selectedDevice.success_rate.toFixed(1)}% / ${Math.round(selectedDevice.avg_latency_ms)} ms` : "—" }}</span></view>
          <p class="note">线路诊断在“验证”段执行，只读，不写设备。运行期累计统计在进程重启后归零。</p>
        </view>
      </view>
      <view v-if="channelForm" class="editor-panel">
        <view class="card-title">{{ channelForm.editing ? "编辑通道" : "新增通道" }} <span>保存后由后端持久化并重建采集</span></view>
        <view class="form-grid">
          <label>通道名称<input v-model="channelForm.description"></label>
          <label>协议<select v-model="channelForm.protocol"><option value="TCP">Modbus TCP</option><option value="RTU">Modbus RTU</option></select></label>
          <label v-if="channelForm.protocol === 'TCP'">目标地址<input v-model="channelForm.uri" placeholder="127.0.0.1:1502"></label>
          <template v-else>
            <label>串口<input v-model="channelForm.port"></label>
            <label>波特率<input v-model="channelForm.baud"></label>
            <label>数据位<select v-model.number="channelForm.data_bits"><option :value="5">5</option><option :value="6">6</option><option :value="7">7</option><option :value="8">8</option></select></label>
            <label>校验位<select v-model="channelForm.parity"><option value="N">N</option><option value="E">E</option><option value="O">O</option></select></label>
            <label>停止位<select v-model.number="channelForm.stop_bits"><option :value="1">1</option><option :value="2">2</option></select></label>
          </template>
          <label>轮询周期（ms）<input v-model.number="channelForm.poll_period" type="number" min="100"></label>
          <label>超时（ms）<input v-model.number="channelForm.timeout" type="number" min="100"></label>
        </view>
        <view class="actions"><button class="primary" :disabled="busy !== ''" @click="saveChannel">{{ busy === 'channel' ? '保存中…' : '保存通道' }}</button><button class="ghost" @click="channelForm = null">取消</button></view>
      </view>
    </template>

    <template v-else-if="stage === 'points'">
      <view class="acc-card wide">
        <view class="card-title">3 · 核对点位 <span>当前站号 {{ serverPoints.length }} 个物理点 · Profile 已映射 {{ mappedCount }}/{{ profilePoints.length }}</span></view>
        <view v-if="!profileID" class="empty">先在“选型号”段选择并应用 Profile。</view>
        <template v-else>
          <div class="table-wrap"><table><thead><tr><th>Profile 变量</th><th>角色</th><th>映射到当前站号点位</th><th>读写</th><th>状态</th></tr></thead>
            <tbody><tr v-for="spec in profilePoints" :key="spec.name">
              <td><strong>{{ spec.name }}</strong><small>{{ spec.unit || "—" }}</small></td>
              <td>{{ roleText(spec.role) }}</td>
              <td><select :value="config.point_map[spec.name] || ''" :disabled="!canConfigure || busy !== ''" @change="updateMapping(spec.name, inputValue($event))">
                <option value="">未映射</option>
                <option v-for="point in serverPoints" :key="point.id" :value="point.id">{{ point.name }} · 地址 {{ point.address }} · {{ point.data_format || point.io_type }}</option>
              </select></td>
              <td>{{ spec.read_only === false ? "可写" : "只读" }}</td>
              <td><span class="chip" :class="config.point_map[spec.name] ? 'ok' : requiredSpecs.has(spec.name) ? 'warn' : ''">{{ config.point_map[spec.name] ? "已映射" : requiredSpecs.has(spec.name) ? "命令必需" : "未映射" }}</span></td>
            </tr></tbody>
          </table></div>
          <view v-if="model?.missing?.length" class="warning-box">后端报告命令所需点未映射：{{ model.missing.join("、") }}</view>
          <view class="actions"><button class="ghost" :disabled="!canConfigure || busy !== ''" @click="autoMatch">按 Profile 名称匹配</button><button class="primary" :disabled="!canConfigure || !selectedChannelID || busy !== ''" @click="saveMapping">{{ busy === 'mapping' ? '保存中…' : '保存点位映射' }}</button><button class="ghost" :disabled="!canConfigure || !selectedChannelID" @click="openPointForm">＋ 新增物理点</button></view>
          <p class="note">自动匹配只修改本地草稿，点击保存后才写后端。命令必需点、回读点或读写方向不满足时，后端会阻断对应命令。</p>
        </template>
      </view>
      <view v-if="pointForm" class="editor-panel">
        <view class="card-title">新增物理点 <span>{{ selectedChannelID }} / 站号 {{ selectedServerID }}</span></view>
        <view class="form-grid">
          <label>点位名称<input v-model="pointForm.description"></label>
          <label>地址<input v-model.number="pointForm.address" type="number" min="0" max="65535"></label>
          <label>类型<select v-model="pointForm.io_type"><option value="modbusHoldingRegister">保持寄存器</option><option value="modbusInputRegister">输入寄存器</option><option value="modbusCoil">线圈</option><option value="modbusDiscreteInput">离散输入</option></select></label>
          <label>数据格式<select v-model="pointForm.data_format"><option value="uint16">uint16</option><option value="int16">int16</option><option value="uint32">uint32</option><option value="int32">int32</option><option value="float32">float32</option></select></label>
          <label>缩放<input v-model.number="pointForm.scale" type="number" step="any"></label>
          <label>偏移<input v-model.number="pointForm.offset" type="number" step="any"></label>
          <label>单位<input v-model="pointForm.units"></label>
          <label class="check-label"><checkbox :checked="pointForm.read_only" @click="pointForm.read_only = !pointForm.read_only" /> 只读（取消勾选表示允许受控写入）</label>
        </view>
        <view class="actions"><button class="primary" :disabled="busy !== ''" @click="savePoint">{{ busy === 'point' ? '保存中…' : '保存物理点' }}</button><button class="ghost" @click="pointForm = null">取消</button></view>
      </view>
    </template>

    <template v-else>
      <view class="verification-grid">
        <view class="acc-card">
          <view class="card-title">4 · 验证 <span>只读证据 + 人工现场记录</span></view>
          <view class="row"><span>当前通信</span><span class="chip" :class="healthClass(selectedDevice?.health)">{{ healthText(selectedDevice?.health) }}</span></view>
          <view class="row"><span>已映射点</span><span>{{ mappedSpecs.length }} 个</span></view>
          <view class="row"><span>当前无值或质量非正常</span><span :class="qualityIssues ? 'warn-text' : 'ok-text'">{{ mappedSpecs.length ? `${qualityIssues} 个` : "—" }}</span></view>
          <view class="actions"><button class="ghost" :disabled="!canConfigure || busy !== '' || !selectedChannelID" @click="runLineTest">{{ busy === 'line-test' ? '测试中…' : '只读连通测试' }}</button></view>
          <view v-if="lineTest" class="line-result">
            <view>通道：{{ lineTest.connected ? "可连接" : "连接失败" }}</view>
            <view>站号 {{ selectedServerID }} {{ lineStation?.responded ? "有响应" : "无响应" }}<template v-if="lineStation"> · {{ lineStation.latency_ms }} ms · {{ lineStation.probe || lineStation.error }}</template></view>
          </view>
          <p class="note">连通测试只读取站号地址 0（保持寄存器失败后尝试输入寄存器），不证明其他点位正确，也不写设备。</p>
          <view class="warning-box">后端没有 L1/L2/L3 分级验证执行与 L3 安全写回读 API；本页不生成这些通过结论。写回读不在此处执行。</view>
        </view>
        <view class="acc-card">
          <view class="card-title">现场验证记录</view>
          <label>验证人<input v-model="config.verifier" :disabled="!canConfigure"></label>
          <label>验证日期<input v-model="config.verified_at" type="date" :disabled="!canConfigure"></label>
          <label>设备序列号<input v-model="config.device_serial" :disabled="!canConfigure"></label>
          <label>验证说明<textarea v-model="config.verification" :disabled="!canConfigure" rows="3" placeholder="地址、缩放、字序、读写方向与真机核对结论"></textarea></label>
          <label class="check-label"><checkbox :checked="config.verified" :disabled="!canConfigure" @click="config.verified = !config.verified" /> 已完成现场逐点核对</label>
          <view class="actions"><button class="primary" :disabled="!canConfigure || !selectedChannelID || busy !== ''" @click="saveVerification">{{ busy === 'verification' ? '保存中…' : '保存验证记录' }}</button></view>
          <p class="note">保存的是人工核对结论，不会触发设备写入。模拟器或当前快照不能替代 M5 真机签收，也不代表项目可交付或已投运。</p>
        </view>
      </view>
      <view class="quality-panel">
        <view class="card-title">已映射点当前值与质量 <span>随工作台实时快照更新；无值显示 —</span></view>
        <div v-if="mappedSpecs.length" class="table-wrap"><table><thead><tr><th>Profile 变量</th><th>物理点</th><th>当前值</th><th>质量</th><th>观测时间</th></tr></thead>
          <tbody><tr v-for="spec in mappedSpecs" :key="spec.name"><td>{{ spec.name }}</td><td>{{ pointName(config.point_map[spec.name]) }}</td><td>{{ sampleText(config.point_map[spec.name]) }}</td><td><span class="chip" :class="sampleQuality(config.point_map[spec.name]) === 'good' ? 'ok' : 'warn'">{{ qualityText(sampleQuality(config.point_map[spec.name])) }}</span></td><td>{{ sampleTime(config.point_map[spec.name]) }}</td></tr></tbody>
        </table></div>
        <view v-else class="empty">还没有已保存的映射点；先完成 Profile 与点位保存。</view>
      </view>
    </template>
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf, point, valueText } from "../../types/api"
import type { Snapshot, StateValue } from "../../types/api"
import { automaticPointMap, canConfigureAccess, prepareModelSave, verificationError } from "./access"
import type { EmsChannel, LedgerDevice, Profile, ProfileApplyResponse, ProfileApplyResult, UnitModel, UnitModelConfig, UnitPoint } from "./access"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; values: StateValue[]; selectedNodeId?: string }>(), { selectedNodeId: "" })
const emit = defineEmits<{
  (event: "refresh"): void
  (event: "status", value: { channels: number; devices: number; bound: number; verified: number }): void
}>()

type Stage = "profile" | "channel" | "points" | "verify"
type ChannelForm = { editing: string; parent: string; description: string; protocol: "TCP" | "RTU"; uri: string; port: string; baud: string; data_bits: number; parity: string; stop_bits: number; poll_period: number; timeout: number; disabled: boolean }
type PointForm = { parent: string; description: string; server_id: number; address: number; io_type: string; data_format: string; read_only: boolean; scale: number; offset: number; units: string; disabled: boolean }
type LineTest = { connected: boolean; latency_ms: number; probed: number; stations: { server_id: number; responded: boolean; latency_ms: number; probe: string; error: string }[] }

const stages: { key: Stage; label: string }[] = [{ key: "profile", label: "选型号" }, { key: "channel", label: "选通道" }, { key: "points", label: "核对点位" }, { key: "verify", label: "验证" }]
const channels = ref<EmsChannel[]>([])
const profiles = ref<Profile[]>([])
const devices = ref<LedgerDevice[]>([])
const directoryLoading = ref(false)
const directoryError = ref("")
const channelLoadFailed = ref(false)
const ledgerLoadFailed = ref(false)
const ledgerOpen = ref(false)
const stage = ref<Stage>("profile")
const selectedChannelID = ref("")
const selectedServerID = ref(1)
const serverInput = ref(1)
const model = ref<UnitModel | null>(null)
const config = ref<UnitModelConfig>({ name: "站号 1", profile_id: "", point_map: {}, verified: false })
const busy = ref("")
const error = ref("")
const notice = ref("")
const previewResponse = ref<ProfileApplyResponse | null>(null)
const channelForm = ref<ChannelForm | null>(null)
const pointForm = ref<PointForm | null>(null)
const lineTest = ref<LineTest | null>(null)
let modelRequest = 0
let previewRequest = 0
let lineTestRequest = 0

const canConfigure = computed(() => canConfigureAccess(props.snapshot))
const selectedChannel = computed(() => channels.value.find(channel => channel.id === selectedChannelID.value) || null)
const selectedDevice = computed(() => devices.value.find(device => device.channel_id === selectedChannelID.value && device.server_id === selectedServerID.value) || null)
const selectedKey = computed(() => selectedChannelID.value ? `${selectedChannelID.value}/${selectedServerID.value}` : "")
const profileID = computed(() => config.value.profile_id)
const availableProfiles = computed(() => model.value?.profiles?.length ? model.value.profiles : profiles.value)
const selectedProfile = computed(() => availableProfiles.value.find(profile => profile.id === profileID.value) || null)
const profilePoints = computed(() => selectedProfile.value?.point_specs || [])
const serverPoints = computed(() => (model.value?.points || []).filter(point => point.server_id === selectedServerID.value))
const mappedCount = computed(() => Object.values(config.value.point_map || {}).filter(Boolean).length)
const mappedSpecs = computed(() => profilePoints.value.filter(spec => config.value.point_map[spec.name]))
const requiredSpecs = computed(() => {
  const result = new Set<string>()
  for (const command of selectedProfile.value?.command_specs || []) {
    if (command.point) result.add(command.point)
    for (const parameter of command.parameters || []) if (parameter.point) result.add(parameter.point)
  }
  return result
})
const missingRequired = computed(() => [...new Set([
  ...(model.value?.missing || []),
  ...[...requiredSpecs.value].filter(name => !config.value.point_map[name]),
])])
const applyPreview = computed<ProfileApplyResult | null>(() => {
  const result = previewResponse.value?.results?.[0]
  return result && result.profile_id === profileID.value && result.server === selectedServerID.value ? result : null
})
const modelLoading = computed(() => busy.value === "model")
const lineStation = computed(() => lineTest.value?.stations.find(station => station.server_id === selectedServerID.value) || null)
const qualityIssues = computed(() => mappedSpecs.value.filter(spec => sampleQuality(config.value.point_map[spec.name]) !== "good").length)
const mappingDirty = computed(() => JSON.stringify(config.value.point_map || {}) !== JSON.stringify(model.value?.model?.point_map || {}))
const profileDirty = computed(() => config.value.profile_id !== (model.value?.model?.profile_id || ""))
const stationDirty = computed(() => serverInput.value !== selectedServerID.value)

function inputValue(event: Event) { return (event.target as HTMLInputElement | HTMLSelectElement).value }
function messageOf(cause: unknown) { return cause instanceof Error ? cause.message : "操作失败" }
function blockStationChange() {
  if (!stationDirty.value) return false
  error.value = "站号已修改，请先读取此站号"
  return true
}
function requireChannel() {
  if (selectedChannelID.value) return false
  error.value = "先选择或新增通道"
  return true
}
function deviceKey(device: LedgerDevice) { return `${device.channel_id}/${device.server_id}` }
function channelName(id: string) { return props.snapshot?.nodes.find(node => node.id === id)?.points.find(item => item.type === "description")?.text || id }
function healthText(value?: string) { return ({ healthy: "在线", degraded: "不稳定", offline: "离线", unknown: "未知" } as Record<string, string>)[value || "unknown"] || value || "未知" }
function healthClass(value?: string) { return value === "healthy" ? "ok" : value === "offline" || value === "degraded" ? "warn" : "" }
function roleText(value?: string) { return ({ telemetry: "遥测", status: "遥信", energy: "电度", setpoint: "定值", protection: "压板" } as Record<string, string>)[value || ""] || value || "—" }
function verdictText(value: string) { return ({ exact: "完全一致", scaled: "缩放/偏移可换算", mismatch: "语义不等价", missing: "缺失" } as Record<string, string>)[value] || value }
function verdictClass(value: string) { return value === "exact" ? "ok" : value === "mismatch" || value === "missing" ? "warn" : "" }
function pointName(id?: string) { const item = model.value?.points.find(point => point.id === id); return item ? `${item.name} · ${item.address}` : "—" }
function sample(pointID: string) { return props.values.find(value => value.point_id === pointID) }
function sampleQuality(pointID: string) { return sample(pointID)?.quality || "uninitialized" }
function qualityText(value: string) { return ({ good: "有效", stale: "过期", offline: "离线", invalid: "无效", uninitialized: "无值" } as Record<string, string>)[value] || value }
function sampleText(pointID: string) { const value = sample(pointID); const item = model.value?.points.find(point => point.id === pointID); return `${valueText(value)}${value && item?.unit ? ` ${item.unit}` : ""}` }
function sampleTime(pointID: string) { const time = sample(pointID)?.source_time; if (!time) return "—"; const date = new Date(time); return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString("zh-CN", { hour12: false }) }

async function loadDirectory() {
  directoryLoading.value = true
  directoryError.value = ""
  const results = await Promise.allSettled([
    request<{ channels: EmsChannel[] }>("/ems/channels"),
    request<{ profiles: Profile[] }>("/ems/profiles"),
    request<{ devices: LedgerDevice[] }>("/device-ledger"),
  ])
  const errors: string[] = []
  if (results[0].status === "fulfilled") {
    channels.value = results[0].value.channels || []
    channelLoadFailed.value = false
  } else {
    channelLoadFailed.value = true
    errors.push(`通道读取失败：${messageOf(results[0].reason)}`)
  }
  if (results[1].status === "fulfilled") profiles.value = results[1].value.profiles || []
  else errors.push(`Profile 读取失败：${messageOf(results[1].reason)}`)
  if (results[2].status === "fulfilled") {
    devices.value = results[2].value.devices || []
    ledgerLoadFailed.value = false
  } else {
    ledgerLoadFailed.value = true
    errors.push(`设备台账读取失败：${messageOf(results[2].reason)}`)
  }
  directoryError.value = errors.join("；")
  directoryLoading.value = false
  emit("status", { channels: channels.value.length, devices: devices.value.length, bound: devices.value.filter(device => !!device.profile_id).length, verified: devices.value.filter(device => device.verified).length })
}

async function readStation() {
  if (!Number.isInteger(serverInput.value) || serverInput.value < 1 || serverInput.value > 247) {
    error.value = "站号必须是 1–247 的整数"
    return
  }
  selectedServerID.value = serverInput.value
  await loadModel()
}

async function loadModel() {
  if (!selectedChannelID.value || !Number.isInteger(selectedServerID.value) || selectedServerID.value < 1 || selectedServerID.value > 247) {
    error.value = "先选择通道并填写 1–247 的站号"
    return
  }
  const requestID = ++modelRequest
  ++previewRequest
  ++lineTestRequest
  const channelID = selectedChannelID.value
  const serverID = selectedServerID.value
  busy.value = "model"
  error.value = ""
  previewResponse.value = null
  lineTest.value = null
  try {
    const result = await request<UnitModel>(`/modbus/${encodeURIComponent(channelID)}/units/${serverID}/model`)
    if (requestID !== modelRequest || channelID !== selectedChannelID.value || serverID !== selectedServerID.value) return
    model.value = result
    config.value = result.model ? { ...result.model, point_map: { ...result.model.point_map } } : { name: `站号 ${serverID}`, profile_id: "", point_map: {}, verified: false, verifier: "", verified_at: "", device_serial: "", verification: "" }
  } catch (cause) {
    if (requestID === modelRequest) error.value = `设备模型读取失败：${messageOf(cause)}`
  } finally {
    if (requestID === modelRequest) busy.value = ""
  }
}

async function ensureSelection() {
  if (selectedChannelID.value && channels.value.some(channel => channel.id === selectedChannelID.value)) return
  const preferred = devices.value.find(device => device.business_node_id === props.selectedNodeId) || devices.value[0]
  selectedChannelID.value = preferred?.channel_id || channels.value[0]?.id || ""
  selectedServerID.value = preferred?.server_id || channels.value[0]?.devices?.find(device => device.server_id !== undefined)?.server_id || 1
  serverInput.value = selectedServerID.value
  if (selectedChannelID.value) await loadModel()
}

async function reload() {
  error.value = ""
  notice.value = ""
  await loadDirectory()
  await ensureSelection()
  if (selectedChannelID.value) await loadModel()
}

async function reloadAfterWrite(message: string) {
  await loadDirectory()
  await loadModel()
  notice.value = message
  emit("refresh")
}

async function openDevice(device: LedgerDevice) {
  ledgerOpen.value = false
  stage.value = "profile"
  selectedChannelID.value = device.channel_id
  selectedServerID.value = device.server_id
  serverInput.value = device.server_id
  model.value = null
  notice.value = ""
  await loadModel()
}

async function startNewUnit() {
  selectedChannelID.value = channels.value[0]?.id || ""
  selectedServerID.value = 1
  serverInput.value = 1
  model.value = null
  config.value = { name: "站号 1", profile_id: "", point_map: {}, verified: false }
  stage.value = "channel"
  ledgerOpen.value = false
  await loadModel()
}

async function selectChannel(id: string) {
  selectedChannelID.value = id
  selectedServerID.value = channels.value.find(channel => channel.id === id)?.devices?.find(device => device.server_id !== undefined)?.server_id || 1
  serverInput.value = selectedServerID.value
  channelForm.value = null
  await loadModel()
}

function selectProfile(id: string) {
  const profile = availableProfiles.value.find(item => item.id === id)
  config.value = {
    ...config.value,
    profile_id: id,
    point_map: profile ? automaticPointMap(profile, model.value?.points || [], selectedServerID.value) : {},
    verified: false,
  }
  previewResponse.value = null
  notice.value = ""
}

async function previewProfile() {
  if (requireChannel()) return
  if (!profileID.value || blockStationChange()) return
  const requestID = ++previewRequest
  const channelID = selectedChannelID.value
  const serverID = selectedServerID.value
  const selectedProfileID = profileID.value
  busy.value = "preview"
  error.value = ""
  notice.value = ""
  try {
    const result = await request<ProfileApplyResponse>(`/modbus/${encodeURIComponent(channelID)}/units/apply`, "POST", { profile_id: selectedProfileID, servers: [serverID], dry_run: true })
    if (requestID !== previewRequest || channelID !== selectedChannelID.value || serverID !== selectedServerID.value || selectedProfileID !== profileID.value) return
    previewResponse.value = result
    const preview = result.results[0]
    if (preview) config.value.point_map = Object.fromEntries(preview.points.filter(point => point.point_id).map(point => [point.name, point.point_id!]))
  } catch (cause) {
    if (requestID === previewRequest) error.value = `Profile 预览失败：${messageOf(cause)}`
  } finally {
    if (requestID === previewRequest) busy.value = ""
  }
}

async function applyProfile() {
  if (requireChannel()) return
  if (!applyPreview.value || blockStationChange()) return
  busy.value = "apply"
  error.value = ""
  notice.value = ""
  try {
    const result = await request<ProfileApplyResponse>(`/modbus/${encodeURIComponent(selectedChannelID.value)}/units/apply`, "POST", { profile_id: profileID.value, servers: [selectedServerID.value], dry_run: false })
    await reloadAfterWrite(`Profile 已应用到 ${result.applied} 个站号；请继续核对点位与验证`)
  } catch (cause) { error.value = `Profile 应用失败：${messageOf(cause)}` } finally { busy.value = "" }
}

function updateMapping(name: string, id: string) {
  const next = { ...config.value.point_map }
  if (id) next[name] = id
  else delete next[name]
  config.value = { ...config.value, point_map: next, verified: false }
  notice.value = ""
}

function autoMatch() {
  if (!selectedProfile.value) return
  config.value = { ...config.value, point_map: automaticPointMap(selectedProfile.value, model.value?.points || [], selectedServerID.value), verified: false }
  notice.value = `已按名称匹配 ${mappedCount.value} 个点位，尚未保存`
}

async function saveMapping() {
  if (requireChannel() || blockStationChange()) return
  if (!profileID.value) { error.value = "先选择设备 Profile"; return }
  busy.value = "mapping"
  error.value = ""
  notice.value = ""
  try {
    const result = await request<UnitModel>(`/modbus/${encodeURIComponent(selectedChannelID.value)}/units/${selectedServerID.value}/model`, "PUT", prepareModelSave(config.value))
    model.value = result
    config.value = result.model ? { ...result.model, point_map: { ...result.model.point_map } } : config.value
    await reloadAfterWrite("点位映射已保存")
  } catch (cause) { error.value = `点位映射保存失败：${messageOf(cause)}` } finally { busy.value = "" }
}

function openChannelForm(edit: boolean) {
  const channel = edit ? selectedChannel.value : null
  const node = channel ? props.snapshot?.nodes.find(item => item.id === channel.id) : null
  channelForm.value = {
    editing: channel?.id || "",
    parent: node?.parent || props.snapshot?.roots[0] || "",
    description: node ? nameOf(node) : "",
    protocol: (node ? point(node, "protocol")?.text : channel?.protocol)?.toUpperCase() === "RTU" ? "RTU" : "TCP",
    uri: node ? point(node, "uri")?.text || channel?.address || "127.0.0.1:1502" : "127.0.0.1:1502",
    port: node ? point(node, "port")?.text || "COM1" : "COM1",
    baud: node ? point(node, "baud")?.text || String(point(node, "baud")?.value || 9600) : "9600",
    data_bits: node ? point(node, "dataBits")?.value || 8 : 8,
    parity: node ? point(node, "parity")?.text || "N" : "N",
    stop_bits: node ? point(node, "stopBits")?.value || 1 : 1,
    poll_period: node ? point(node, "pollPeriod")?.value || 1000 : 1000,
    timeout: node ? point(node, "timeout")?.value || 1000 : 1000,
    disabled: node ? (point(node, "disabled")?.value || 0) !== 0 : false,
  }
}

async function saveChannel() {
  const form = channelForm.value
  if (!form) return
  if (!form.description.trim()) { error.value = "通道名称不能为空"; return }
  if (form.protocol === "TCP" && !form.uri.trim()) { error.value = "TCP 目标地址不能为空"; return }
  if (form.protocol === "RTU" && !form.port.trim()) { error.value = "RTU 串口不能为空"; return }
  busy.value = "channel"
  error.value = ""
  notice.value = ""
  try {
    const { editing, ...payload } = form
    const result = await request<{ id?: string; node_id?: string }>(editing ? `/modbus/${encodeURIComponent(editing)}` : "/modbus", editing ? "PUT" : "POST", { ...payload, description: payload.description.trim(), baud: String(payload.baud) })
    selectedChannelID.value = editing || result.id || result.node_id || selectedChannelID.value
    channelForm.value = null
    await reloadAfterWrite("通道已保存")
  } catch (cause) { error.value = `通道保存失败：${messageOf(cause)}` } finally { busy.value = "" }
}

function openPointForm() {
  if (requireChannel()) return
  pointForm.value = { parent: selectedChannelID.value, description: "", server_id: selectedServerID.value, address: 0, io_type: "modbusHoldingRegister", data_format: "uint16", read_only: true, scale: 1, offset: 0, units: "", disabled: false }
}

async function savePoint() {
  if (requireChannel() || blockStationChange()) return
  const form = pointForm.value
  if (!form) return
  if (!form.description.trim()) { error.value = "点位名称不能为空"; return }
  if (!Number.isInteger(form.address) || form.address < 0 || form.address > 65535) { error.value = "点位地址必须是 0–65535 的整数"; return }
  if (!Number.isFinite(form.scale) || form.scale === 0) { error.value = "缩放必须是非零数值"; return }
  busy.value = "point"
  error.value = ""
  notice.value = ""
  try {
    await request(`/modbus/${encodeURIComponent(form.parent)}/io`, "POST", { ...form, description: form.description.trim() })
    pointForm.value = null
    await reloadAfterWrite("物理点已创建；请核对 Profile 映射并保存")
  } catch (cause) { error.value = `物理点保存失败：${messageOf(cause)}` } finally { busy.value = "" }
}

async function runLineTest() {
  if (requireChannel()) return
  if (!canConfigure.value) { error.value = "当前账号无权执行此操作"; return }
  if (blockStationChange()) return
  const requestID = ++lineTestRequest
  const channelID = selectedChannelID.value
  const serverID = selectedServerID.value
  busy.value = "line-test"
  error.value = ""
  lineTest.value = null
  try {
    const result = await request<LineTest>(`/modbus/${encodeURIComponent(channelID)}/line-test`, "POST", { server_id: serverID })
    if (requestID === lineTestRequest && channelID === selectedChannelID.value && serverID === selectedServerID.value) lineTest.value = result
  } catch (cause) {
    if (requestID === lineTestRequest) error.value = `连通测试失败：${messageOf(cause)}`
  } finally {
    if (requestID === lineTestRequest) busy.value = ""
  }
}

async function saveVerification() {
  if (requireChannel() || blockStationChange()) return
  const validation = verificationError(config.value, missingRequired.value)
  if (validation) { error.value = validation; return }
  if (profileDirty.value || mappingDirty.value) { error.value = "先保存 Profile 和点位映射，再记录验证"; return }
  busy.value = "verification"
  error.value = ""
  notice.value = ""
  try {
    await request<UnitModel>(`/modbus/${encodeURIComponent(selectedChannelID.value)}/units/${selectedServerID.value}/model`, "PUT", prepareModelSave(config.value))
    await reloadAfterWrite("验证记录已保存")
  } catch (cause) { error.value = `验证记录保存失败：${messageOf(cause)}` } finally { busy.value = "" }
}

watch(() => props.selectedNodeId, id => {
  const device = devices.value.find(item => item.business_node_id === id)
  if (device && deviceKey(device) !== selectedKey.value) void openDevice(device)
})

onMounted(async () => { await reload() })
</script>

<style scoped>
.access-step { display: flex; flex-direction: column; gap: 12px; min-width: 0; }
.step-head, .panel-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.step-head b { font-size: 16px; }
.step-head > span:not(.spacer), .panel-head .src { color: var(--faint); font-size: 12px; }
.ledger-panel, .preview-panel, .editor-panel, .quality-panel { padding: 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 12px; background: rgba(255,255,255,.28); }
.ledger-table td strong, td small { display: block; }
td small { color: var(--faint); font-size: 10.5px; margin-top: 2px; }
tr.selected td { background: rgba(230,243,238,.45); }
.pipeline { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.pipeline button { border: 1px solid rgba(255,255,255,.75); border-radius: 999px; padding: 5px 12px; background: rgba(255,255,255,.48); color: var(--faint); font-size: 12px; }
.pipeline button.active { border-color: var(--accent); color: var(--accent); background: rgba(255,255,255,.82); }
.acc-grid, .verification-grid { display: grid; grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr); gap: 12px; align-items: start; }
.acc-card { min-width: 0; padding: 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 12px; background: rgba(255,255,255,.38); box-shadow: 0 8px 22px rgba(45,83,110,.06); }
.acc-card > label, .verification-grid .acc-card > label { display: flex; flex-direction: column; gap: 5px; margin-bottom: 10px; color: var(--muted); font-size: 12px; }
input, select, textarea { width: 100%; border: 1px solid rgba(255,255,255,.82); border-radius: 8px; padding: 7px 9px; background: rgba(255,255,255,.62); font-size: 12px; }
select { min-height: 36px; }
textarea { resize: vertical; }
.pick { display: flex; flex-direction: column; gap: 4px; width: 100%; text-align: left; border: 1px solid rgba(255,255,255,.72); border-radius: 9px; padding: 8px 10px; margin-bottom: 6px; background: rgba(255,255,255,.42); }
.pick.active { border-color: var(--accent); background: rgba(240,250,249,.72); }
.pick strong { font-size: 12.5px; }
.pick small { color: var(--faint); font-size: 10.5px; }
.station-form { display: flex; align-items: end; gap: 8px; flex-wrap: wrap; margin-top: 12px; }
.station-form label { width: 130px; color: var(--muted); font-size: 12px; }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.form-grid label { display: flex; flex-direction: column; gap: 5px; color: var(--muted); font-size: 12px; }
.check-label { display: flex !important; flex-direction: row !important; align-items: center; gap: 7px !important; }
.check-label input { width: auto; }
.notice { margin: 0; padding: 9px 12px; border: 1px solid rgba(35,112,95,.25); border-radius: 10px; background: rgba(230,243,238,.72); color: var(--mint); font-size: 12.5px; }
.warning-box { margin-top: 10px; padding: 9px 11px; border: 1px solid rgba(164,99,27,.25); border-radius: 9px; background: rgba(253,247,234,.75); color: #6f4a12; font-size: 12px; line-height: 1.7; }
.warn-text { color: var(--amber); }
.ok-text { color: var(--mint); }
.line-result { margin-top: 10px; color: var(--muted); font-size: 12px; line-height: 1.8; }
.profile-summary { margin-top: 12px; }
@media (max-width: 1100px) {
  .acc-grid, .verification-grid { grid-template-columns: 1fr; }
}
@media (max-width: 760px) {
  .form-grid { grid-template-columns: 1fr; }
  .step-head .spacer { display: none; }
}
</style>
