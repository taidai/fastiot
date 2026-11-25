import { request } from '../../api/client'
import type { DailyStats } from './daily-stats'

// No cache or browser-side accumulation. Callers fence results by layer/project.
export function loadDailyStats(layerId: string): Promise<DailyStats> {
  return request<DailyStats>(`/overview/daily-stats?layer=${encodeURIComponent(layerId)}`)
}
