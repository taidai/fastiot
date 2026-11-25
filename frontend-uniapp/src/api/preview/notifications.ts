import type { NotificationItem } from '../../types/notification'
import type { PreviewResult } from './index'

// Module-local fixture only: reloading resets reads; never consult a live token/API.
const items: NotificationItem[] = [{ id: 'preview-notice-1', rule_id: 'preview-rule', scope_node_id: 'site-demo', title: '模拟通知：请检查设备状态', body: '这是隔离预览中的站内记录，未向真实账号发送通知。', created_at: '2026-11-25T08:00:00Z', read_at: null }]
export function resolveNotificationPreview(path: string, method: string, body?: unknown): PreviewResult {
  const url = new URL(path, 'https://preview.invalid')
  if (url.pathname === '/notifications' && method === 'GET') {
    const limit = Number(url.searchParams.get('limit') || 20)
    let invalid = false
    url.searchParams.forEach((_, key) => { if (!['limit', 'before'].includes(key)) invalid = true })
    if (invalid || !Number.isInteger(limit) || limit < 1 || limit > 100) return { handled: true, status: 400, data: { error: 'invalid_notification_query' } }
    const before = url.searchParams.get('before')
    const start = before ? items.findIndex(item => item.id === before) + 1 : 0
    const page = before && start === 0 ? [] : items.slice(start, start + limit)
    return { handled: true, data: { items: page.map(item => ({ ...item })), ...(start + limit < items.length ? { next_before: page[page.length - 1]?.id } : {}) } }
  }
  const match = url.pathname.match(/^\/notifications\/([^/]+)\/read$/)
  if (match && method === 'POST') {
    if (url.search || !body || typeof body !== 'object' || Array.isArray(body) || Object.keys(body).length) return { handled: true, status: 400, data: { error: 'invalid_json' } }
    const item = items.find(item => item.id === match[1])
    if (!item) return { handled: true, status: 403, data: { error: 'notification_forbidden' } }
    item.read_at ||= new Date().toISOString()
    return { handled: true, data: { id: item.id, read_at: item.read_at } }
  }
  if (url.pathname === '/notification-recipients' && method === 'GET') {
    if (!url.searchParams.get('scope_node_id')) return { handled: true, status: 400, data: { error: 'invalid_notification_query' } }
    return { handled: true, data: { items: [{ id: 'preview-account', display_name: '预览接收账号' }] } }
  }
  return { handled: false }
}
