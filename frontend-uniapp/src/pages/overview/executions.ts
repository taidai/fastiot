export type ExecutionAction = { id?: string; output?: string; target?: string; targets?: string[]; command_id?: string; canonical_status?: string; notification_id?: string; kind?: string; status: string; confirmation?: string; reason?: string }
export type Execution = { id: string; source_id: string; source_type: string; source_name: string; source_revision: string; execution_id: string; at: string; status: string; reason: string; counted: boolean; actions: ExecutionAction[] }
export type ExecutionPage = { items: Execution[]; total: number; execution_count: number; offset: number; limit: number; coverage: { complete: boolean; rules: string; machines: string; target_scope?: string }; restricted: boolean }

export type ExecutionQueryOptions = { date: string; timezone: string; source?: string; offset?: number; limit?: number }

// Both the sidebar summary and the full audit drawer read the same endpoint.
export function buildExecutionQuery(layerId: string, options: ExecutionQueryOptions) {
 return new URLSearchParams({
  layer: layerId,
  date: options.date,
  timezone: options.timezone,
  source: options.source || '',
  offset: String(options.offset ?? 0),
  limit: String(options.limit ?? 5),
 }).toString()
}

export function localDate(now = new Date()) {
 return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}

export function executionSummaryText(page: Pick<ExecutionPage, 'items' | 'total' | 'execution_count' | 'restricted'>) {
 const extra = page.total > page.execution_count ? `，另有 ${page.total - page.execution_count} 条未计数的旧检查` : ''
 const shown = page.items.length ? `仅显示最近 ${page.items.length} 条` : '暂无可显示的记录'
 return `今日 ${page.execution_count} 次已保留执行${extra}；${shown}，筛选与执行依据见“查看全部”。${page.restricted ? '部分历史引用已不在当前权限范围，未展示。' : ''}`
}
export function executionStatus(status: string): string {
 return ({ confirmed: '过程已确认（非设备完成）', fired: '已触发', 'already-fired': '本轮已申请', held: '跳过', skipped: '跳过', rejected: '已拒绝', failed: '失败', unknown: '结果未知', pending: '进行中', 'waiting-hold': '持续计时中', cooldown: '冷却中（跳过）', 'no-match': '未命中' } as Record<string, string>)[status] || '结果未知'
}
export function actionStatus(action: ExecutionAction): string {
 if (action.kind === 'notify' && action.status === 'created') return '通知已创建（不代表已读）'
 if (action.status === 'confirmed') {
  if (action.confirmation === 'software') return '软件确认（非物理回读）'
  if (action.confirmation === 'device_readback' || action.confirmation === 'device') return '设备回读确认'
  return '已确认（确认类型未提供）'
 }
 return ({ 'waiting-hold': '持续计时中（尚未执行）', cooldown: '冷却中（尚未执行）', pending: '进行中', 'waiting-delay': '进行中（等待延时）', 'waiting-readback': '进行中（等待回读）', executing: '进行中', accepted: '已接受', submitted: '已提交（未确认）', skipped: '跳过', rejected: '已拒绝', failed: '失败', unknown: '结果未知' } as Record<string, string>)[action.status] || '结果未知'
}
