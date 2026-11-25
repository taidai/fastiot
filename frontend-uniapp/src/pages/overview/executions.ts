export type ExecutionAction = { id?: string; output?: string; target?: string; targets?: string[]; command_id?: string; canonical_status?: string; notification_id?: string; kind?: string; status: string; confirmation?: string; reason?: string }
export type Execution = { id: string; source_id: string; source_type: string; source_name: string; source_revision: string; execution_id: string; at: string; status: string; reason: string; counted: boolean; actions: ExecutionAction[] }
export type ExecutionPage = { items: Execution[]; total: number; execution_count: number; offset: number; limit: number; coverage: { complete: boolean; rules: string; machines: string; target_scope?: string }; restricted: boolean }
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
 return ({ 'waiting-hold': '持续计时中（尚未执行）', cooldown: '冷却中（尚未执行）', pending: '进行中', executing: '进行中', accepted: '已接受', submitted: '已提交（未确认）', skipped: '跳过', rejected: '已拒绝', failed: '失败', unknown: '结果未知' } as Record<string, string>)[action.status] || '结果未知'
}
