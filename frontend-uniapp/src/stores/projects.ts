import { computed, reactive } from 'vue'
import { ApiError, request } from '../api/client'

export type Project = {
  id: string
  root_id: string
  name: string
  scope_root_ids: string[]
  runtime: { kind: 'local'; root_id: string }
  sites: { id: string; name: string; latest_version: string | null }[]
  dependencies: { kind: string; id: string; version: string }[]
}
export const projectContext = reactive({ id: '', user: '', layer: '', generation: 0, projects: [] as Project[] })
export const currentProject = computed(() => projectContext.projects.find(project => project.id === projectContext.id))
let loading: Promise<void> | undefined
let ownerToken = ''

function storageKey() { return `fastiot-project-context:${projectContext.user}` }
function persist() {
  if (projectContext.user) uni.setStorageSync(storageKey(), { project: projectContext.id, layer: projectContext.layer })
}
function activate(id: string, layer = '') {
  projectContext.id = id
  projectContext.layer = layer
  // Synchronous generation subscribers must see the completed transition.
  projectContext.generation++
  uni.$emit('project-context-changed')
}
export function resetProjectContext() {
  loading = undefined
  // Establish ownership before notifying readers, including reentrant ones.
  ownerToken = (uni.getStorageSync('fastems-token') || '') as string
  projectContext.projects = []
  projectContext.user = ''
  activate('')
}

// The directory is authoritative. Browser memory is only a preference, never permission.
export async function ensureProjectContext() {
  const token = (uni.getStorageSync('fastems-token') || '') as string
  if (!token) throw new ApiError('会话已改变，请重新登录', 401, 'session_changed')
  if (ownerToken !== token) resetProjectContext()
  if (projectContext.id) return
  if (!loading) {
    const generation = projectContext.generation
    const pending = (async () => {
      const directory = await request<{ user_id: string; projects: Project[] }>('/projects')
      if (generation !== projectContext.generation) throw new ApiError('项目上下文已改变，请重新读取', 409, 'project_context_changed')
      projectContext.user = directory.user_id
      projectContext.projects = directory.projects
      const saved = uni.getStorageSync(storageKey()) as { project?: string; layer?: string } | undefined
      const selected = directory.projects.find(project => project.id === saved?.project) || directory.projects[0]
      if (!selected) throw new ApiError('没有可访问的项目', 403, 'no_project_access')
      activate(selected.id, selected.id === saved?.project ? saved.layer || '' : '')
      persist()
    })()
    loading = pending
    pending.finally(() => { if (loading === pending) loading = undefined }).catch(() => {})
  }
  await loading
}

export function rememberLayer(id: string) {
  if (!projectContext.id) return
  projectContext.layer = id
  persist()
}

// Caller reloads the current workbench after this returns true: no old editor is reused.
// Confirmation is intentionally conservative, including editors without dirty tracking.
export async function selectProject(id: string, confirmDiscard: () => Promise<boolean>) {
  if (id === projectContext.id || !projectContext.projects.some(project => project.id === id)) return false
  const generation = projectContext.generation
  if (!await confirmDiscard() || generation !== projectContext.generation) return false
  activate(id)
  persist()
  return true
}
