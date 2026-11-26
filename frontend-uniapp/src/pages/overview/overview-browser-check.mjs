// 独立 headless 合同冒烟：拦截 API 为 Go 合同夹具，不声称真实后端验收。
import { createRequire } from "node:module"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"
import assert from "node:assert/strict"

const require = createRequire(new URL("../../../../frontend/package.json", import.meta.url))
const { chromium } = require("playwright-core")
const base = process.env.T2_UNIAPP_URL || "http://127.0.0.1:5174/uniapp/"
const prototypeUrl = process.env.OVERVIEW_PROTOTYPE_URL || ""
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
  { id: "target", type: "modbusIo", parent: "room", points: [
    { type: "description", key: "", time: "", text: "目标压力" },
    { type: "units", key: "", time: "", text: "MPa" },
  ] },
  { id: "other-target", type: "modbusIo", parent: "room", points: [{ type: "description", key: "", time: "", text: "2号泵目标" }] },
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
const currentHour = Math.floor(now / 3600000) * 3600000
const hourlyRecords = Array.from({ length: 24 }, (_, index) => ({
  point_id: "pressure", device_id: "channel/1", source_time: new Date(currentHour - (23 - index) * 3600000).toISOString(),
  bucket_end: new Date(currentHour - (22 - index) * 3600000).toISOString(), quality: "good", value: 0.3 + index / 100,
  source: "modbus_runtime", unit: "MPa", aggregate: "1h0m0s", sample_count: 12,
}))
let handover = null
let handoverDelayNext = false
let writeDelayNext = false
let delayedHandoverRelease
let delayedWriteRelease
let commandOutcome = null
const commandHistory = []

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
      profile_id: "pump-v1", model: "CDL-32", protocol: "modbus", point_count: 3, command_count: 2, health: "healthy", verified: true, verifier: "测试员",
    }, {
      channel_id: "channel", channel_name: "泵房通道", server_id: 2, name: "2号泵", business_node_id: "room",
      profile_id: "pump-v1", model: "CDL-32", protocol: "modbus", point_count: 1, command_count: 0, health: "healthy", verified: true, verifier: "测试员",
    }, {
      channel_id: "channel", channel_name: "泵房通道", server_id: 3, name: "水箱", business_node_id: "room",
      profile_id: "tank-v1", model: "TK-20", protocol: "modbus", point_count: 1, command_count: 0, health: "healthy", verified: true, verifier: "测试员",
    }] })
    const unitMatch = apiPath.match(/^\/modbus\/channel\/units\/(\d+)\/(model|commands)(?:\/(.*))?$/)
    if (unitMatch?.[2] === "model" && request.method() === "GET") {
      if (Number(unitMatch[1]) === 2) return json({
        channel_id: "channel", server_id: 2,
        model: { name: "2号泵", profile_id: "pump-v1", business_node_id: "room", point_map: { other: "other-target" }, verified: true, verifier: "测试员" },
        profiles: [{ id: "pump-v1", vendor: "示例厂商", model: "CDL-32" }], profile_errors: [],
        points: [{ id: "other-target", name: "2号泵目标", server_id: 2, address: 200, io_type: "modbusHoldingRegister", read_only: true, unit: "MPa", scale: 0.01, offset: 0 }],
        commands: [], missing: [],
      })
      return json({
      channel_id: "channel", server_id: Number(unitMatch[1]),
      model: { name: "1号泵", profile_id: "pump-v1", business_node_id: "room", point_map: { pressure: "pressure", run: "run", target: "target" }, verified: true, verifier: "测试员" },
      profiles: [{ id: "pump-v1", vendor: "示例厂商", model: "CDL-32" }], profile_errors: [],
      points: [
        { id: "pressure", name: "出口压力", server_id: 1, address: 100, io_type: "modbusInputRegister", read_only: true, unit: "MPa", scale: 0.01, offset: 0 },
        { id: "run", name: "1#泵运行反馈", server_id: 1, address: 1, io_type: "modbusCoil", read_only: true, scale: 1, offset: 0 },
        { id: "target", name: "目标压力", server_id: 1, address: 102, io_type: "modbusHoldingRegister", read_only: false, unit: "MPa", scale: 0.01, offset: 0 },
      ],
      commands: [
        { id: "start", name: "启动", point_id: "run", point_name: "1#泵运行反馈", value: 1, readback_point_id: "run", readback_point_name: "1#泵运行反馈", readback_value: 1, timeout_ms: 1000, parameters: [], available: true },
        { id: "set-target", name: "下发目标值", point_name: "", parameters: [{ name: "target", label: "目标压力", point_id: "target", point_name: "目标压力", unit: "MPa", kind: "number", has_range: true, min: 0, max: 1, default: 0.45, available: true }], available: true },
      ], missing: [],
      })
    }
    if (unitMatch?.[2] === "commands" && !unitMatch[3] && request.method() === "GET") return json({ commands: Number(unitMatch[1]) === 1 ? commandHistory : [] })
    if (unitMatch?.[2] === "commands" && !unitMatch[3] && request.method() === "POST") {
      const body = request.postDataJSON()
      commandOutcome = { request_id: body.request_id, device_id: `channel/${unitMatch[1]}`, command_id: body.command_id, actor: "admin", state: "accepted", started_at: new Date().toISOString(), updated_at: new Date().toISOString() }
      return json(commandOutcome)
    }
    if (unitMatch?.[2] === "commands" && unitMatch[3] && request.method() === "GET") {
      commandOutcome = { ...commandOutcome, state: "unknown", reason: "no readback before timeout", updated_at: new Date().toISOString() }
      if (!commandHistory.some(item => item.request_id === commandOutcome.request_id)) commandHistory.unshift(commandOutcome)
      return json(commandOutcome)
    }
    if (apiPath === "/nodes/target/value" && request.method() === "POST") {
      const body = request.postDataJSON()
      if (writeDelayNext) {
        writeDelayNext = false
        await new Promise(resolve => { delayedWriteRelease = resolve })
      }
      const outcome = { request_id: body.request_id, status: "confirmed", target: body.value, actor: "admin", created_at: new Date().toISOString() }
      commandHistory.unshift({ request_id: body.request_id, device_id: "channel/1", command_id: "", actor: "admin", state: "confirmed", started_at: outcome.created_at, updated_at: outcome.created_at })
      return json(outcome)
    }
    if (apiPath === "/overview/handover" && request.method() === "GET") return json({ handover: url.searchParams.get("layer") === "room" ? handover : null })
    if (apiPath === "/overview/handover" && request.method() === "PUT") {
      if (handoverDelayNext) {
        handoverDelayNext = false
        await new Promise(resolve => { delayedHandoverRelease = resolve })
        return json({ handover: { layer_id: "room", note: "旧层留言", actor: "admin", updated_at: new Date().toISOString() } })
      }
      const body = request.postDataJSON()
      handover = { layer_id: "room", note: body.note, actor: "admin", updated_at: new Date().toISOString() }
      return json({ handover })
    }
    if (apiPath === "/ems/state") { stateRequests += 1; return json({ evaluated_at: new Date().toISOString(), values: [
      { device_id: "channel/1", point_id: "pressure", value: { kind: "number", number: 0 }, quality: "good", source_time: at(2), received_time: at(1) },
      { device_id: "channel/1", point_id: "run", value: { kind: "bool", bool: true }, quality: "good", source_time: at(2), received_time: at(1) },
    ] }) }
    if (apiPath === "/ems/quality") return json({
      channels: [{ channel_id: "channel", health: "healthy", window_5m: { polls: 10, failures: 1, success_rate: 90 } }],
      devices: [{ channel_id: "channel", device_id: "channel/1", health: "healthy", last_success: at(1), window_5m: { polls: 10, failures: 1, success_rate: 90 } }],
    })
    if (apiPath === "/ems/alarms") return json({ alarms: [alarm] })
    if (apiPath.startsWith("/ems/screen/")) return json({ title: "泵房画面", columns: 3, point_ids: ["pressure", "run"], blocks: [{ type: "kpi", binding: { metrics: [{ point_id: "pressure", target: "0.45 MPa" }, { point_id: "run", target: "运行" }] } }] })
    if (apiPath === "/history" && request.method() === "GET") {
      const point = url.searchParams.get("node_id")
      const records = point === "pressure" ? (url.searchParams.get("aggregate") === "1h" ? hourlyRecords : historyRecords) : []
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

  await page.goto(`${base}pages/overview/index?live=1`, { waitUntil: "domcontentloaded" })
  await page.locator(".hero h1").waitFor({ timeout: 15000 })
  await page.locator(".ledger .node").filter({ hasText: "泵房" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "泵房")
  assert.equal(await page.locator(".metric").count(), 6)
  assert.equal(await page.locator(".metric.vacant").count(), 4)
  assert.deepEqual({
    workbenchTabs: await page.locator(".topbar .workbench").count(),
    appearanceButtons: await page.locator(".topbar > .appearance").count(),
    ledgerSearch: await page.locator(".ledger .search").count(),
    ledgerCollapse: await page.locator(".ledger .collapse, .ledger-toggle").count(),
    metricTargets: await page.locator(".metric .target").count(),
    runningSummary: await page.locator(".side .summary-section").count(),
  }, { workbenchTabs: 0, appearanceButtons: 1, ledgerSearch: 0, ledgerCollapse: 0, metricTargets: 0, runningSummary: 0 }, "overview chrome must match the latest glass prototype")
  assert.match(await page.locator(".hero").innerText(), /出口压力[\s\S]*0[\s\S]*MPa/)
  assert.doesNotMatch(await page.locator(".hero").innerText(), /目标/)
  if (prototypeUrl) {
    const prototype = await context.newPage()
    await prototype.goto(prototypeUrl, { waitUntil: "domcontentloaded" })
    await prototype.locator(".hero").waitFor()
    const prototypeMeta = await prototype.locator(".meta").innerText()
    await page.locator(".top-meta").evaluate((element, text) => { element.textContent = text }, prototypeMeta)
    const prototypeMetricContent = await prototype.locator(".metric").evaluateAll(elements => elements.map(element => ({ key: element.querySelector(".k").innerHTML, value: element.querySelector(".v").innerHTML })))
    await page.locator(".metric").evaluateAll((elements, contents) => elements.forEach((element, index) => {
      const key = element.querySelector(".k")
      const value = element.querySelector(".v")
      const scope = key.getAttributeNames().find(name => name.startsWith("data-v-"))
      key.innerHTML = contents[index].key
      value.innerHTML = contents[index].value
      const small = value.querySelector("small")
      if (small) {
        const unit = document.createElement("text")
        unit.innerHTML = small.innerHTML
        small.replaceWith(unit)
      }
      if (scope) element.querySelectorAll("*").forEach(descendant => descendant.setAttribute(scope, ""))
    }), prototypeMetricContent)
    const prototypeFlowSubtitle = await prototype.locator(".scene-head small").innerText()
    await page.locator(".flow-sub").evaluate((element, text) => { element.textContent = text }, prototypeFlowSubtitle)
    const pairs = [[".topbar", "header.mast"], [".ledger", "nav.index"], [".ledger .node", "nav.index button"], [".hero", ".hero"], [".headline", ".headline"], [".hero h1", ".hero h1"], [".metrics", ".metrics"], [".metric", ".metric"], [".metric:nth-child(4)", ".metric:nth-child(4)"], [".metric .k", ".metric .k"], [".metric .v", ".metric .v"], [".flow", ".chain-wrap"], [".lower", ".lower"], [".lower .panel", ".lower .panel"], [".lower .panel:nth-child(2)", ".lower .panel:nth-child(2)"], [".lower .panel:nth-child(2) .card-title", ".lower .panel:nth-child(2) h2"], [".lower .panel:nth-child(2) .link", ".lower .panel:nth-child(2) .link"], [".lower .card-title", ".lower h2"], [".lower .run", ".lower .run"], [".daily", ".daily"]]
    const measure = selectors => selectors.map(selector => {
      const element = document.querySelector(selector)
      const rect = element.getBoundingClientRect()
      const style = getComputedStyle(element)
      return { selector, rect: [rect.x, rect.y, rect.width, rect.height].map(value => Math.round(value * 100) / 100), display: style.display, columns: style.gridTemplateColumns, rows: style.gridTemplateRows, gap: style.gap, ...(element.tagName === "TABLE" ? { minWidth: style.minWidth } : {}), ...(element.classList.contains("metric") ? {} : { padding: style.padding }), margin: style.margin, ...(element.classList.contains("metric") ? {} : { fontSize: style.fontSize }) }
    })
    const actualLayout = await page.evaluate(measure, pairs.map(pair => pair[0]))
    const prototypeLayout = await prototype.evaluate(measure, pairs.map(pair => pair[1])).then(rows => rows.map((row, index) => ({ ...row, selector: pairs[index][0] })))
    assert.deepEqual(actualLayout, prototypeLayout, "computed geometry must match the latest overview prototype")
    await page.setViewportSize({ width: 375, height: 812 })
    await prototype.setViewportSize({ width: 375, height: 812 })
    assert.equal(await page.locator(".lower .panel:nth-child(2) table").evaluate(element => getComputedStyle(element).minWidth), "0px", "recent table must not inherit the global mobile 560px minimum")
    const actualMobileLayout = await page.evaluate(measure, pairs.map(pair => pair[0]))
    const prototypeMobileLayout = await prototype.evaluate(measure, pairs.map(pair => pair[1])).then(rows => rows.map((row, index) => ({ ...row, selector: pairs[index][0] })))
    assert.equal(actualMobileLayout.length, prototypeMobileLayout.length)
    actualMobileLayout.forEach((row, index) => {
      const expected = prototypeMobileLayout[index]
      const actualRect = row.rect
      const expectedRect = expected.rect
      assert.deepEqual({ ...row, rect: null }, { ...expected, rect: null }, `mobile style mismatch: ${row.selector}`)
      actualRect.forEach((value, rectIndex) => {
        if (row.selector === ".ledger .node" && rectIndex === 2) return
        assert.ok(Math.abs(value - expectedRect[rectIndex]) <= 0.25, `${row.selector} rect[${rectIndex}]: ${value} vs ${expectedRect[rectIndex]}`)
      })
    })
    await page.setViewportSize({ width: 1280, height: 800 })
    await prototype.close()
  }
  assert.doesNotMatch(await page.locator(".ledger").innerText(), /decisionTable|rules|不应出现在台账|空区域/)
  assert.match(await page.locator(".side").innerText(), /今日执行[\s\S]*后端未提供/)
  assert.equal(await page.locator(".daily-trend .hour-bar.has-data").count(), 24)
  await page.locator(".appearance").click()
  await page.locator(".theme-choice[data-theme='dark']").click()
  assert.match(await page.locator(".page").getAttribute("class"), /theme-dark/)
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-overview-dark-1280.png") })
  await page.locator(".theme-choice[data-theme='tint']").click()
  assert.match(await page.locator(".page").getAttribute("class"), /theme-tint/)
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-overview-tint-1280.png") })
  const alphaSlider = page.locator(".appearance-panel uni-slider")
  assert.equal(await alphaSlider.count(), 1)
  const sliderBox = await alphaSlider.boundingBox()
  assert.ok(sliderBox)
  await alphaSlider.click({ position: { x: sliderBox.width * 0.75, y: sliderBox.height / 2 } })
  await page.waitForFunction(() => document.querySelector(".appearance-panel .num")?.textContent !== "42%")
  await page.locator(".appearance-reset").click()
  assert.doesNotMatch(await page.locator(".page").getAttribute("class"), /theme-dark|theme-tint/)
  assert.equal(await page.locator(".appearance-panel .num").innerText(), "42%")
  await page.locator(".appearance").click()
  await page.waitForTimeout(200)
  const handoverInput = page.locator(".handover-input textarea")
  await handoverInput.click()
  await handoverInput.pressSequentially("夜间注意液位", { delay: 30 })
  await page.waitForTimeout(200)
  await page.locator(".save-handover").click()
  await page.locator(".handover-record").filter({ hasText: "夜间注意液位" }).waitFor()
  assert.match(await page.locator(".handover-record").innerText(), /admin/)
  await page.waitForTimeout(300)
  handoverDelayNext = true
  await handoverInput.click()
  await page.keyboard.press("Control+A")
  await page.keyboard.press("Backspace")
  await handoverInput.pressSequentially("旧层留言", { delay: 30 })
  await page.waitForTimeout(200)
  await page.locator(".save-handover").click()
  await page.waitForFunction(() => document.querySelector(".save-handover")?.textContent === "保存中…")
  await page.locator(".ledger .node").filter({ hasText: "一号现场" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "一号现场")
  while (!delayedHandoverRelease) await new Promise(resolve => setTimeout(resolve, 25))
  delayedHandoverRelease()
  await page.waitForTimeout(400)
  assert.doesNotMatch(await page.locator(".handover-section").innerText(), /旧层留言|交接已保存/)
  await page.locator(".ledger .node").filter({ hasText: "泵房" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "泵房")
  await page.locator(".handover-record").filter({ hasText: "夜间注意液位" }).waitFor()
  assert.doesNotMatch(await page.locator(".lower .panel").nth(1).innerText(), /来源/)
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "overview 1280px horizontal overflow")
  await page.evaluate(() => window.scrollTo(0, 0))
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-t2-overview-1280.png") })
  await page.setViewportSize({ width: 1024, height: 768 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "overview 1024px horizontal overflow")
  assert.equal(await page.locator(".metric").count(), 6)
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-t2-overview-1024.png") })
  await page.setViewportSize({ width: 1280, height: 800 })

  await page.locator(".ledger .node").filter({ hasText: "1号泵" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "1号泵")
  assert.match(await page.locator(".stage").innerText(), /设备状态|实时数据/)
  assert.match(await page.locator(".stage").innerText(), /运行/)
  await page.locator(".device-tabs > *").filter({ hasText: "设备参数" }).click()
  assert.match(await page.locator(".device-params").innerText(), /pump-v1[\s\S]*CDL-32/)
  await page.locator(".device-tabs > *").filter({ hasText: "实时数据" }).click()
  assert.match(await page.locator(".device-points").innerText(), /目标压力/)
  await page.locator(".open-operations").click()
  await page.locator(".operation-drawer").waitFor()
  const drawerBox = await page.locator(".operation-drawer").boundingBox()
  assert.ok(drawerBox && drawerBox.x > 800 && drawerBox.x + drawerBox.width <= 1280, `drawer must be viewport-right, got ${JSON.stringify(drawerBox)}`)
  await page.locator(".command-button").filter({ hasText: "启动" }).click()
  await page.locator(".operation-result").filter({ hasText: "结果未知" }).waitFor()
  await page.locator(".write-point").selectOption("target")
  await page.locator(".write-value input").fill("0.45")
  await page.locator(".write-submit").click()
  await page.locator(".operation-result").filter({ hasText: "已回读" }).waitFor()
  assert.match(await page.locator(".operation-records").innerText(), /结果未知[\s\S]*已回读|已回读[\s\S]*结果未知/)
  if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, "uniapp-overview-operation-1280.png") })
  await page.locator(".operation-drawer .drawer-close").click()
  writeDelayNext = true
  await page.locator(".open-operations").click()
  await page.locator(".operation-drawer").waitFor()
  await page.locator(".write-value input").fill("0.46")
  await page.locator(".write-submit").click()
  await page.waitForFunction(() => document.querySelector(".operation-result")?.textContent.includes("正在写入"))
  await page.locator(".ledger .node").filter({ hasText: "2号泵" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "2号泵")
  await page.locator(".device-points").filter({ hasText: "2号泵目标" }).waitFor()
  while (!delayedWriteRelease) await new Promise(resolve => setTimeout(resolve, 25))
  delayedWriteRelease()
  await page.waitForTimeout(400)
  assert.doesNotMatch(await page.locator(".operation-records").innerText(), /目标|已回读/)
  assert.equal(await page.locator(".operation-drawer").count(), 0)
  await page.locator(".ledger .node").filter({ hasText: "1号泵" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "1号泵")
  await page.locator(".device-points").filter({ hasText: "目标压力" }).waitFor()

  await page.locator(".lower .panel").nth(1).locator(".object-link").filter({ hasText: "出口压力" }).first().click()
  await page.waitForFunction(() => document.querySelector(".history-drawer .hist-head h1")?.textContent === "出口压力")
  await page.locator(".history-drawer tbody tr").first().waitFor()
  assert.equal(await page.locator(".history-drawer tbody tr").count(), 12)
  await page.locator(".history-drawer .hist-bar .chips > *").filter({ hasText: "曲线" }).click()
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
  await page.locator(".history-drawer .hist-head .ghost").filter({ hasText: "导出 CSV" }).click()
  const download = await downloadPromise
  assert.match(download.suggestedFilename(), /出口压力/)

  await page.locator(".history-drawer .hist-head .ghost").filter({ hasText: "← 返回" }).click()
  await page.locator(".history-drawer").waitFor({ state: "detached" })
  await page.locator(".hero h1").waitFor()
  const pollsBeforeReturn = stateRequests
  await page.waitForTimeout(2400)
  assert.ok(stateRequests > pollsBeforeReturn, "overview polling should resume after history hides")

  await page.locator(".bell").click()
  await page.locator(".alarm-tabs > *").filter({ hasText: "历史 1" }).click()
  await page.locator(".alarm .ghost").filter({ hasText: "定位到对象" }).click()
  await page.waitForFunction(() => document.querySelector(".hero h1")?.textContent === "1号泵")
  await page.locator(".lower .panel").nth(1).locator(".object-link").filter({ hasText: "1#泵运行反馈" }).first().click()
  await page.waitForFunction(() => document.querySelector(".history-drawer .hist-head h1")?.textContent === "1#泵运行反馈")
  assert.equal(await page.locator(".history-drawer thead th").count(), 5)
  assert.equal(await page.locator(".history-drawer tbody tr").count(), 1)
  assert.equal((await page.locator(".history-drawer tbody").innerText()).trim(), "—")

  assert.deepEqual(consoleErrors, [], `console errors: ${consoleErrors.join("\n")}`)
  console.log(JSON.stringify({ passed: true, mode: "contract fixtures; dev modules via @fs and uni-h5 CSS stubs", checks: ["layer switch", "device switch", "business metric and unit without non-prototype target row", "today execution gap", "three themes, working alpha slider and reset", "24 real hourly bars", "handover save response", "handover response cannot land on another layer", "device parameter and point tabs", "Profile command unknown result", "manual write confirmed readback", "device A write cannot land on device B", "viewport-right operation drawer", "history query", "line gaps and keyboard readout", "alarm ack and close", "CSV export", "1280x800 and 1024x768 no page overflow", ...(prototypeUrl ? ["computed geometry equals latest overview prototype"] : []), "no console errors after dev module workaround"], evidenceDir }, null, 2))
} catch (error) {
  console.error("browser-check failure", error)
  console.error("console errors", consoleErrors)
  console.error("failed responses", failedResponses)
  if (page) console.error("page", page.url(), (await page.locator("body").innerText()).slice(0, 2000))
  throw error
} finally {
  if (browser) await browser.close()
}
