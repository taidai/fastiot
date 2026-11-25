import test from 'node:test'
import assert from 'node:assert/strict'
import * as strategy from './strategy.ts'

const node = (id, parent, type = 'group', points = []) => ({ id, parent, type, points })
const rule = (id, parent, scope) => node(id, parent, 'decisionTable', [{ type: 'applied', text: JSON.stringify({ scope, table: { rows: [] } }) }])

test('current-value simulation never invents zero or relabels invalid data as good', () => {
 const document = { table: { rows: [] }, bindings: { points: { zero: { node_id: 'p0' }, bad: { node_id: 'p1' }, absent: { node_id: 'p2' } }, actions: {} } }
 const values = [{ point_id: 'p0', quality: 'good', source_time: '2026-11-25T00:00:00Z', value: { kind: 'number', number: 0 } }, { point_id: 'p1', quality: 'invalid', value: { kind: 'number', number: 9 } }]
 assert.deepEqual(strategy.liveSimulationInputs(document, values), { inputs: { zero: 0 }, textInputs: {} })
})

test('draft payload retains only referenced bindings without rebinding stable identities', () => {
 const doc = { scope: 'self', revision: 'v1', table: { rows: [{ row_id: 'r', conditions: [{ point_id: 'x', operator: 'eq', value: 1 }], actions: [] }] }, bindings: { points: { x: { node_id: 'original' }, extra: { node_id: 'hidden' } }, actions: { unused: { node_id: 'hidden' } } } }
 const result = strategy.referencedDocument(doc)
 assert.deepEqual(result.bindings, { points: { x: { node_id: 'original' } }, actions: {} })
 assert.equal(result.revision, 'v1')
 assert.ok(doc.bindings.points.extra)
})

test('inheritance uses explicit published descendants scope and stops at visible boundary', () => {
 const nodes = [node('site', 'root'), node('area', 'site'), node('device', 'area', 'device')]
 const rules = [rule('self', 'site', 'self'), rule('inherited', 'site', 'descendants'), rule('missing', 'area', undefined), rule('own', 'device', 'self')]
 assert.deepEqual(strategy.rulesForNode(rules, nodes, 'device').map(r => r.id), ['inherited', 'own'])
 assert.deepEqual(strategy.rulesForNode(rules, nodes.slice(1), 'device').map(r => r.id), ['own'])
})
