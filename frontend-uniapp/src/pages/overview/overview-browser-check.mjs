// 独立 headless 合同冒烟：拦截 API 为 Go 合同夹具，不声称真实后端验收。
import { createRequire } from "node:module"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"
import assert from "node:assert/strict"

const require = createRequire(new URL("../../../../frontend/package.json", import.meta.url))
const { chromium } = require("playwright-core")
const base = process.env.T2_UNIAPP_URL || "http://127.0.0.1:5174/uniapp/"
const evidenceDir = process.env.T2_EVIDENCE_DIR || ""
const uniappRoot = fileURLToPath(new URL("../../../", import.meta.url)).replaceAll("\\", "/").replace(/\/$/, "")
if (evidenceDir) await mkdir(evidenceDir, { recursive: true })

const now = Date.now()
const at = offsetSeconds => new Date(now - offsetSeconds * 1000).toISOString()
const nodes = [
  { id: "site", type: "site", parent: "", points: [{ type: "description", key: "", time: "", text: "一号现场" }] },
  { id: "room", type: "group", parent: "site", points: [{ type: "description", key: "", time: "", text: "泵房" }] },
  { id: "pressure", type: "modbusIo", parent: "room", points: [
    { type: "description", key: "", time: "", text: "出口压力" },
    { type: "units", key: "", time: "", text: "MPa" },
  ] },
  { id: "run", type: "modbusIo", parent: "room", points: [{ type: "description", key: "", time: "", text: "1#泵运行反馈" }] },
  { id: "rules", type: "decisionTable", parent: "room", points: [{ type: "description", key: "", time: "", text: "不应出现在台账" }] },
  { id: "empty", type: "group", parent: "site", points: [{ type: "description", key: "", time: "", text: "空区域" }] },
]
let alarm = {
  id: "alarm-1", key: "PUMP", revision: 1, severity: "warning", title: "出口压力低", detail: "检查泵运行状态",
  device_id: "channel/1", point_id: "pressure", active: true, state: "active", created_at: at(100), updated_at: at(100),
  permissions: { ack: true, maintain: true },
}
let stateRequests = 0
const historyRecords = Array.from({ length: 12 }, (_, index) => ({
  point_id: "pressure", device_id: "channel/1", source_time: at((11 - index) * 30),
  received_time: at((11 - index) * 30 - 1), quality: index === 5 ? "invalid" : "good",
  ...(index === 5 ? { kind: "quality_event", reason: "timeout" } : { value: index === 0 ? 0 : 0.4 + index / 100 }),
  source: "modbus_runtime", unit: "MPa",
}))

