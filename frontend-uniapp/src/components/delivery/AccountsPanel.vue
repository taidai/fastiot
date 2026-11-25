<template>
  <view class="variant-head"><b>账号与权限</b><span>谁能做什么？</span></view>
  <section class="card glass accounts-card">
    <view v-if="!canManage" class="empty">当前账号没有账号管理权限；服务端会拒绝读取或修改账号。</view>
    <template v-else>
      <view v-if="message" class="success">{{ message }}</view>
      <view v-if="operationError" class="error">{{ operationError }}</view>
      <view v-if="loadError" class="error">{{ loadError }}</view>
      <view v-else-if="loading && !users.length" class="empty">正在读取账号…</view>
      <view v-else-if="!users.length" class="empty">后端没有返回账号。</view>
      <view v-else class="table-wrap"><table class="account-table"><thead><tr><th>人员</th><th>角色</th><th>工程权限</th><th>可操作对象</th><th></th></tr></thead>
        <tbody><tr v-for="user in users" :key="user.id">
          <td><b class="account-name">{{ user.email || user.id }}</b></td>
          <td>{{ roleText(user.policy.role) }}<template v-if="user.policy.admin"> · 管理员</template></td>
          <td>{{ allowed(user, "configure") ? "可修改" : "—" }}<small>{{ user.policy.actions?.length ? `白名单 ${user.policy.actions.length} 项` : "按角色默认" }}</small></td>
          <td>{{ scopeNames(user) }}</td>
          <td class="row-actions"><button class="ghost" @click="openEdit(user)">{{ editingId === user.id ? "收起" : "编辑权限" }}</button></td>
        </tr></tbody>
      </table></view>
      <button v-if="canManage" class="tag create-toggle" @click="creating = !creating">{{ creating ? "收起" : "＋ 创建账号" }}</button>
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
      <form v-if="editingUser" class="editor" @submit.prevent="savePolicy(editingUser.id)">
        <view class="card-title">编辑权限 <span>{{ editingUser.email || editingUser.id }}</span></view>
        <label>角色<select v-model="editRole"><option v-for="role in roles" :key="role" :value="role">{{ roleText(role) }}</option></select></label>
        <fieldset><legend>动作白名单（留空 = 按角色默认）</legend><checkbox-group class="action-grid" @change="editActions = $event.detail.value"><label v-for="action in actionOptions" :key="action"><checkbox :value="action" :checked="editActions.includes(action)" color="#0c7d84" />{{ actionText(action) }}</label></checkbox-group></fieldset>
        <view class="actions"><button class="primary" type="button" :disabled="busy === editingUser.id" @click="savePolicy(editingUser.id)">{{ busy === editingUser.id ? "保存中…" : "保存权限" }}</button><button class="ghost" type="button" @click="editingId = ''">取消</button></view>
      </form>
      <p class="note">范围在创建时绑定到项目根或 Site；现有接口不支持编辑范围，因此这里不伪装可修改。每个请求仍由服务端校验动作与树范围。</p>
    </template>
  </section>
  <section v-if="canManage && users.length" class="card glass matrix-card">
    <h5>角色 × 动作 × 对象</h5>
    <div class="matrix-wrap">
      <table class="permission-matrix"><thead><tr><th>账号</th><th>角色</th><th>对象范围</th><th>查看总览</th><th>启动 / 停止</th><th>修改点位</th><th>发布版本</th><th>账号管理</th><th>审计查看</th></tr></thead>
        <tbody><tr v-for="user in users" :key="`matrix-${user.id}`">
          <td>{{ user.email || user.id }}</td><td>{{ roleText(user.policy.role) }}</td><td>{{ scopeNames(user) }}</td>
          <td :class="canView(user) ? 'allowed' : 'denied'">{{ canView(user) ? "允许" : "拒绝" }}</td>
          <td v-for="column in matrixActions" :key="column.key" :class="allowed(user, column.action) ? 'allowed' : 'denied'">{{ allowed(user, column.action) ? "允许" : "拒绝" }}</td>
        </tr></tbody>
      </table>
    </div>
    <template v-if="PREVIEW_MODE">
      <view class="actions"><button class="ghost" @click="deliveryPreviewState.permissionDemo = !deliveryPreviewState.permissionDemo">{{ deliveryPreviewState.permissionDemo ? "收起李工检查" : "以李工身份检查" }}</button></view>
      <view v-if="deliveryPreviewState.permissionDemo" class="permission-exercise">
        <view class="row">启动 2号泵：<span class="chip" :class="permissionCheck.start.startsWith('允许') ? 'ok' : 'warn'">{{ permissionCheck.start.startsWith('允许') ? '允许 · 对象在泵房内' : '拒绝 · 动作或对象范围未授权' }}</span></view>
        <view class="row">修改 1号泵 点位：<span class="chip" :class="permissionCheck.editPoints === '允许' ? 'ok' : 'warn'">{{ permissionCheck.editPoints === '允许' ? '允许 · 已授予工程权限' : '拒绝 · 操作员无工程权限' }}</span></view>
        <view class="row">发布版本：<span class="chip" :class="permissionCheck.publish === '允许' ? 'ok' : 'warn'">{{ permissionCheck.publish === '允许' ? '允许 · 已授予发布权限' : '拒绝 · 操作员无发布权限' }}</span></view>
        <p class="note">原型权限演练：读取当前李工权限，不切换登录身份，不发送命令。</p>
      </view>
    </template>
    <p class="note">矩阵使用后端返回的每个账号、角色、白名单和 root_node_ids 计算；不模拟他人身份，服务端仍逐请求校验对象范围。</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import { request } from "../../api/client"
