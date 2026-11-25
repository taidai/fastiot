import test from 'node:test'
import assert from 'node:assert/strict'
import { executionStatus, actionStatus } from './executions.ts'

test('producer delay and readback waits remain known progress, not unknown outcomes', () => {
 assert.equal(actionStatus({ status: 'waiting-delay' }), '进行中（等待延时）')
 assert.equal(actionStatus({ status: 'waiting-readback' }), '进行中（等待回读）')
 assert.equal(actionStatus({ status: 'unknown' }), '结果未知')
 assert.equal(actionStatus({ status: 'rejected' }), '已拒绝')
})

test('execution labels never promote notification creation or software confirmation to physical completion', () => {
 assert.equal(executionStatus('unknown'), '结果未知')
 assert.equal(executionStatus('confirmed'), '过程已确认（非设备完成）')
 assert.equal(actionStatus({ status: 'created', confirmation: 'durable_record', kind: 'notify' }), '通知已创建（不代表已读）')
 assert.equal(actionStatus({ status: 'confirmed', confirmation: 'software' }), '软件确认（非物理回读）')
 assert.equal(actionStatus({ status: 'confirmed', confirmation: 'device_readback' }), '设备回读确认')
 assert.equal(actionStatus({ status: 'confirmed' }), '已确认（确认类型未提供）')
 assert.equal(actionStatus({ status: 'future-status' }), '结果未知')
})
