<template>
  <view class="history-scrim" @click="emit('close')"></view>
  <view class="history-drawer glass" role="dialog" aria-modal="true" aria-label="历史数据">
    <HistoryPanel :point-options="pointOptions" :initial-point="initialPoint" back-label="← 返回" @back="emit('close')" />
  </view>
</template>

<script setup lang="ts">
import HistoryPanel from "../history/HistoryPanel.vue"
import type { HistoryPointOption } from "../history/history"

defineProps<{ pointOptions: HistoryPointOption[]; initialPoint: string }>()
const emit = defineEmits<{ (event: "close"): void }>()
</script>

<style scoped>
.history-scrim { position: fixed; inset: 0; z-index: 55; background: rgba(20,32,40,.16); }
.history-drawer { position: fixed; z-index: 56; top: 16px; right: 16px; bottom: 16px; width: min(920px, 96vw); padding: 18px 20px; overflow: auto; background: var(--glass-strong); }
@media (max-width: 760px) { .history-drawer { inset: 8px; width: auto; padding: 14px; } }
</style>
