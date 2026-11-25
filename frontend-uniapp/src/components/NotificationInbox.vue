<template>
  <view class="inbox-entry" @keydown.esc.stop="close">
    <button class="inbox-trigger" role="button" tabindex="0" :aria-expanded="open" aria-haspopup="dialog" @keydown.enter.prevent @keydown.space.prevent @keyup.enter.stop.prevent="toggle" @keyup.space.stop.prevent="toggle" @click="toggle">站内通知</button>
    <section v-if="open" class="inbox-panel glass" role="dialog" aria-label="站内通知">
      <view class="inbox-heading"><strong>站内通知</strong><button role="button" tabindex="0" aria-label="关闭站内通知" @keydown.enter.prevent @keydown.space.prevent @keyup.enter.stop.prevent="close" @keyup.space.stop.prevent="close" @click="close">关闭</button></view>
      <p class="inbox-note">{{ PREVIEW_MODE ? '隔离预览 · 刷新页面后重置' : '只显示发给本账号且当前有权查看的记录' }}</p>
      <p class="inbox-note">已记录不代表送达。打开此面板不会自动标记已读。</p>
      <view class="inbox-tools"><button role="button" :tabindex="busy ? -1 : 0" :disabled="busy" @keydown.enter.prevent @keydown.space.prevent @keyup.enter.stop.prevent="load()" @keyup.space.stop.prevent="load()" @click="load()">刷新</button><text v-if="busy" role="status">读取中…</text></view>
      <p v-if="error" role="alert" class="inbox-error">{{ error }}</p>
      <p v-else-if="!busy && !items.length" class="inbox-note">暂无当前有权查看的站内通知</p>
      <article v-for="item in items" :key="item.id" class="inbox-item">
        <view class="inbox-heading"><strong>{{ item.title }}</strong><text class="inbox-state">已记录 · {{ item.read_at ? '已读' : '未读' }}</text></view>
        <p class="inbox-body">{{ item.body }}</p>
        <view class="inbox-footer"><time>{{ timeText(item.created_at) }}</time><button v-if="!item.read_at" role="button" :tabindex="busy ? -1 : 0" :disabled="busy" @keydown.enter.prevent @keydown.space.prevent @keyup.enter.stop.prevent="read(item.id)" @keyup.space.stop.prevent="read(item.id)" @click="read(item.id)">标记已读</button><time v-else>已读于 {{ timeText(item.read_at) }}</time></view>
      </article>
      <button v-if="nextBefore" role="button" :tabindex="busy ? -1 : 0" :disabled="busy" @keydown.enter.prevent @keydown.space.prevent @keyup.enter.stop.prevent="load(nextBefore)" @keyup.space.stop.prevent="load(nextBefore)" @click="load(nextBefore)">下一页</button>
    </section>
  </view>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { listNotifications, markNotificationRead } from '../api/notifications'
import { ApiError } from '../api/client'
import { PREVIEW_MODE } from '../api/source'
import { session } from '../stores/session'
import { projectContext } from '../stores/projects'
import type { NotificationItem } from '../types/notification'
const open = ref(false), busy = ref(false), error = ref(''), nextBefore = ref('')
const items = ref<NotificationItem[]>([])
let generation = 0
function close() {
  open.value = false
  generation++
  busy.value = false
  document.querySelector<HTMLElement>('.inbox-trigger')?.focus()
}
watch(() => [session.token, projectContext.generation], () => { close(); items.value = []; error.value = '' })
async function toggle() {
  if (open.value) { close(); return }
  open.value = true
  await nextTick()
  document.querySelector<HTMLElement>('.inbox-panel [aria-label="关闭站内通知"]')?.focus()
  await load()
}
async function load(before = '') {
  if (busy.value) return
  const current = ++generation
  busy.value = true; error.value = ''; items.value = []; nextBefore.value = ''
  try {
    const page = await listNotifications(before)
    if (current === generation) { items.value = page.items; nextBefore.value = page.next_before || '' }
  } catch (e) { if (current === generation) error.value = failure(e) }
  finally { if (current === generation) busy.value = false }
}
async function read(id: string) {
  if (busy.value) return
  const current = ++generation
  busy.value = true; error.value = ''
  try {
    const result = await markNotificationRead(id)
    if (current === generation) { const item = items.value.find(item => item.id === id); if (item) item.read_at = result.read_at }
  } catch (e) {
    if (current === generation) { items.value = []; nextBefore.value = ''; error.value = failure(e) }
  } finally { if (current === generation) busy.value = false }
}
function failure(e: unknown) {
  const messages: Record<string, string> = { notification_forbidden: '此通知不属于本账号或已超出当前可查看范围', notification_storage_unavailable: '通知记录暂时无法读取，请重试', notification_read_not_saved: '已读尚未确认保存，请刷新后核实' }
  return e instanceof ApiError && messages[e.code] || (e instanceof Error ? e.message : '通知操作失败，请重试')
}
const timeText = (value: string) => new Date(value).toLocaleString()
</script>

<style scoped>
.inbox-entry { display: flex; }
.inbox-trigger, .inbox-panel [role="button"] { min-height: 30px; margin: 0; padding: 6px 12px; border: 1px solid var(--glass-border); border-radius: 10px; background: var(--control-bg, rgba(255,255,255,.55)); color: var(--ink); font-size: 12px; line-height: normal; white-space: nowrap; }
.inbox-panel { position: absolute; top: calc(100% + 8px); right: 8px; width: min(440px, calc(100vw - 36px)); box-sizing: border-box; max-height: 70vh; overflow: auto; padding: 16px; background: var(--glass-strong, #fff); text-align: left; }
.inbox-heading, .inbox-footer, .inbox-tools { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.inbox-heading strong { font-size: 14px; overflow-wrap: anywhere; }
.inbox-note, .inbox-footer { color: var(--muted); font-size: 11px; line-height: 1.6; }
.inbox-tools { justify-content: flex-start; font-size: 12px; }
.inbox-item { padding: 14px 0; border-bottom: 1px solid var(--line); }
.inbox-state { color: var(--accent); font-size: 11px; white-space: nowrap; }
.inbox-body { margin: 10px 0; font-size: 13px; line-height: 1.6; white-space: pre-wrap; overflow-wrap: anywhere; }
.inbox-footer { flex-wrap: wrap; }
.inbox-error { color: var(--red, #a32b37); font-size: 12px; }
[role="button"]:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
</style>
