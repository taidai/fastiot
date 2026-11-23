<template>
  <section class="card glass">
    <view class="step-head">
      <view><b>账号与权限</b><span>角色决定默认动作，白名单决定显式边界。</span></view>
      <button v-if="canManage" class="primary" @click="creating = !creating">{{ creating ? "收起" : "＋ 创建账号" }}</button>
    </view>
    <view v-if="!canManage" class="empty">当前账号没有账号管理权限；服务端会拒绝读取或修改账号。</view>
    <template v-else>
      <form v-if="creating" class="editor create-editor" @submit.prevent="createAccount">
        <view class="form-grid">
          <label>登录账号<input v-model="newEmail" type="email" required autocomplete="off" placeholder="name@example.com" /></label>
          <label>初始密码<input v-model="newPassword" type="password" required minlength="12" autocomplete="new-password" placeholder="至少 12 位" /></label>
          <label>角色<select v-model="newRole"><option v-for="role in roles" :key="role" :value="role">{{ roleText(role) }}</option></select></label>
          <label>可见与操作范围<select v-model="newParent"><option v-for="scope in scopes" :key="scope.id" :value="scope.id">{{ scope.label }}</option></select></label>
        </view>
        <fieldset><legend>动作白名单（留空 = 按角色默认）</legend><checkbox-group class="action-grid" @change="newActions = $event.detail.value"><label v-for="action in actionOptions" :key="action"><checkbox :value="action" :checked="newActions.includes(action)" color="#0c7d84" />{{ actionText(action) }}</label></checkbox-group></fieldset>
        <button class="primary" type="button" :disabled="busy === 'create' || !newParent" @click="createAccount">{{ busy === "create" ? "创建中…" : "创建账号" }}</button>
      </form>
      <view v-if="message" class="success">{{ message }}</view>
      <view v-if="operationError" class="error">{{ operationError }}</view>
      <view v-if="loadError" class="error">{{ loadError }}</view>
      <view v-else-if="loading && !users.length" class="empty">正在读取账号…</view>
      <view v-else-if="!users.length" class="empty">后端没有返回账号。</view>
      <view v-else class="account-list">
        <view v-for="user in users" :key="user.id" class="account">
          <view class="account-head">
            <view><b>{{ user.email || user.id }}</b><span>{{ roleText(user.policy.role) }}<template v-if="user.policy.admin"> · 管理员</template></span></view>
            <button class="ghost" @click="openEdit(user)">{{ editingId === user.id ? "收起" : "编辑权限" }}</button>
          </view>
          <view class="account-meta"><span>范围：{{ scopeNames(user) }}</span><span>{{ user.policy.actions?.length ? `白名单 ${user.policy.actions.length} 项` : "按角色默认" }}</span></view>
          <view class="effective">有效动作：{{ effectiveActions(user) }}</view>
          <form v-if="editingId === user.id" class="editor" @submit.prevent="savePolicy(user.id)">
            <label>角色<select v-model="editRole"><option v-for="role in roles" :key="role" :value="role">{{ roleText(role) }}</option></select></label>
            <fieldset><legend>动作白名单（留空 = 按角色默认）</legend><checkbox-group class="action-grid" @change="editActions = $event.detail.value"><label v-for="action in actionOptions" :key="action"><checkbox :value="action" :checked="editActions.includes(action)" color="#0c7d84" />{{ actionText(action) }}</label></checkbox-group></fieldset>
            <view class="actions"><button class="primary" type="button" :disabled="busy === user.id" @click="savePolicy(user.id)">{{ busy === user.id ? "保存中…" : "保存权限" }}</button><button class="ghost" type="button" @click="editingId = ''">取消</button></view>
          </form>
        </view>
      </view>
      <p class="note">范围在创建时绑定到项目根或 Site；现有接口不支持编辑范围，因此这里不伪装可修改。每个请求仍由服务端校验动作与树范围。</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import { request } from "../../api/client"
import { nameOf } from "../../types/api"
import type { Node } from "../../types/api"
import { actionText, deliveryError, resolveActions, roleText } from "./contracts"
import type { AccessPolicy } from "./contracts"

type UserAccount = { id: string; email: string; policy: AccessPolicy }
const props = defineProps<{ rootId: string; nodes: Node[]; canManage: boolean }>()
const roles = ["admin", "implementer", "operator", "viewer"]
const users = ref<UserAccount[]>([])
const actionOptions = ref<string[]>([])
const loading = ref(false)
const loadError = ref("")
const operationError = ref("")
const message = ref("")
const busy = ref("")
const creating = ref(false)
const newEmail = ref("")
const newPassword = ref("")
const newRole = ref("viewer")
const newParent = ref("")
const newActions = ref<string[]>([])
const editingId = ref("")
const editRole = ref("viewer")
const editActions = ref<string[]>([])

