import { reactive } from 'vue'

export const previewProjects = reactive([
  { id: 'site-pump', name: '教学泵房', requiresEms: false },
  { id: 'site-energy', name: '储能站 A', requiresEms: true },
  { id: 'site-farm', name: '批量接入示例', requiresEms: true },
])
export const previewWorkspace = reactive({ projectId: 'site-pump', emsEnabled: true, revision: 0 })

export function selectPreviewProject(id: string) {
  if (!previewProjects.some(project => project.id === id)) throw new Error('未知预览项目')
  previewWorkspace.projectId = id
  previewWorkspace.revision += 1
}
export function createPreviewProject() {
  const project = { id: `site-new-${previewProjects.length}`, name: '未命名项目', requiresEms: false }
  previewProjects.push(project)
  selectPreviewProject(project.id)
  return project
}
export function setPreviewEms(enabled: boolean) { previewWorkspace.emsEnabled = enabled }
