<template>
  <OverviewPreview v-if="PREVIEW_MODE" :initial-history="initialHistory" />
  <LiveOverview v-else />
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onLoad } from '@dcloudio/uni-app'
import { PREVIEW_MODE } from "../../api/source"
const initialHistory = ref(false)
onLoad(options => { initialHistory.value = options?.history === '1' })
import LiveOverview from "./LiveOverview.vue"
import OverviewPreview from "./OverviewPreview.vue"
</script>
