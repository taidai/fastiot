import test from "node:test"
import assert from "node:assert/strict"
import { memberRolesOf, missingRequiredMembers } from "./members.ts"

test("memberRolesOf reads persisted roles and ignores malformed JSON", () => {
  const node = { id: "unit-1", type: "unit", parent: "area-1", points: [{ type: "easyemsUnitMembers", text: JSON.stringify([{ role: "驱动", node_id: "device-1", required: true }]) }] }
  assert.deepEqual(memberRolesOf(node), [{ role: "驱动", node_id: "device-1", required: true }])
  assert.deepEqual(memberRolesOf({ ...node, points: [{ type: "easyemsUnitMembers", text: "broken" }] }), [])
})

test("missingRequiredMembers reports only unbound required roles", () => {
  assert.deepEqual(missingRequiredMembers([
    { role: "驱动", required: true },
    { role: "压力", node_id: "point-1", required: true },
    { role: "可选测量", required: false },
  ]), ["驱动"])
})
