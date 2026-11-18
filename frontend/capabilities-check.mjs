import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import ts from "typescript"

const source = readFileSync(new URL("./src/capabilities.ts", import.meta.url), "utf8")
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } })
const { Actions, capability, hasAction, reasonText } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`)

const implementer = new Set([Actions.configure, Actions.export, Actions.writeValue])
assert.equal(capability(implementer, Actions.export).allowed, true)
assert.equal(capability(implementer, Actions.accountManage).reason, "not_granted")
assert.equal(reasonText("not_granted"), "当前账号没有此操作权限")
assert.equal(capability(implementer, Actions.writeValue, { disabled: true }).reason, "target_disabled")
assert.equal(capability(implementer, Actions.writeValue, { readOnly: true }).reason, "read_only")
assert.equal(capability(implementer, Actions.writeValue, { serverAvailable: false }).reason, "server_unavailable")
assert.equal(capability(implementer, Actions.command, { serverAvailable: false }).reason, "not_granted")
assert.equal(capability(undefined, Actions.export).allowed, false)
assert.equal(hasAction(undefined, Actions.export), false)

const whitelistedExportOnly = new Set([Actions.export])
assert.equal(capability(whitelistedExportOnly, Actions.export).allowed, true)
assert.equal(capability(whitelistedExportOnly, Actions.writeValue).allowed, false)

const operator = new Set([Actions.writeValue, Actions.command, Actions.alarmAck])
assert.equal(capability(operator, Actions.writeValue).allowed, true)
assert.equal(capability(operator, Actions.configure).allowed, false)

console.log("Capability checks passed: action grants, target disabled/read-only, server availability and refusal reasons")
