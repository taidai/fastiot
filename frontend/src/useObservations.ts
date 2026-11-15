import { useEffect, useState } from "react"
import { fetchEmsState, type EmsStateValue } from "./api"
import { observationKey } from "./observations"

type ObservationState = {
  token: string
  values: Record<string, EmsStateValue>
  loading: boolean
  error: string
  evaluatedAt: string
  startedAt: number
}

// Request timeout limits transport wait; freshness comes from server bounds.
export function useObservations(token: string): Omit<ObservationState, "token" | "startedAt"> & { elapsedMs: number } {
  const [state, setState] = useState<ObservationState>({ token, values: {}, loading: Boolean(token), error: "", evaluatedAt: "", startedAt: 0 })
  const [, tick] = useState(0)
  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof window.setTimeout> | undefined
    let controller: AbortController | undefined
    setState({ token, values: {}, loading: Boolean(token), error: "", evaluatedAt: "", startedAt: 0 })
    if (!token) return
    const clock = window.setInterval(() => tick(value => value + 1), 1000)
    const load = async () => {
      const startedAt = performance.now()
      controller = new AbortController()
      const deadline = window.setTimeout(() => controller?.abort(), 5000)
      try {
        const result = await fetchEmsState(token, controller.signal)
        if (!cancelled) setState({
          token, values: Object.fromEntries((result.values || []).map(value => [observationKey(value.device_id, value.point_id), value])),
          loading: false, error: "", evaluatedAt: result.evaluated_at, startedAt,
        })
      } catch (cause) {
        if (!cancelled) setState(previous => ({
          token, values: previous.token === token ? previous.values : {}, loading: false,
          evaluatedAt: previous.token === token ? previous.evaluatedAt : "",
          startedAt: previous.token === token ? previous.startedAt : 0,
          error: cause instanceof Error && cause.name === "AbortError" ? "状态读取超时" : cause instanceof Error ? cause.message : "状态读取失败",
        }))
      } finally {
        window.clearTimeout(deadline)
        if (!cancelled) timer = window.setTimeout(() => void load(), 2000)
      }
    }
    void load()
    return () => {
      cancelled = true
      window.clearTimeout(timer)
      window.clearInterval(clock)
      controller?.abort()
    }
  }, [token])
  // Ignore browser wall-clock changes and include transport time in cache age.
  return state.token === token
    ? { values: state.values, loading: state.loading, error: state.error, evaluatedAt: state.evaluatedAt, elapsedMs: Math.max(0, performance.now() - state.startedAt) }
    : { values: {}, loading: Boolean(token), error: "", evaluatedAt: "", elapsedMs: 0 }
}
