import test from 'node:test'
import assert from 'node:assert/strict'
import * as strategy from './strategy.ts'

const node = (id, parent, type = 'group', points = []) => ({ id, parent, type, points })
const rule = (id, parent, scope) => node(id, parent, 'decisionTable', [{ type: 'applied', text: JSON.stringify({ scope, table: { rows: [] } }) }])

test('notification plans preserve explicit accounts without numeric writes or output bindings', () => {
 const action={action_id:'notice',kind:'notify',notification:{recipient_ids:['chosen-account'],title:'Notice',body:'Literal text'}}
 const doc=strategy.parseDocument(JSON.stringify({scope:'self',table:{rows:[{row_id:'r',actions:[action]}]},bindings:{points:{},actions:{notice:{node_id:'unused-palette-output'}}}}))
 const payload=JSON.parse(JSON.stringify(strategy.createSimulationPayload(strategy.referencedDocument(doc),{}, {},'')))
 assert.deepEqual(payload.document.table.rows[0].actions[0],action)
 assert.deepEqual(payload.document.bindings.actions,{})
 assert.match(strategy.actionText(action,id=>id),/站内通知/)
 assert.doesNotMatch(strategy.actionText(action,id=>id),/= 0|undefined/)
})

test('mixed per-condition joins survive parse and request binding projection', () => {
 const document = strategy.parseDocument(JSON.stringify({ scope: 'self', revision: 'mixed-v1', table: { rows: [{ row_id: 'r', conditions: [{ point_id: 'a', operator: 'eq', value: 1 }, { point_id: 'b', operator: 'eq', value: 1, join: 'OR' }, { point_id: 'c', operator: 'eq', value: 1, join: 'AND' }], actions: [] }] }, bindings: { points: { a: { node_id: 'a' }, b: { node_id: 'b' }, c: { node_id: 'c' } }, actions: {} } }))
 const payload = strategy.createSimulationPayload(strategy.referencedDocument(document), { a: 1, b: 0, c: 0 }, {}, '')
 assert.deepEqual(payload.document.table.rows[0].conditions.map(condition => condition.join), [undefined, 'OR', 'AND'])
 assert.equal(payload.document.revision, 'mixed-v1')
})

test('weekday group events preserve actual selections and reject malformed values', () => {
 assert.equal(strategy.ruleWeekdayMask({ detail: { value: ['1', '3'] } }), 10)
 assert.equal(strategy.ruleWeekdayMask({ detail: { value: ['0', '1', '2', '3', '4', '5', '6'] } }), 127)
 assert.equal(strategy.ruleWeekdayMask({ detail: { value: [] } }), 0)
 assert.throws(() => strategy.ruleWeekdayMask({ detail: { value: null } }), /星期/)
})

test('restricted historical data does not mark an allowed current draft restricted', () => {
 const current = node('r', 'site', 'decisionTable', [{ type: 'ruleAccess', key: '0', text: '{"history_restricted":true,"trace_restricted":true}' }])
 assert.equal(strategy.getRuleAccess(current).history_restricted, true)
 assert.equal(strategy.getRuleAccess(current).draft_restricted, undefined)
 assert.deepEqual(strategy.getRuleAccess(), {})
})

test('invalid rule numeric input stays invalid rather than becoming zero', () => {
 assert.equal(strategy.ruleInputNumber({ detail: { value: '0' } }), 0)
 assert.equal(Number.isNaN(strategy.ruleInputNumber({ detail: { value: '' } })), true)
 assert.equal(Number.isNaN(strategy.ruleInputNumber({ detail: { value: 'NaN' } })), true)
 assert.equal(Number.isNaN(strategy.ruleInputNumber({ detail: { value: null } })), true)
 assert.throws(() => strategy.createSimulationPayload(strategy.parseDocument(), {}, {}, '', Number.NaN), /模拟/)
})

test('rule fields accept both UniApp detail and native select event values', () => {
 assert.equal(strategy.ruleInputValue({ detail: { value: '1.2' }, target: {} }), '1.2')
 assert.equal(strategy.ruleInputValue({ target: { value: 'descendants' } }), 'descendants')
 assert.equal(strategy.ruleInputValue({ detail: { value: 0 } }), '0')
})

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
