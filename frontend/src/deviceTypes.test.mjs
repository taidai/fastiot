import test from 'node:test'
import assert from 'node:assert/strict'
import { isTerminalCommand } from './deviceTypes.ts'

test('command consumers stop polling rejected and other canonical terminal outcomes', () => {
  assert.equal(isTerminalCommand('rejected'), true)
  for (const state of ['confirmed', 'failed', 'unknown', 'submitted', 'superseded', 'timeout']) assert.equal(isTerminalCommand(state), true, state)
  for (const state of ['accepted', 'executing']) assert.equal(isTerminalCommand(state), false, state)
})
