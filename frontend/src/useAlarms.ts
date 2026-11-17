import { useCallback, useEffect, useState } from "react"
import { EmsAlarm, fetchAlarms, Node, Snapshot } from "./api"

export function useAlarms(token: string, nodes: Node[], policy?: Snapshot["user"]["policy"]) {
  const scope = nodes.map(n => n.id).sort().join(",")
  const key = `${token}/${scope}/${JSON.stringify(policy)}`
  const [revision, setRevision] = useState(0)
  const [result, setResult] = useState<{ key: string; alarms: EmsAlarm[]; error: string }>()
  const refresh = useCallback(async () => { setRevision(n => n + 1) }, [])
  useEffect(() => {
    let stopped = false, inFlight = false
    const controller = new AbortController(), ids = new Set(scope.split(","))
    setResult(undefined)
    if (!token) return
    async function load() {
      if (inFlight) return
      inFlight = true
      try {
        const response = await fetchAlarms(token, AbortSignal.any([controller.signal, AbortSignal.timeout(5000)]))
        const alarms = response.alarms.filter(a => [a.point_id, a.target?.node_id, ...(a.target?.point_ids || []), a.device_id].some(id => id && ids.has(id)))
        if (!stopped) setResult({ key, alarms, error: "" })
      } catch (e) {
        if (!stopped) setResult({ key, alarms: [], error: (e as Error).message })
      } finally { inFlight = false }
    }
    void load()
    const timer = window.setInterval(load, 5000)
    return () => { stopped = true; controller.abort(); window.clearInterval(timer) }
  }, [token, scope, key, revision])
  const current = result?.key === key ? result : undefined
  const ready = !!current && !current.error
  const alarms = current?.alarms || []
  const count = ready ? alarms.filter(a => a.active && !(a.maintenance_until && Date.parse(a.maintenance_until) > Date.now())).length : null
  return { alarms, count, loading: !current, error: current?.error || "", ready, refresh }
}
