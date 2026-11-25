export type DailyMetric = {
  status: 'available' | 'partial' | 'stale' | 'unavailable'; value: number | null; unit?: string; reason?: string
  coverage?: number; covered_seconds?: number; source?: string; point_id?: string; resets?: number
  observed_at?: string; time_basis?: string; history_lifetime?: string
  retention?: { kind: string; limit?: number } | 'process_memory_partial_after_restart'
  attempts?: number; successes?: number; start?: string; end?: string
}
export type DailyStats = {
  layer_id: string; as_of: string; config_version: number
  window: { date: string; time_zone: string; start: string; end: string; through: string } | null
  energy: DailyMetric; runtime: DailyMetric; freshness: DailyMetric; communication: DailyMetric
}
export function dailyMetricText(metric?: DailyMetric): string {
  return metric?.value != null && Number.isFinite(metric.value) ? Number(metric.value.toFixed(2)).toString() : '—'
}
export function dailyMetricDetail(metric?: DailyMetric): string {
  if (!metric) return '未读取'
  if (metric.status === 'partial') return `部分统计 · 有效覆盖 ${Number(((metric.coverage || 0) * 100).toFixed(1))}%`
  if (metric.status === 'stale') return '观测已过期'
  if (metric.status === 'available' && metric.attempts != null) return `窗口内 ${metric.attempts} 次尝试`
  if (metric.status === 'available') return metric.coverage != null && metric.unit !== 's' ? '有效覆盖 100%' : '有效观测'
  const reasons: Record<string, string> = {
    binding_missing: '未配置', no_valid_intervals: '无有效统计区间', no_observations: '无观测',
    no_attempts: '窗口内无通信记录', communication_source_unavailable: '通信记录不可用',
    history_limit_exceeded: '记录超出查询上限', future_observation: '观测时间异常',
    invalid_or_untrusted_observation: '观测无效或时间依据不足',
  }
  return reasons[metric.reason || ''] || '统计不可用'
}
