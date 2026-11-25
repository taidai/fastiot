import { request } from './client'
import type { NotificationPage, NotificationRecipients } from '../types/notification'

export const listNotifications = (before = '') => request<NotificationPage>(`/notifications?limit=20${before ? `&before=${encodeURIComponent(before)}` : ''}`)
export const markNotificationRead = (id: string) => request<{ id: string; read_at: string }>(`/notifications/${encodeURIComponent(id)}/read`, 'POST', {})
export const notificationRecipients = (scopeNodeID: string, after = '') => request<NotificationRecipients>(`/notification-recipients?scope_node_id=${encodeURIComponent(scopeNodeID)}&limit=100${after ? `&after=${encodeURIComponent(after)}` : ''}`)
