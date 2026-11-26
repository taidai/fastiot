<template>
  <view class="login-page">
    <view class="login glass">
      <view class="brand">FastIOT</view>
      <view class="subtitle">可重复交付的 IoT / SCADA 工作台</view>
      <label>账号<input v-model="email" type="text" placeholder="邮箱或账号" @blur="email = inputValue($event)" @confirm="email = inputValue($event); submit()" /></label>
      <label>密码<input v-model="password" type="password" placeholder="密码" @blur="password = inputValue($event)" @confirm="password = inputValue($event); submit()" /></label>
      <button class="primary" :disabled="busy" @click="submit">{{ busy ? "登录中…" : "登录" }}</button>
      <view v-if="error" class="error">{{ error }}</view>
      <view class="note">使用本实例账号；角色和对象范围由服务端决定。</view>
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref } from "vue"
import { login } from "../../api/client"
import { startPolling, session } from "../../stores/session"

const email = ref("")
const password = ref("")
const busy = ref(false)
const error = ref("")
// uni input model updates are throttled; blur/confirm carry the current value.
function inputValue(event: unknown) {
  return (event as { detail: { value: string } }).detail.value
}
async function submit() {
  if (busy.value) return
  if (!email.value || !password.value) { error.value = "请输入账号和密码"; return }
  busy.value = true; error.value = ""
  try {
    const token = await login(email.value, password.value)
    session.token = token
    startPolling()
    uni.reLaunch({ url: "/pages/overview/index" })
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "登录失败"
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.login-page { min-height: 100vh; display: grid; place-items: center; padding: 20px; }
.login { width: min(400px, 100%); padding: 26px; display: flex; flex-direction: column; gap: 14px; }
.brand { color: var(--accent); font-size: 26px; font-weight: 700; }
.subtitle { color: var(--faint); font-size: 12px; margin-top: -8px; }
label { display: flex; flex-direction: column; gap: 6px; color: var(--muted); font-size: 12px; }
input { min-height: 44px; border: 1px solid var(--glass-border); border-radius: 10px; background: rgba(255,255,255,.6); padding: 11px 12px; line-height: 20px; }
.note { color: var(--faint); font-size: 11px; line-height: 1.7; }
</style>
