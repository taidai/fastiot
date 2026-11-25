<template>
  <view v-if="node" class="member-roles">
    <view class="panel-head"><strong>单元成员角色</strong><span>必需角色未绑定只限制相关能力，草稿可以保存。</span></view>
    <p v-if="missing.length" class="warning-box">必需角色未绑定：{{ missing.join("、") }}</p>
    <div v-if="draft.length" class="table-wrap"><table><thead><tr><th>角色</th><th>绑定设备 / 点位</th><th>必需</th><th></th></tr></thead>
      <tbody><tr v-for="(member, index) in draft" :key="index">
        <td><input v-model="member.role" :disabled="readOnly || busy" placeholder="例如：驱动"></td>
        <td><select v-model="member.node_id" :disabled="readOnly || busy">
          <option value="">未绑定</option>
          <option v-for="target in targets" :key="target.id" :value="target.id">{{ target.name }} · {{ target.kind }}</option>
        </select></td>
        <td><checkbox :checked="member.required" :disabled="readOnly || busy" aria-label="必需角色" @click="member.required = !member.required" /></td>
        <td><button class="ghost" :disabled="readOnly || busy" @click="draft.splice(index, 1)">移除</button></td>
      </tr></tbody>
    </table></div>
    <view v-else class="empty">还没有成员角色；添加角色并绑定真实设备或点位。</view>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <view class="actions"><button class="ghost" :disabled="readOnly || busy" @click="draft.push({ role: '', node_id: '', required: true })">＋ 添加角色</button><button class="primary" :disabled="readOnly || busy" @click="save">{{ busy ? "保存中…" : "保存成员绑定" }}</button></view>
  </view>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf } from "../../types/api"
import type { Snapshot } from "../../types/api"
import { memberRolesOf, missingRequiredMembers } from "./members"
import type { MemberRole } from "./members"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; nodeId: string; readOnly?: boolean }>(), { readOnly: false })
const emit = defineEmits<{ (event: "refresh"): void }>()
const draft = ref<MemberRole[]>([])
const busy = ref(false)
const error = ref("")
const notice = ref("")
const node = computed(() => props.snapshot?.nodes.find(item => item.id === props.nodeId && (item.type === "unit" || item.type === "energyUnit")) || null)
const missing = computed(() => missingRequiredMembers(draft.value))
const targets = computed(() => (props.snapshot?.nodes || []).filter(item => ["device", "variable", "modbusIo"].includes(item.type)).map(item => ({
  id: item.id, name: nameOf(item), kind: item.type === "device" ? "设备" : item.type === "modbusIo" ? "物理点" : "数据点",
})))
watch(() => [props.nodeId, node.value?.points] as const, () => {
  draft.value = memberRolesOf(node.value).map(member => ({ ...member }))
  error.value = ""
  notice.value = ""
}, { immediate: true, deep: true })
async function save() {
  if (!node.value) return
  const roles = draft.value.map(member => ({ ...member, role: member.role.trim(), node_id: member.node_id?.trim() || "" }))
  if (roles.some(member => !member.role)) { error.value = "成员角色名称不能为空"; return }
  if (new Set(roles.map(member => member.role.toLowerCase())).size !== roles.length) { error.value = "成员角色不能重复"; return }
  busy.value = true
  error.value = ""
  notice.value = ""
  try {
    const result = await request<{ missing_required: string[] }>(`/nodes/${encodeURIComponent(node.value.id)}`, "PATCH", { description: nameOf(node.value), members: roles })
    notice.value = result.missing_required?.length ? `草稿已保存；必需角色仍未绑定：${result.missing_required.join("、")}` : "成员角色已保存"
    emit("refresh")
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "成员绑定保存失败"
  } finally { busy.value = false }
}
</script>

<style scoped>
.member-roles { display: flex; flex-direction: column; gap: 10px; padding: 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 12px; background: rgba(255,255,255,.28); }
.panel-head { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.panel-head span { color: var(--faint); font-size: 12px; }
td input:not([type="checkbox"]), td select { width: 100%; min-width: 150px; }
.actions { display: flex; gap: 8px; flex-wrap: wrap; }
.empty { color: var(--faint); font-size: 12px; }
</style>