import { PREVIEW_MODE } from "../../api/source"
import { deliveryPermissionCheck, deliveryPreviewState } from "../../api/preview/delivery"
import { nameOf } from "../../types/api"
import type { Node } from "../../types/api"
import { actionText, deliveryError, resolveActions, roleText } from "./contracts"
import type { AccessPolicy } from "./contracts"

type UserAccount = { id: string; email: string; policy: AccessPolicy }
const props = defineProps<{ rootId: string; nodes: Node[]; canManage: boolean }>()
const roles = ["admin", "implementer", "operator", "viewer"]
const matrixActions = [
  { key: "command", action: "command" },
  { key: "configure", action: "configure" },
  { key: "version_publish", action: "configure" },
  { key: "account_manage", action: "account_manage" },
  { key: "audit_view", action: "audit_view" },
]
const users = ref<UserAccount[]>([])
const permissionCheck = computed(() => {
  const policy = users.value.find(user => user.id === "u-li")?.policy
  return deliveryPermissionCheck(policy ? { ...policy, actions: policy.actions || [], root_node_ids: policy.root_node_ids || [] } : undefined)
})
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
const editingUser = computed(() => users.value.find(user => user.id === editingId.value) || null)

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
function allowed(user: UserAccount, action: string) { return resolveActions(user.policy).includes(action) }
function canView(user: UserAccount) { return user.policy.admin || (user.policy.root_node_ids || []).length > 0 }
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
.variant-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; margin: 0; }
.variant-head b { font-size: 15px; }
.variant-head span, .note { color: var(--faint); font-size: 12.5px; }
.create-toggle { margin-top: 12px; }
.editor { margin: 12px 0; padding: 12px; border: 1px solid var(--line); border-radius: 12px; background: rgba(255,255,255,.35); }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.editor label { display: grid; gap: 5px; color: var(--muted); font-size: 12px; }
.editor input:not([type="checkbox"]), .editor select { width: 100%; min-height: 44px; padding: 7px 10px; border: 1px solid var(--line); border-radius: 9px; background: rgba(255,255,255,.75); }
fieldset { margin: 12px 0; padding: 0; border: 0; }
legend { margin-bottom: 6px; color: var(--faint); font-size: 11.5px; }
.action-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 4px; }
.action-grid label { display: flex; align-items: center; gap: 6px; min-height: 44px; padding: 4px; }
.action-grid checkbox { width: 17px; height: 17px; }
.account-table { min-width: 720px; }
.account-name { display: block; font-weight: 500; overflow-wrap: anywhere; }
.account-table small { display: block; margin-top: 3px; color: var(--faint); font-size: 10.5px; }
.account-table .row-actions { text-align: right; white-space: nowrap; }
.success { margin: 10px 0; padding: 10px 12px; border-radius: 10px; background: rgba(230,243,238,.8); color: var(--mint); font-size: 12.5px; }
.error { margin: 10px 0; }
.note { line-height: 1.7; }
.card-title { margin-bottom: 8px; font-size: 12px; font-weight: 600; }
.matrix-wrap { max-width: 100%; overflow-x: auto; border: 1px solid var(--line); border-radius: 10px; }
.permission-matrix { min-width: 920px; font-size: 11.5px; }
.permission-matrix td { vertical-align: top; }
.permission-matrix td:first-child, .permission-matrix td:nth-child(3) { max-width: 180px; overflow-wrap: anywhere; }
.permission-matrix .allowed { color: var(--mint); }
.permission-matrix .denied { color: var(--danger); }
@media (max-width: 900px) { .action-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 560px) { .form-grid { grid-template-columns: 1fr; } }
</style>
