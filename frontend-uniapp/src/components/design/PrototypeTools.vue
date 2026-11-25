<template>
  <view class="prototype-tools">
    <view class="banner context-strip"><span>当前对象 <b>{{ selected ? nameOf(selected) : '未选择对象' }}</b> <span class="tag">{{ selected ? kindLabel(selected) : '—' }}</span></span><span class="note">接入与策略从台账节点进入；站点工具在根节点。</span><button class="ghost" @click="emit('back')">回台账树</button></view>
    <view class="variant-head"><b>{{ definition?.label || '站点工具' }}</b><span>{{ definition?.question }}</span></view>
    <AggregationPanel v-if="tool === 'aggregate'" :snapshot="snapshot" :parent="selected?.id || ''" :read-only="readOnly" />
    <template v-else>
      <view class="skeleton glass"><strong>这一页只验证位置，不实现内容。</strong><view class="line" style="width:72%" /><view class="line" style="width:48%" /><view class="line" style="width:61%" /></view>
      <p class="note">{{ definition?.note }}</p>
    </template>
  </view>
</template>

<script setup lang="ts">
import { computed } from "vue"
import type { Snapshot } from "../../types/api"
import { nameOf } from "../../types/api"
import { kindLabel } from "./design"
import AggregationPanel from "./AggregationPanel.vue"

const props = withDefaults(defineProps<{ tool: string; snapshot: Snapshot | null; selectedNodeId?: string; readOnly?: boolean }>(), { selectedNodeId: "", readOnly: false })
const emit = defineEmits<{ (event: "back"): void }>()
const selected = computed(() => props.snapshot?.nodes.find(node => node.id === props.selectedNodeId) || props.snapshot?.nodes.find(node => node.id === props.snapshot?.roots[0]))
const tools: Record<string, { label: string; question: string; note: string }> = {
  channels: { label: "通道", question: "数据从哪来、连接通不通？", note: "一个端口一行；线路测试与扫描只读，不写设备。" },
  points: { label: "点位表", question: "这个数是什么含义、绑到哪个属性？", note: "批量新增、改缩放、CSV 预检与回滚都在这一页。" },
  profiles: { label: "型号库", question: "厂商型号与版本、点位/命令映射，换现场怎么复用？", note: "跨通道、跨项目复用；从设备绑定时也能选。" },
  templates: { label: "模板库", question: "点表、策略、画面哪些能跨项目复用？", note: "点表、策略、画面三类模板；新建项目时可从这里选。" },
  verify: { label: "设备验证", question: "哪台设备的映射、读写方向与连通已经确认？", note: "一台设备一行；验证结论不等于项目可交付。" },
  aggregate: { label: "聚合计算", question: "多个点怎么合成一个值？", note: "聚合定义挂在节点上；这里只是跨节点列表。结果是对应节点上的只读聚合点，窗口值会标明滞后。" },
  calculations: { label: "计算点", question: "哪些值由公式算出、来源是什么？", note: "公式与来源可查；只读，不写设备。" },
  virtualInputs: { label: "虚拟输入", question: "哪些输入是模拟的、会不会写设备？", note: "模拟输入只参与计算，不代表实测。" },
  health: { label: "通信健康与报文", question: "通不通、断在哪、原始帧是什么？", note: "实时窗口与台账累计分开标注；原始帧只读。" },
  alarmTypes: { label: "报警类型", question: "什么算异常，持续多久、怎么恢复？", note: "定义阈值、持续与恢复；确认接手不等于故障消除。" },
  retention: { label: "数据保留", question: "历史留多久、磁盘会用多少？", note: "部署级设置；当前实现没有界面，这一页只验证位置。" },
}
const definition = computed(() => tools[props.tool])
</script>

<style scoped>
.prototype-tools { min-width: 0; }
.context-strip { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; margin-bottom: 14px; }
.context-strip uni-button { margin-left: auto; }
.variant-head { display: flex; align-items: baseline; flex-wrap: wrap; gap: 10px; margin-bottom: 14px; }
.variant-head b { font-size: 16px; }
.variant-head span, .note { color: var(--faint); font-size: 12px; line-height: 1.7; }
.skeleton { border-radius: 14px; padding: 28px; }
.skeleton strong { display: block; margin-bottom: 24px; font-size: 14px; }
.line { height: 12px; margin-top: 14px; border-radius: 8px; background: rgba(72,111,139,.1); }
</style>
