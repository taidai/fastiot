import test from "node:test"
import assert from "node:assert/strict"
import { bindStateMachineDefinition, createStateMachineDefinition, executionStatusText, stateMachineSimulationPayload } from "./state-machine.ts"

test("state machine definition binds only supplied real inputs and outputs", () => {
  const definition = createStateMachineDefinition("permit", "pump")
  assert.equal(definition.scope, "self")
  assert.equal(definition.bindings.points.permit.node_id, "permit")
  assert.equal(definition.bindings.actions.pump.point_type, "valueSet")
  assert.equal(definition.transitions[0].actions[0].readback.timeout_ms, 5000)
  const empty = createStateMachineDefinition("", "")
  assert.deepEqual(empty.transitions, [])
})

test("bindStateMachineDefinition binds only referenced points and maps businessId to node id", () => {
  const nodes = [
    { id: "n-permit", type: "variable", parent: "site", points: [{ type: "businessId", text: "permit" }, { type: "description", text: "许可" }] },
    { id: "n-pump", type: "modbusIo", parent: "site", points: [{ type: "businessId", text: "pump" }, { type: "modbusIoType", text: "modbusCoil" }] },
    { id: "n-other-site", type: "variable", parent: "other", points: [{ type: "businessId", text: "other" }] },
  ]
  const definition = {
    initial_state: "stopped", scope: "self", states: ["stopped", "running"],
    transitions: [{ transition_id: "t", from: "stopped", event: "start", to: "running",
      conditions: [{ point_id: "permit", operator: "eq", kind: "bool", value: 1 }],
      actions: [{ action_id: "pump", value: 1, readback: { timeout_ms: 5000 } }] }],
    bindings: { points: {}, actions: {} },
  }
  const bound = bindStateMachineDefinition(definition, nodes)
  assert.deepEqual(bound.bindings.points.permit, { node_id: "n-permit", point_type: "value" })
  assert.deepEqual(bound.bindings.actions.pump, { node_id: "n-pump", point_type: "valueSet" })
  assert.equal(bound.bindings.points.other, undefined, "unreferenced points from other scopes must not be bound")
  assert.equal(bound.bindings.points["n-other-site"], undefined)
})

test("simulation payload keeps zero and execution status never calls unknown success", () => {
  const payload = stateMachineSimulationPayload("start", { permit: 0 }, { mode: "auto" }, "2026-11-23T10:00")
  assert.equal(payload.inputs.permit, 0)
  assert.equal(payload.text_inputs.mode, "auto")
  assert.equal(payload.at, "2026-11-23T10:00")
  assert.equal(executionStatusText("unknown"), "结果未知")
  assert.equal(executionStatusText("failed"), "执行失败")
  assert.equal(executionStatusText("confirmed"), "已回读确认")
})
