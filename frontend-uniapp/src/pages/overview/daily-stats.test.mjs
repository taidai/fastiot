import test from 'node:test'
import assert from 'node:assert/strict'
import { dailyMetricText, dailyMetricDetail } from './daily-stats.ts'

test('daily values preserve zero, partial evidence and unavailable reasons', () => {
 assert.equal(dailyMetricText({ status: 'available', value: 0, unit: 'kWh' }), '0')
 assert.equal(dailyMetricText({ status: 'unavailable', value: null, unit: 'kWh', reason: 'binding_missing' }), '—')
 assert.equal(dailyMetricText({ status: 'partial', value: 13, unit: 'kWh' }), '13')
 assert.match(dailyMetricDetail({ status: 'partial', value: 13, unit: 'kWh', coverage: 0.5 }), /部分统计.*50%/)
 assert.match(dailyMetricDetail({ status: 'unavailable', value: null, reason: 'binding_missing' }), /未配置/)
 assert.equal(dailyMetricDetail({ status: 'available', value: 50, unit: '%', attempts: 2 }), '窗口内 2 次尝试')
 assert.match(dailyMetricDetail({ status: 'stale', value: 600, time_basis: 'device' }), /过期/)
})
