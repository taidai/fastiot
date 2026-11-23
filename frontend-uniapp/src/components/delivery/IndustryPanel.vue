<template>
  <section class="card glass">
    <view class="step-head"><b>行业包</b><span>本站启用了什么行业能力？</span></view>
    <view v-if="message" class="success">{{ message }}</view>
    <view v-if="error" class="error">{{ error }}</view>
    <view v-if="loading && !packages.length" class="empty">正在读取行业包…</view>
    <view v-else-if="!error && !packages.length" class="empty">没有行业包记录；IoT/SCADA 底座照常可用。</view>
    <view v-else class="package-list">
      <view v-for="pkg in packages" :key="pkg.id" class="package-item">
        <view class="package-head"><view><b>{{ pkg.label || pkg.id }}</b><span>{{ pkg.id }}@{{ pkg.version || "—" }}</span></view><span class="chip" :class="pkg.installed ? 'ok' : 'warn'">{{ pkg.installed ? "已启用" : "未启用" }}</span></view>
        <view class="provided"><view v-for="item in pkg.provides || []" :key="item">{{ item }}</view></view>
        <view class="installed-at">{{ pkg.installed_at ? `启用时间：${clock(pkg.installed_at)}` : pkg.installed ? "默认启用；本站尚无单独启用记录" : "本站未启用" }}</view>
        <button v-if="canChange" class="ghost" :class="{ danger: confirmId === pkg.id }" :disabled="!!busy" @click="pkg.installed ? uninstall(pkg) : install(pkg)">
          {{ busy === pkg.id ? "处理中…" : pkg.installed ? (confirmId === pkg.id ? "确认停用（不删除数据）" : "停用（不删除数据）") : "启用" }}
        </button>
      </view>
    </view>
    <p class="note">启用不等于购买；停用只关闭行业能力入口，不删除已配置对象。依赖版本不匹配时，项目包导入仍由后端拒绝。</p>
  </section>
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
.step-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.step-head b { font-size: 16px; }
.step-head span, .note { color: var(--faint); font-size: 12px; }
.package-list { display: grid; gap: 10px; }
.package-item { padding: 14px; border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.35); }
.package-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; }
.package-head > view { display: grid; gap: 4px; }
.package-head b { font-size: 14px; }
.package-head span, .installed-at { color: var(--faint); font-size: 11.5px; }
.provided { display: grid; gap: 6px; margin: 12px 0; font-size: 12.5px; }
.provided view::before { content: "✓"; margin-right: 7px; color: var(--accent); }
.installed-at { margin-bottom: 10px; }
.danger { color: var(--danger); border-color: rgba(178,58,50,.35); }
.success { margin-bottom: 10px; padding: 10px 12px; border-radius: 10px; background: rgba(230,243,238,.8); color: var(--mint); font-size: 12.5px; }
.error { margin-bottom: 10px; }
.note { margin-bottom: 0; line-height: 1.7; }
</style>