const scopes = computed(() => {
  const root = props.nodes.find(node => node.id === props.rootId)
  return [
    ...(props.rootId ? [{ id: props.rootId, label: `整个项目${root ? ` · ${nameOf(root)}` : ""}` }] : []),
    ...props.nodes.filter(node => node.type === "site").map(node => ({ id: node.id, label: `Site · ${nameOf(node)}` })),
  ]
})
watch(() => props.rootId, id => { if (id && !newParent.value) newParent.value = id }, { immediate: true })
watch(() => props.canManage, allowed => { if (allowed) void load() })

async function load() {
  if (!props.canManage) return
  loading.value = true
  loadError.value = ""
  try {
    const result = await request<{ users: UserAccount[]; actions: string[] }>("/users")
    users.value = result.users || []
    actionOptions.value = result.actions || []
  } catch (error) {
    users.value = []
    loadError.value = deliveryError(error)
  } finally {
    loading.value = false
  }
}
function scopeNames(user: UserAccount) {
  const ids = user.policy.root_node_ids || []
  if (!ids.length) return "—"
  return ids.map(id => {
    const node = props.nodes.find(item => item.id === id)
    return node ? nameOf(node) : id
  }).join("、")
}
function effectiveActions(user: UserAccount) {
  const actions = resolveActions(user.policy)
  return actions.length ? actions.map(actionText).join("、") : "无写动作"
}
function openEdit(user: UserAccount) {
  if (editingId.value === user.id) { editingId.value = ""; return }
  editingId.value = user.id
  editRole.value = user.policy.role || "viewer"
  editActions.value = [...(user.policy.actions || [])]
  operationError.value = ""
  message.value = ""
}
async function createAccount() {
  operationError.value = ""
  message.value = ""
  if (!newParent.value) { operationError.value = "请选择账号范围"; return }
  if (newPassword.value.length < 12) { operationError.value = "初始密码至少 12 位"; return }
  busy.value = "create"
  try {
    await request("/users", "POST", { parent: newParent.value, email: newEmail.value.trim(), password: newPassword.value, role: newRole.value, actions: newActions.value })
    message.value = "账号已创建"
    newEmail.value = ""; newPassword.value = ""; newActions.value = []; creating.value = false
    await load()
  } catch (error) { operationError.value = deliveryError(error) } finally { busy.value = "" }
}
async function savePolicy(id: string) {
  busy.value = id
  operationError.value = ""
  message.value = ""
  try {
    await request(`/users/${encodeURIComponent(id)}`, "PUT", { role: editRole.value, actions: editActions.value })
    message.value = "权限已保存"
    editingId.value = ""
    await load()
  } catch (error) { operationError.value = deliveryError(error) } finally { busy.value = "" }
}
onMounted(() => { if (props.canManage) void load() })
</script>

<style scoped>
.step-head, .account-head, .account-meta { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.step-head { margin-bottom: 12px; }
.step-head > view { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.step-head b { font-size: 16px; }
.step-head span, .note { color: var(--faint); font-size: 12px; }
.editor { margin: 12px 0; padding: 12px; border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.35); }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.editor label { display: grid; gap: 5px; color: var(--muted); font-size: 12px; }
.editor input:not([type="checkbox"]), .editor select { width: 100%; min-height: 44px; padding: 7px 10px; border: 1px solid var(--line); border-radius: 9px; background: rgba(255,255,255,.75); }
fieldset { margin: 12px 0; padding: 0; border: 0; }
legend { margin-bottom: 6px; color: var(--faint); font-size: 11.5px; }
.action-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 4px; }
.action-grid label { display: flex; align-items: center; gap: 6px; min-height: 44px; padding: 4px; }
.action-grid checkbox { width: 17px; height: 17px; }
.account-list { display: grid; gap: 9px; }
.account { padding: 12px; border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.3); }
.account-head > view { display: grid; gap: 4px; }
.account-head b { font-size: 13px; overflow-wrap: anywhere; }
.account-head span, .account-meta, .effective { color: var(--faint); font-size: 11.5px; line-height: 1.7; }
.account-meta { align-items: flex-start; flex-wrap: wrap; margin-top: 6px; }
.effective { margin-top: 3px; overflow-wrap: anywhere; }
.success { margin: 10px 0; padding: 10px 12px; border-radius: 10px; background: rgba(230,243,238,.8); color: var(--mint); font-size: 12.5px; }
.error { margin: 10px 0; }
.note { line-height: 1.7; }
@media (max-width: 900px) { .action-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 560px) { .form-grid { grid-template-columns: 1fr; } .step-head { align-items: flex-start; flex-direction: column; } }
</style>
