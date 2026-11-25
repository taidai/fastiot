import test from 'node:test'
import assert from 'node:assert/strict'
import { previewWorkspace, previewProjects, selectPreviewProject, createPreviewProject, setPreviewEms } from '../../api/preview/workspace.ts'

test('project menu switches known projects without resetting industry state', () => {
  assert.deepEqual(previewProjects.slice(0, 3).map(p => p.name), ['教学泵房', '储能站 A', '批量接入示例'])
  setPreviewEms(false)
  selectPreviewProject('site-energy')
  assert.equal(previewWorkspace.projectId, 'site-energy')
  assert.equal(previewWorkspace.emsEnabled, false)
  assert.equal(previewProjects.find(p => p.id === previewWorkspace.projectId).requiresEms, true)
  assert.throws(() => selectPreviewProject('missing'))
  selectPreviewProject('site-pump')
  setPreviewEms(true)
})

test('new project is empty and receives a distinct id', () => {
  const first = createPreviewProject()
  const second = createPreviewProject()
  assert.notEqual(first.id, second.id)
  assert.equal(second.name, '未命名项目')
  assert.equal(second.requiresEms, false)
  assert.equal(previewWorkspace.projectId, second.id)
  selectPreviewProject('site-pump')
})
