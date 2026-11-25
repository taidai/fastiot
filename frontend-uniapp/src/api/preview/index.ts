import { resolveNotificationPreview } from './notifications'
import { resolveDesignPreview, selectDesignPreviewProject } from './design'
import { resolveDeliveryPreview, setDeliveryPreviewEms, selectDeliveryPreviewProject } from './delivery'
import { previewWorkspace, previewProjects, setPreviewEms } from './workspace'
import type { Snapshot } from '../../types/api'

export type PreviewResult = { handled: boolean; status?: number; data?: unknown }

export async function resolvePreview(path: string, method: string, body?: unknown): Promise<PreviewResult> {
  const inbox = resolveNotificationPreview(path, method, body)
  if (inbox.handled) return inbox
  if (path === '/login' && method === 'POST') return { handled: true, data: { token: 'prototype-preview' } }
  const project = previewProjects.find(item => item.id === previewWorkspace.projectId)!
  selectDesignPreviewProject(project.id, project.name)
  selectDeliveryPreviewProject(project.id)
  const route = path.split('?')[0]
  if (route === '/industry/packages' && method === 'POST') setPreviewEms(true)
  if (route === '/industry/packages/ems' && method === 'DELETE') setPreviewEms(false)
  setDeliveryPreviewEms(previewWorkspace.emsEnabled)
  resolveDesignPreview(previewWorkspace.emsEnabled ? '/industry/packages' : '/industry/packages/ems', previewWorkspace.emsEnabled ? 'POST' : 'DELETE', { id: 'ems', version: '1.0.0' })
  if (route === '/industry/packages' || route === '/industry/packages/ems') return resolveDeliveryPreview(path, method, body)
  if (route === '/snapshot') {
    const result = resolveDesignPreview(path, method, body)
    const snapshot = result.data as Snapshot
    const delivery = resolveDeliveryPreview('/snapshot', 'GET').data as Snapshot
    const imports = delivery.nodes.filter(node => node.id.includes('imported') && (node.parent === project.id || node.parent === 'site-imported' || node.parent === 'channel-imported'))
    return { ...result, data: { ...snapshot, nodes: [...snapshot.nodes, ...imports] } }
  }
  // Prefer the current hash route, not a stale history pathname left by dev URLs.
  const activeRoute = typeof location === 'undefined' ? '' : location.hash || location.pathname
  if (activeRoute.includes('/pages/delivery/')) {
    const delivery = resolveDeliveryPreview(path, method, body)
    if (delivery.handled) return delivery
  }
  const design = resolveDesignPreview(path, method, body)
  if (design.handled) return design
  return resolveDeliveryPreview(path, method, body)
}
