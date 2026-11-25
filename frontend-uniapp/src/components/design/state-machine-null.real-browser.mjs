import assert from "node:assert/strict"
import { chromium } from "playwright-core"

// TestStateMachineBrowserBackend: real authenticated API, isolated store, no execution.
const base = process.env.FASTIOT_MACHINE_URL || "http://127.0.0.1:18115"
const fixture = await (await fetch(base + "/__fixture")).json()
assert.equal(fixture.fixture, "ticket14-private-simulator")
const api = async (path, method = "GET", body) => {
  const response = await fetch(base + "/api" + path, {
    method, headers: { Authorization: `Bearer ${fixture.token}`, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  assert.equal(response.status, 200, await response.clone().text())
  return response.json()
}
const guard = await api("/nodes", "POST", { parent: fixture.site, type: "variable", description: "Null-array guard", value: 1 })
const output = await api("/nodes", "POST", { parent: fixture.site, type: "variable", description: "Null-array software output", value: 0 })
const definition = {
  initial_state: "idle", scope: "subtree", states: ["idle"],
  transitions: [
    { transition_id: "guarded", from: "idle", event: "tick", to: "idle", conditions: [{ point_id: guard.id, operator: "eq", value: 99 }], actions: null },
    { transition_id: "software", from: "idle", event: "software", to: "idle", conditions: null, actions: [{ action_id: output.id, value: 1, readback: { timeout_ms: 1000 } }] },
  ],
  bindings: { points: { [guard.id]: { node_id: guard.id } }, actions: { [output.id]: { node_id: output.id } } },
}
let record = await api("/state-machines", "POST", { parent: fixture.site, name: "Nullable process", definition })
const path = `/state-machines/${record.id}`
record = await api(path + "/publish", "POST", { revision: record.revision })
const raw = (await api("/state-machines")).state_machines.find(item => item.id === record.id)
assert.equal(raw.draft.transitions[0].actions, null)
assert.equal(raw.applied.transitions[1].conditions, null)
assert.equal(raw.versions[1].transitions[0].actions, null)
const originalWire = JSON.stringify({ draft: raw.draft, applied: raw.applied, versions: raw.versions })
const originalVersion = JSON.stringify(raw.versions[1])
let browser
try {
  try { browser = await chromium.launch({ channel: "chrome", headless: true }) }
  catch { browser = await chromium.launch({ channel: "msedge", headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  page.setDefaultTimeout(12000)
  const errors = [], controls = []
  page.on("pageerror", error => errors.push(error.message))
  page.on("request", request => { if (request.method() === "POST" && /\/(execute|commands|value)(\?|$)/.test(request.url())) controls.push(request.url()) })
  await page.addInitScript(token => localStorage.setItem("fastems-token", token), fixture.token)
  const open = async () => {
    await page.goto(base + "/uniapp/#/pages/design/index")
    await page.locator(".design-ledger .node").filter({ hasText: "State machine fixture" }).click()
    await page.locator(".stepper .step").filter({ hasText: "配策略" }).click()
    await page.locator(".machine-tabs uni-button").filter({ hasText: /^Nullable process$/ }).click()
    await page.locator(".machine-table tbody tr").first().waitFor()
  }
  await open()
  const panel = page.locator(".machine-panel")
  assert.equal(await panel.locator(".machine-table tbody tr").count(), 2)
  assert.doesNotMatch(await panel.innerText(), /Cannot read properties|读取失败/)
  record = await api(path)
  assert.equal(JSON.stringify({ draft: record.draft, applied: record.applied, versions: record.versions }), originalWire, "opening the editor must not rewrite wire definitions")
  await panel.locator(".machine-table tbody tr").filter({ hasText: "software" }).locator("uni-button").filter({ hasText: /^编辑$/ }).click()
  const transition = panel.locator(".transition:visible")
  await transition.locator("uni-button").filter({ hasText: /^＋ 守卫条件$/ }).click()
  await panel.locator("uni-button").filter({ hasText: /^保存草稿$/ }).click()
  await panel.locator(".notice").filter({ hasText: "草稿已保存" }).waitFor()
  record = await api(path)
  assert.equal(record.version, 1)
  assert.equal(record.draft.transitions[1].conditions.length, 1)
  assert.equal(JSON.stringify(record.applied), JSON.stringify(raw.applied), "draft save must not normalize the applied wire")
  assert.equal(JSON.stringify(record.versions[1]), originalVersion)
  await panel.locator("uni-button").filter({ hasText: /^发布草稿$/ }).click()
  await panel.locator("uni-button").filter({ hasText: /^确认发布$/ }).click()
  await panel.locator(".notice").filter({ hasText: "发布成功" }).waitFor()
  record = await api(path)
  assert.equal(record.version, 2)
  assert.equal(record.applied.transitions[1].conditions.length, 1)
  assert.equal(JSON.stringify(record.versions[1]), originalVersion, "published v1 must remain exact, including null arrays")
  await page.reload()
  await open()
  assert.match(await panel.innerText(), /已发布 v2/)
  assert.doesNotMatch(await panel.innerText(), /Cannot read properties/)
  assert.deepEqual(errors, [])
  assert.deepEqual(controls, [])
  assert.equal(((await (await fetch(base + "/__fixture")).json()).writes || []).length, 0)
  console.log(JSON.stringify({ realGo: true, nullableAPI: true, openedEditedPublishedReloaded: true, originalWireUnchangedOnOpen: true, immutableV1Exact: true, controlRequests: 0, physicalWrites: 0, errors }))
} finally { await browser?.close() }
