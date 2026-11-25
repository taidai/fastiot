import { computed, reactive } from "vue"
import { logout, request } from "../api/client"
import { PREVIEW_MODE } from '../api/source'
import { kindOf, nameOf } from "../types/api"
import type { EmsAlarm, HistoryQueryResponse, Node, ScreenConfig, Snapshot, StateValue } from "../types/api"

export const session = reactive({
  token: PREVIEW_MODE ? "" : uni.getStorageSync("fastems-token") as string,
  snapshot: null as Snapshot | null,
  values: [] as StateValue[],
  alarms: [] as EmsAlarm[],
  quality: { channels: [] as unknown[], devices: [] as unknown[] },
  layer: "",
  loading: false,
  error: "",
  lastSync: "",
})

let timer: ReturnType<typeof setInterval> | undefined

uni.$on("session-ended", () => {
  stopPolling()
  session.token = ""
  session.snapshot = null
  session.values = []
  session.alarms = []
  session.quality = { channels: [], devices: [] }
  session.layer = ""
  session.lastSync = ""
})

export const pendingAlarms = computed(() => session.alarms.filter(alarm => alarm.active))
export const currentLayer = computed(() => {
  if (!session.snapshot) return null
  return session.snapshot.nodes.find(node => node.id === session.layer) || null
})
export const canOperate = computed(() => {
  const policy = session.snapshot?.user.policy
  return !!policy?.admin || policy?.role === "implementer" || policy?.role === "operator"
})

export function valueFor(pointId: string) {
  return session.values.find(value => value.point_id === pointId)
}

export function childrenOf(nodeId: string) {
  return session.snapshot?.nodes.filter(node => node.parent === nodeId) || []
}

export function descendantsOf(nodeId: string): Node[] {
  const result: Node[] = []
  const walk = (id: string) => {
    for (const child of childrenOf(id)) {
      result.push(child)
      walk(child.id)
    }
  }
  walk(nodeId)
  return result
}

export function layerPath(nodeId: string): Node[] {
  if (!session.snapshot) return []
  const path: Node[] = []
  let current = session.snapshot.nodes.find(node => node.id === nodeId)
  while (current) {
    path.unshift(current)
    current = session.snapshot.nodes.find(node => node.id === current?.parent)
  }
  return path
}

export function selectLayer(id: string) {
  session.layer = id
}

async function refreshSnapshot() {
  const snapshot = await request<Snapshot>("/snapshot")
  session.snapshot = snapshot
  if (!session.layer || !snapshot.nodes.some(node => node.id === session.layer)) {
    session.layer = snapshot.roots[0] || snapshot.nodes[0]?.id || ""
  }
}

async function refreshRuntime() {
  const [state, quality, alarms] = await Promise.all([
    request<{ values: StateValue[]; evaluated_at: string }>("/ems/state"),
    request<{ channels: unknown[]; devices: unknown[] }>("/ems/quality"),
    request<{ alarms: EmsAlarm[] }>("/ems/alarms"),
  ])
  session.values = state.values
  session.quality = quality
  session.alarms = alarms.alarms
  session.lastSync = state.evaluated_at
}

export async function refreshAll() {
  session.loading = true
  session.error = ""
  try {
    await Promise.all([refreshSnapshot(), refreshRuntime()])
  } catch (error) {
    session.error = error instanceof Error ? error.message : String(error)
  } finally {
    session.loading = false
  }
}

export function startPolling() {
  stopPolling()
  void refreshAll()
  timer = setInterval(() => {
    void refreshRuntime().catch(error => {
      session.error = error instanceof Error ? error.message : String(error)
    })
  }, 2000)
}

export function stopPolling() {
  if (timer) clearInterval(timer)
  timer = undefined
}

export function requireAuth() {
  if (PREVIEW_MODE) return true
  if (!session.token) {
    logout()
    return false
  }
  return true
}

export async function loadScreen(nodeId: string) {
  try {
    return await request<ScreenConfig>(`/ems/screen/${encodeURIComponent(nodeId)}`)
  } catch {
    return null
  }
}

export async function loadHistory(pointId: string, start: Date, end: Date, aggregate = "raw") {
  const query = new URLSearchParams({ type: "value", start: start.toISOString(), end: end.toISOString(), limit: "2000", aggregate })
  query.append("node_id", pointId)
  const result = await request<HistoryQueryResponse & { node_id?: string; points?: any[] }>(`/history?${query.toString()}`)
  if (Array.isArray(result.results)) return result
  return {
    ...result,
    results: [{
      node_id: result.node_id || pointId,
      records: (result.points || []).map(point => ({
        point_id: result.node_id || pointId,
        source_time: point.time,
        quality: point.dataType === 1 ? "有效" : String(point.dataType ?? "—"),
        value: point.value,
        source: point.origin || "",
        unit: point.units || point.unit || "",
      })),
      matched: result.points?.length || 0,
      returned: result.points?.length || 0,
      truncated: false,
    }],
  } as HistoryQueryResponse
}

export async function acknowledgeAlarm(id: string) {
  await request(`/ems/alarms/${encodeURIComponent(id)}/ack`, "POST", { note: "" })
  await refreshRuntime()
}

export async function closeAlarm(id: string) {
  await request(`/ems/alarms/${encodeURIComponent(id)}/close`, "POST", {})
  await refreshRuntime()
}

export { kindOf, nameOf }