let browser
let page
const consoleErrors = []
const failedResponses = []
try {
  try { browser = await chromium.launch({ channel: "chrome", headless: true }) }
  catch { browser = await chromium.launch({ channel: "msedge", headless: true }) }
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, acceptDownloads: true })
  await context.addInitScript(() => {
    localStorage.setItem("fastems-token", "contract-fixture")
    sessionStorage.setItem("fastems-token", "contract-fixture")
  })
  page = await context.newPage()
  page.on("response", response => { if (response.status() >= 400) failedResponses.push(`${response.status()} ${response.url()}`) })
  page.on("console", message => { if (message.type() === "error") consoleErrors.push(message.text()) })
  page.on("pageerror", error => consoleErrors.push(error.message))
  await page.route(`${new URL(base).origin}/api/**`, async route => {
    const request = route.request()
    const url = new URL(request.url())
    const apiPath = url.pathname.replace(/^\/api/, "")
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    if (apiPath === "/snapshot") return json({ user: { user_id: "admin", policy: { admin: true, role: "admin" } }, roots: ["site"], nodes })
    if (apiPath === "/device-ledger") return json({ devices: [{
      channel_id: "channel", channel_name: "泵房通道", server_id: 1, name: "1号泵", business_node_id: "room",
      profile_id: "pump-v1", model: "CDL-32", protocol: "modbus", point_count: 2, command_count: 2, health: "healthy", verified: true, verifier: "测试员",
    }] })
    if (apiPath === "/ems/state") { stateRequests += 1; return json({ evaluated_at: new Date().toISOString(), values: [
      { device_id: "channel/1", point_id: "pressure", value: { kind: "number", number: 0 }, quality: "good", source_time: at(2), received_time: at(1) },
      { device_id: "channel/1", point_id: "run", value: { kind: "bool", bool: true }, quality: "good", source_time: at(2), received_time: at(1) },
    ] }) }
    if (apiPath === "/ems/quality") return json({
      channels: [{ channel_id: "channel", health: "healthy", window_5m: { polls: 10, failures: 1, success_rate: 90 } }],
      devices: [{ channel_id: "channel", device_id: "channel/1", health: "healthy", last_success: at(1), window_5m: { polls: 10, failures: 1, success_rate: 90 } }],
    })
    if (apiPath === "/ems/alarms") return json({ alarms: [alarm] })
    if (apiPath.startsWith("/ems/screen/")) return json({ title: "泵房画面", columns: 3, point_ids: ["pressure", "run"] })
    if (apiPath === "/history" && request.method() === "GET") {
      const point = url.searchParams.get("node_id")
      const records = point === "pressure" ? historyRecords : []
      return json({ results: [{ node_id: point, source: "modbus_runtime", records, matched: records.length, returned: records.length, truncated: false, sample_interval_ms: 30000 }], total_matched: records.length })
    }
    if (apiPath === "/history/export") return route.fulfill({ status: 200, contentType: "text/csv", body: "点位ID,数值\npressure,0.4\n" })
    if (apiPath === "/ems/alarms/alarm-1/ack" && request.method() === "POST") {
      alarm = { ...alarm, state: "acknowledged", updated_at: new Date().toISOString() }
      return json(alarm)
    }
    if (apiPath === "/ems/alarms/alarm-1/close" && request.method() === "POST") {
      alarm = { ...alarm, active: false, state: "closed", updated_at: new Date().toISOString() }
      return json(alarm)
    }
    return json({ error: `unhandled_fixture_route:${apiPath}` }, 404)
  })

  // 仅在本测试内绕过共享 dev server 的 base 模块 404；产品文件不改。
  await page.route("**/uniapp/node_modules/**", route => {
    const url = new URL(route.request().url())
    const rest = url.pathname.split("/uniapp/node_modules/")[1]
    url.pathname = `/uniapp/@fs/${uniappRoot}/node_modules/${rest}`
    return route.continue({ url: url.toString() })
  })
  await page.route("**/uniapp/node_modules/@dcloudio/uni-h5/style/api/*.css", route => route.fulfill({ status: 200, contentType: "application/javascript", body: "export default '';" }))

  await page.goto(`${base}pages/overview/index`, { waitUntil: "domcontentloaded" })
  await page.locator(".hero h1").waitFor({ timeout: 15000 })
  await page.locator(".ledger .node").filter({ hasText: "泵房" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "泵房")
  assert.equal(await page.locator(".metric").count(), 6)
  assert.equal(await page.locator(".metric.vacant").count(), 4)
  assert.match(await page.locator(".hero").innerText(), /出口压力[\s\S]*0[\s\S]*MPa/)
  assert.doesNotMatch(await page.locator(".ledger").innerText(), /decisionTable|rules|不应出现在台账|空区域/)
  assert.match(await page.locator(".side").innerText(), /今日执行[\s\S]*后端未提供/)
  assert.doesNotMatch(await page.locator(".lower .panel").nth(1).innerText(), /来源/)
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "overview 1280px horizontal overflow")
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-t2-overview-1280.png") })
  await page.setViewportSize({ width: 1024, height: 768 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "overview 1024px horizontal overflow")
  assert.equal(await page.locator(".metric").count(), 6)
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-t2-overview-1024.png") })
  await page.setViewportSize({ width: 1280, height: 800 })

  await page.locator(".ledger .node").filter({ hasText: "1号泵" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "1号泵")
  assert.match(await page.locator(".stage").innerText(), /设备状态/)
  assert.match(await page.locator(".stage").innerText(), /运行/)

  await page.locator(".metric").filter({ hasText: "出口压力" }).click()
  await page.waitForFunction(() => document.querySelector(".hist-head h1")?.textContent === "出口压力")
  await page.locator(".history tbody tr").first().waitFor()
  assert.equal(await page.locator(".history tbody tr").count(), 12)
  await page.locator(".hist-bar .chips > *").filter({ hasText: "曲线" }).click()
  await page.locator(".trend-svg").waitFor()
  assert.equal(await page.locator(".trend-svg text").count(), 6, "three y labels and three x labels must be real SVG text")
  assert.equal(await page.locator(".series-line").count(), 2, "quality event should split the line")
  await page.locator(".trend-svg").focus()
  await page.keyboard.press("End")
  assert.match(await page.locator(".readout").innerText(), /0\.51/)
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "history 1280px horizontal overflow")
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-t2-history-1280.png") })
  await page.setViewportSize({ width: 1024, height: 768 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "history 1024px horizontal overflow")
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-t2-history-1024.png") })
  await page.setViewportSize({ width: 1280, height: 800 })

  await page.locator(".bell").click()
  await page.locator(".alarm .ghost").filter({ hasText: "确认接手" }).click()
  await page.locator(".alarm-tabs > *").filter({ hasText: "活动 1" }).waitFor()
  // 模拟现场条件随后恢复：只有 resolved 才能关闭，ack 本身不恢复。
  alarm = { ...alarm, active: false, state: "resolved", updated_at: new Date().toISOString() }
  await page.locator(".alarm-tabs > *").filter({ hasText: "历史 1" }).click()
  await page.locator(".alarm").filter({ hasText: "已恢复" }).waitFor()
  await page.locator(".alarm .ghost").filter({ hasText: "关闭告警" }).click()
  await page.locator(".alarm .ghost").filter({ hasText: "关闭告警" }).waitFor({ state: "detached" })
  await page.locator(".drawer-head .x").click()
  const downloadPromise = page.waitForEvent("download")
  await page.locator(".hist-head .ghost").filter({ hasText: "导出 CSV" }).click()
  const download = await downloadPromise
  assert.match(download.suggestedFilename(), /出口压力/)

  await page.locator(".hist-head .ghost").filter({ hasText: "← 返回" }).click()
  await page.locator(".hero h1").waitFor()
  const pollsBeforeReturn = stateRequests
  await page.waitForTimeout(2400)
  assert.ok(stateRequests > pollsBeforeReturn, "overview polling should resume after history hides")

  await page.locator(".bell").click()
  await page.locator(".alarm-tabs > *").filter({ hasText: "历史 1" }).click()
  await page.locator(".alarm .ghost").filter({ hasText: "定位到对象" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "1号泵")
  await page.locator(".metric").filter({ hasText: "1#泵运行反馈" }).click()
  await page.waitForFunction(() => document.querySelector(".hist-head h1")?.textContent === "1#泵运行反馈")
  assert.equal(await page.locator(".history thead th").count(), 5)
  assert.equal(await page.locator(".history tbody tr").count(), 1)
  assert.equal((await page.locator(".history tbody").innerText()).trim(), "—")

  assert.deepEqual(consoleErrors, [], `console errors: ${consoleErrors.join("\n")}`)
  console.log(JSON.stringify({ passed: true, mode: "contract fixtures; dev modules via @fs and uni-h5 CSS stubs", checks: ["layer switch", "device switch", "business metric and unit", "today execution gap", "history query", "line gaps and keyboard readout", "alarm ack and close", "CSV export", "1280x800 and 1024x768 no page overflow", "no console errors after dev module workaround"], evidenceDir }, null, 2))
} catch (error) {
  console.error("browser-check failure", error)
  console.error("console errors", consoleErrors)
  console.error("failed responses", failedResponses)
  if (page) console.error("page", page.url(), (await page.locator("body").innerText()).slice(0, 2000))
  throw error
} finally {
  if (browser) await browser.close()
}
