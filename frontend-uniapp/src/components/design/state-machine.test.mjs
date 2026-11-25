import test from "node:test"
import assert from "node:assert/strict"
import { bindStateMachineDefinition, createStateMachineDefinition, executionStatusText, stateMachineSimulationPayload, stateMachineInputNumber, stateMachineCommandText } from "./state-machine.ts"

test("legal Go null arrays become editable only in a detached definition copy", () => {
  const wire = {
    initial_state: "idle", scope: "self", states: ["idle"],
    transitions: [
      { transition_id: "no-actions", from: "idle", event: "tick", to: "idle", conditions: [], actions: null },
      { transition_id: "no-guards", from: "idle", event: "start", to: "idle", conditions: null, actions: [] },
    ],
    bindings: { points: null, actions: null },
  }
  const record = { draft: wire, applied: structuredClone(wire), versions: { 1: structuredClone(wire) } }
  const before = JSON.stringify(record)
  const editable = bindStateMachineDefinition(record.draft, [])
  assert.deepEqual(editable.transitions.map(t => [t.conditions, t.actions]), [[[], []], [[], []]])
  assert.deepEqual(editable.bindings, { points: {}, actions: {} })
  editable.transitions[0].conditions.push({ point_id: "new-input", operator: "eq", value: 0 })
  assert.equal(JSON.stringify(record), before, "draft/applied/version wire must not be normalized in place")
  const empty = { ...wire, transitions: null }
  assert.deepEqual(bindStateMachineDefinition(empty, []).transitions, [])
  assert.equal(empty.transitions, null)
})

test("uni numeric input preserves the explicit command parameter instead of substituting zero", () => {
  assert.equal(stateMachineInputNumber({ detail: { value: "20" }, target: {} }), 20)
  assert.equal(stateMachineInputNumber({ target: { value: "0" } }), 0)
  assert.equal(Number.isNaN(stateMachineInputNumber({ detail: { value: "" } })), true)
  const definition = createStateMachineDefinition("permit", "pump")
  definition.transitions[0].actions[0].value = Number.NaN
  assert.throws(() => bindStateMachineDefinition(definition, []), /有效数字/)
})

test("renaming an empty state removes only generated empty lifecycle rows", () => {
  const definition = createStateMachineDefinition("", "")
  definition.states = ["idle", "active"]
  definition.initial_state = "idle"
  definition.state_actions = { stopped: { entry: [], exit: [] }, running: { entry: [{ action_id: "must-resolve", value: 1 }] } }
  const bound = bindStateMachineDefinition(definition, [])
  assert.equal(bound.state_actions.stopped, undefined)
  assert.equal(bound.state_actions.running.entry.length, 1, "never silently discard configured lifecycle effects")
})

test("declared command labels show bound parameters rather than the unused point value", () => {
  const definition = createStateMachineDefinition("", "")
  definition.command_bindings = { command: { device_id: "device", command_id: "set", parameters: { target: 20 } } }
  assert.equal(stateMachineCommandText("command", definition, id => id === "device" ? "Pump" : id), "Pump · set（target=20）")
  assert.equal(stateMachineCommandText("point", definition, id => id), undefined)
})

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

test("state entry and exit action bindings survive editing without hidden targets", () => {
  const definition = createStateMachineDefinition("", "")
  definition.state_actions = { stopped: { exit: [{ action_id: "pump", value: 0, readback: { timeout_ms: 100 } }] } }
  definition.bindings.actions.pump = { node_id: "stable-pump", point_type: "valueSet" }
  const bound = bindStateMachineDefinition(definition, [])
  assert.equal(bound.bindings.actions.pump.node_id, "stable-pump")
  assert.deepEqual(bound.state_actions, definition.state_actions)
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
