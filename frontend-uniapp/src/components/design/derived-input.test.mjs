import test from 'node:test'
import assert from 'node:assert/strict'
import { metricForPoint } from '../../pages/overview/overview.ts'
import { buildHistoryQuery, historyValueText } from '../../pages/history/history.ts'
import { bindStateMachineDefinition } from './state-machine.ts'
import { withBindings, parseDocument, liveSimulationInputs } from './strategy.ts'

const observation = { value: 7, kind: 'number', unit: 'kW', quality: 'good', source: 'aggregate', software: true, simulated: false, time: '2026-11-25T10:00:00Z', evaluated_at: '2026-11-25T10:00:05Z', time_source: 'window_end', window: '5m', lag_seconds: 5 }
const node = { id: 'area', parent: 'site', type: 'group', points: [{ type: 'easyemsInput', text: JSON.stringify(observation) }, { type: 'easyemsAggregate', text: '{}' }] }

test('derived history displays the captured value and queries the aggregate stream', () => {
 const scope = { point: 'area', type: 'easyemsAggregateValue', start: new Date(observation.time), end: new Date(observation.evaluated_at), aggregate: 'raw' }
 assert.equal(new URLSearchParams(buildHistoryQuery(scope)).get('type'), 'easyemsAggregateValue')
 assert.equal(historyValueText({ value: 7, source: 'derived_runtime', text: JSON.stringify(observation), kind: 'observation' }), '7')
})

test('saved derived metric uses observation evidence, not scalar storage time', () => {
 const metric = metricForPoint('area', { nodes: [node] }, [])
 assert.equal(metric.value, '7')
 assert.equal(metric.unit, 'kW')
 assert.match(metric.quality, /聚合/)
 assert.match(metric.quality, /软件/)
 assert.match(metric.quality, /5m/)
 assert.match(metric.quality, /滞后/)
 const pending = { ...node, points: [{ type: 'easyemsInput', text: JSON.stringify({ ...observation, quality: 'pending', value: null, time: undefined, lag_seconds: 0 }) }] }
 assert.match(metricForPoint('area', { nodes: [pending] }, []).quality, /滞后未知/)
 const stale = { ...node, points: [{ type: 'easyemsInput', text: JSON.stringify({ ...observation, quality: 'stale', value: null }) }] }
 assert.equal(metricForPoint('area', { nodes: [stale] }, []).value, '—')
})

test('derived bindings are typed read-only decisions, never command outputs', () => {
 const result = withBindings(parseDocument(), [node])
 assert.deepEqual(result.bindings.points.area, { node_id: 'area', point_type: 'easyemsAggregateValue', use: 'decision' })
 assert.equal(result.bindings.actions.area, undefined)
 assert.deepEqual(liveSimulationInputs(result, [], [node]).inputs, { area: 7 })
 const invalid = { ...node, points: [{ type: 'easyemsInput', text: JSON.stringify({ ...observation, value: null, quality: 'invalid' }) }] }
 assert.deepEqual(liveSimulationInputs(result, [], [invalid]).inputs, {})
 const machine = bindStateMachineDefinition({ states: ['idle'], transitions: [{ conditions: [{ point_id: 'area' }], actions: [] }], bindings: { points: {}, actions: {} } }, [node])
 assert.deepEqual(machine.bindings.points.area, result.bindings.points.area)
})
