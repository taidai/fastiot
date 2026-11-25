export interface NotificationItem {
  id: string
  rule_id: string
  scope_node_id: string
  title: string
  body: string
  created_at: string
  read_at: string | null
}
export interface NotificationPage { items: NotificationItem[]; next_before?: string }
export interface NotificationRecipient { id: string; display_name: string }
export interface NotificationRecipients { items: NotificationRecipient[]; next_after?: string }
