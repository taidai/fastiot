<template>
  <view class="variant-head"><b>行业包</b><span>本站启用了什么行业能力？</span></view>
  <view v-if="message" class="success">{{ message }}</view>
  <view v-if="error" class="error">{{ error }}</view>
  <section v-if="loading && !packages.length" class="card glass"><view class="empty">正在读取行业包…</view></section>
  <section v-else-if="!error && !packages.length" class="card glass"><view class="empty">没有行业包记录；IoT/SCADA 底座照常可用。</view></section>
  <template v-else>
    <template v-for="pkg in packages" :key="pkg.id">
      <view v-if="!pkg.installed" class="banner industry-banner">
        <span>{{ pkg.label || pkg.id }}未启用：功率分配等页面不可达；底座功能不受影响。</span>
        <button v-if="canChange" :disabled="!!busy" @click="install(pkg)">{{ busy === pkg.id ? "处理中…" : "启用" }}</button>
      </view>
      <section class="card glass industry-card">
        <view class="panel-head"><h3>{{ pkg.label || pkg.id }}</h3><span class="tag" :class="pkg.installed ? 'ok' : ''">{{ pkg.installed ? "已启用" : "未启用" }}</span><span class="tag">{{ pkg.id }}@{{ pkg.version || "—" }}</span></view>
        <view v-if="pkg.installed" class="package-provides">
          <view v-for="item in pkg.provides || []" :key="item" class="row capability-row"><span>{{ item }}</span><span class="src">{{ provideType(item) }}</span></view>
        </view>
        <p v-else class="note">停用不删除已配置的能源对象；再次启用时它们仍在。启用不等于购买。</p>
        <view class="installed-at">{{ pkg.installed_at ? `启用时间：${clock(pkg.installed_at)}` : pkg.installed ? "默认启用；本站尚无单独启用记录" : "本站未启用" }}</view>
        <button v-if="pkg.installed && canChange" class="tag industry-toggle" :class="{ danger: confirmId === pkg.id }" :disabled="!!busy" @click="uninstall(pkg)">
          {{ busy === pkg.id ? "处理中…" : confirmId === pkg.id ? "确认停用（不删除数据）" : "停用（不删除数据）" }}
        </button>
        <p v-if="pkg.installed" class="note">启用不等于购买；停用只关闭行业能力入口，不删除已配置对象。依赖版本不匹配时，项目包导入仍由后端拒绝。</p>
      </section>
    </template>
  </template>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue"
import { request } from "../../api/client"
import { clock, deliveryError } from "./contracts"

type IndustryPackage = { id: string; version: string; label: string; provides?: string[]; installed: boolean; installed_at?: string }
defineProps<{ canChange: boolean }>()
const packages = ref<IndustryPackage[]>([])
const loading = ref(false)
const busy = ref("")
const error = ref("")
const message = ref("")
const confirmId = ref("")
const provideTypes: Record<string, string> = {
  "能源单元、设备与能力约束": "对象类型",
  "能源策略与电站级目标": "策略模板",
  "功率分配、仲裁与联合调度": "策略",
  "需量窗口与 96 点计划": "策略",
}
function provideType(item: string) { return provideTypes[item] || "行业能力" }
async function load() {
  loading.value = true
  error.value = ""
  try {
    const result = await request<{ packages: IndustryPackage[] }>("/industry/packages")
    packages.value = result.packages || []
  } catch (cause) { packages.value = []; error.value = deliveryError(cause) } finally { loading.value = false }
}
async function install(pkg: IndustryPackage) {
  busy.value = pkg.id; error.value = ""; message.value = ""; confirmId.value = ""
  try {
    await request("/industry/packages", "POST", { id: pkg.id, version: pkg.version })
    message.value = `${pkg.label || pkg.id} 已启用；不代表购买或部署`
    await load()
  } catch (cause) { error.value = deliveryError(cause) } finally { busy.value = "" }
}
async function uninstall(pkg: IndustryPackage) {
  if (confirmId.value !== pkg.id) { confirmId.value = pkg.id; message.value = "再次点击确认停用；已配置对象不会删除"; return }
  busy.value = pkg.id; error.value = ""; message.value = ""
  try {
    await request(`/industry/packages/${encodeURIComponent(pkg.id)}`, "DELETE")
    message.value = `${pkg.label || pkg.id} 已停用；配置数据保留`
    confirmId.value = ""
    await load()
  } catch (cause) { error.value = deliveryError(cause) } finally { busy.value = "" }
}
onMounted(() => { void load() })
</script>

<style scoped>
.variant-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin: 0; }
.variant-head b { font-size: 15px; }
.variant-head span, .note { color: var(--faint); font-size: 12.5px; }
.panel-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.panel-head h3 { margin: 0; font-size: 17px; }
.package-provides { min-width: 0; }
.capability-row .src { flex: none; }
.industry-banner { margin: 0; }
.installed-at { margin-bottom: 10px; color: var(--faint); font-size: 11.5px; }
.industry-toggle { margin-top: 0; }
.danger { color: var(--danger); border-color: rgba(178,58,50,.35); }
.success { margin-bottom: 10px; padding: 10px 12px; border-radius: 10px; background: rgba(230,243,238,.8); color: var(--mint); font-size: 12.5px; }
.error { margin-bottom: 10px; }
.note { margin-bottom: 0; line-height: 1.7; }
</style>
