import test from 'node:test'
import assert from 'node:assert/strict'
import { projectOverview } from './project-overview.ts'

test('energy overview uses selected project ledger instead of pump-room fixtures', () => {
  const node = (id, type, parent, name) => ({ id, type, parent, points: [{ type: 'description', text: name }] })
  const result = projectOverview({ roots: ['energy'], nodes: [node('energy','site','root','储能站 A'),node('ess','energyUnit','energy','储能1'),node('pcs','device','ess','PCS1')] }, '储能站 A', true)
  assert.equal(result.layers[0].name, '储能站 A')
  assert.equal(result.data.energy.title, '储能站 A')
  assert.ok(result.data.energy.kpis.some(k => k[0] === '储能 SOC'))
  assert.equal(result.devices[0][0], 'PCS1')
  assert.equal(result.flow[1].label, '并网点 PCC')
  assert.ok(!JSON.stringify(result).includes('1号泵'))
})
