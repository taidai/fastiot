import assert from "node:assert/strict"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import ts from "typescript"

const source = readFileSync(new URL("./src/capabilities.ts", import.meta.url), "utf8")
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } })
const { Actions, capability, hasAction, reasonText } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`)

const implementer = new Set([Actions.configure, Actions.export, Actions.writeValue])
assert.equal(capability(implementer, Actions.export).allowed, true)
assert.equal(capability(implementer, Actions.accountManage).reason, "not_granted")
assert.equal(reasonText("not_granted"), "当前账号没有此操作权限")
assert.equal(capability(implementer, Actions.writeValue, { disabled: true }).reason, "target_disabled")
assert.equal(capability(implementer, Actions.writeValue, { disabled: true, readOnly: true }).reason, "read_only")
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

const temp = mkdtempSync(join(process.cwd(), ".capability-check-"))
try {
  const contextSource = readFileSync(new URL("./src/capabilitiesContext.tsx", import.meta.url), "utf8")
  const contextOutput = ts.transpileModule(contextSource, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX } }).outputText.replace('"./capabilities"', '"./capabilities.js"')
  writeFileSync(join(temp, "capabilities.js"), outputText)
  writeFileSync(join(temp, "capabilitiesContext.js"), contextOutput)
  const { CapabilityProvider, useCapabilities } = await import(pathToFileURL(join(temp, "capabilitiesContext.js")).href)
  const { createElement } = await import("react")
  const { renderToStaticMarkup } = await import("react-dom/server")
  let verdict
  function Probe() {
    verdict = useCapabilities().verdict(Actions.command, { serverAvailable: false })
    return createElement("span", null, reasonText(verdict.reason))
  }
  const html = renderToStaticMarkup(createElement(CapabilityProvider, { actions: new Set([Actions.command]) }, createElement(Probe)))
  assert.equal(verdict.allowed, false)
  assert.equal(verdict.reason, "server_unavailable")
  assert.match(html, /设备当前不支持此操作/)

  const controlSource = readFileSync(new URL("./src/CommandExecuteButton.tsx", import.meta.url), "utf8")
  const controlOutput = ts.transpileModule(controlSource, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX } }).outputText.replace('"./capabilitiesContext"', '"./capabilitiesContext.js"').replace('"./capabilities"', '"./capabilities.js"')
  writeFileSync(join(temp, "CommandExecuteButton.js"), controlOutput)
  const { default: CommandExecuteButton } = await import(pathToFileURL(join(temp, "CommandExecuteButton.js")).href)
  const command = { id: "start", name: "开机", available: false, reason: "未映射点位" }
  const controlHTML = renderToStaticMarkup(createElement(CapabilityProvider, { actions: new Set([Actions.command]) }, createElement(CommandExecuteButton, { command, onClick: () => {} })))
  assert.match(controlHTML, /disabled/)
  assert.match(controlHTML, /title="设备当前不支持此操作：未映射点位"/)
  assert.match(controlHTML, /aria-label="执行 开机：设备当前不支持此操作：未映射点位"/)
  const enabledHTML = renderToStaticMarkup(createElement(CapabilityProvider, { actions: new Set([Actions.command]) }, createElement(CommandExecuteButton, { command: { ...command, available: true, reason: "" }, onClick: () => {} })))
  assert.doesNotMatch(enabledHTML, /disabled/)
} finally {
  rmSync(temp, { recursive: true, force: true })
}

console.log("Capability checks passed: action grants, target disabled/read-only, server availability and refusal reasons")
