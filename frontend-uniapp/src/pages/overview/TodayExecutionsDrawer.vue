<template>
  <view class="exec-scrim" @click="emit('close')"></view>
  <view class="exec-drawer glass" role="dialog" aria-modal="true" aria-label="今日执行 · 全部记录">
    <view class="drawer-head"><h2>今日执行 · 全部记录</h2><button class="ghost" aria-label="关闭执行抽屉" @click="emit('close')">关闭</button></view>
    <TodayExecutions :layer-id="layerId" />
  </view>
</template>

<script setup lang="ts">
import TodayExecutions from './TodayExecutions.vue'

defineProps<{ layerId: string }>()
const emit = defineEmits<{ (event: "close"): void }>()
</script>

<style scoped>
.exec-scrim { position: fixed; inset: 0; z-index: 55; background: rgba(20,32,40,.16); }
.exec-drawer { position: fixed; z-index: 56; top: 16px; right: 16px; bottom: 16px; width: min(860px, 96vw); padding: 18px 20px; overflow: auto; background: var(--glass-strong); }
.drawer-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 10px; }
.drawer-head h2 { margin: 0; font-size: 15px; }
@media (max-width: 760px) { .exec-drawer { inset: 8px; width: auto; padding: 14px; } }
</style>
