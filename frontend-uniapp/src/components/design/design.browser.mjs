import assert from "node:assert/strict"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || "msedge", headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  await page.route(url => url.pathname.startsWith("/api/"), route => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api/, "")
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    if (path === "/login") return json({ token: "mock-token" })
    if (path === "/templates") return json({ site_templates: [
      { id: "blank", name: "空白现场", description: "只建现场根对象", nodes: [] },
      { id: "pump-house", name: "供水泵房", description: "泵房结构", nodes: [{ name: "泵房", type: "area", children: [{ name: "供水单元", type: "unit", children: [{ name: "1 号泵", type: "device" }] }] }] },
    ] })
    if (path === "/industry/packages") return json({ packages: [{ id: "ems", installed: true }] })
    if (path === "/snapshot") return json({
      user: { user_id: "admin", policy: { admin: true, role: "admin" } }, roots: ["site-1"],
      nodes: [
        { id: "site-1", type: "site", parent: "", points: [{ type: "description", key: "", time: "", text: "教学泵房" }] },
        { id: "area-1", type: "area", parent: "site-1", points: [{ type: "description", key: "", time: "", text: "泵房" }] },
        { id: "unit-1", type: "energyUnit", parent: "area-1", points: [{ type: "description", key: "", time: "", text: "供水单元" }] },
        { id: "device-1", type: "device", parent: "unit-1", points: [{ type: "description", key: "", time: "", text: "1 号泵" }] },
        { id: "point-1", type: "variable", parent: "device-1", points: [{ type: "description", key: "", time: "", text: "出口压力" }, { type: "units", key: "", time: "", text: "MPa" }] },
        { id: "power-1", type: "variable", parent: "device-1", points: [{ type: "description", key: "", time: "", text: "总功率" }, { type: "units", key: "", time: "", text: "kW" }] },
        { id: "rule-1", type: "decisionTable", parent: "site-1", points: [{ type: "description", key: "", time: "", text: "压力策略" }, { type: "draft", key: "", time: "", text: "{\"table\":{\"rows\":[]},\"bindings\":{\"points\":{},\"actions\":{}}}" }] },
      ],
    })
    if (path === "/ems/state") return json({ evaluated_at: "2026-11-23T10:00:00Z", values: [
      { device_id: "device-1", point_id: "point-1", value: { kind: "number", number: 12.5 }, quality: "good", source_time: "2026-11-23T10:00:00Z" },
      { device_id: "device-1", point_id: "power-1", value: { kind: "number", number: 10.8 }, quality: "good", source_time: "2026-11-23T10:00:00Z" },
    ] })
    if (path === "/ems/quality") return json({ channels: [], devices: [{ device_id: "device-1", health: "healthy", window_5m: { polls: 100, failures: 1, success_rate: 99 } }] })
    if (path === "/ems/alarms") return json({ alarms: [{ id: "alarm-1", key: "pressure", revision: 1, severity: "warning", title: "出口压力低", detail: "现场告警", device_id: "device-1", point_id: "point-1", active: true, state: "active", created_at: "2026-11-23T10:00:00Z", updated_at: "2026-11-23T10:00:00Z" }] })
    if (path === "/ems/screen/site-1") return json({ title: "教学画面", columns: 3, point_ids: ["power-1", "point-1"] })
    if (path.startsWith("/ems/screen/")) return json({ error: "not_configured" }, 404)
    return json({})
  })
  await page.goto("http://127.0.0.1:5174/uniapp/?live=1#/pages/login/index")
  await page.locator("input").nth(0).fill("admin", { force: true })
  await page.locator("input").nth(1).fill("fixture-password-not-a-real-account", { force: true })
  await page.locator("uni-button").filter({ hasText: "登录" }).first().click()
  await page.waitForURL(/pages\/overview\/index/)
  await page.goto("http://127.0.0.1:5174/uniapp/?live=1#/pages/design/index")
  await page.locator(".stepper").waitFor()

  await page.locator(".tpl-card").filter({ hasText: "供水泵房" }).click()
  await page.locator(".template-outline .row").filter({ hasText: "区域泵房" }).waitFor()
  assert.equal(await page.locator(".design-ledger .node").count(), 4, "left ledger contains every real business object")
  await page.locator(".stepper uni-button").filter({ hasText: "搭台账" }).click()
  await page.locator(".ledger-worktable").getByText("1 号泵", { exact: false }).waitFor()
  assert.equal(await page.locator(".ledger-worktable").getByText("出口压力", { exact: false }).count(), 0, "points are not business rows")
  await page.locator(".tree-search input").fill("1 号泵", { force: true })
  if (process.env.DESIGN_DEBUG) console.log("tree search", await page.locator(".tree-search input").inputValue(), await page.locator(".ledger-worktable tbody").innerText())
  await page.waitForFunction(() => document.querySelectorAll(".ledger-worktable tbody tr").length === 1)
  assert.equal(await page.locator(".ledger-worktable tbody tr").count(), 1, "real object search filters the worktable")

  await page.locator(".stepper uni-button").filter({ hasText: "配界面" }).click()
  await page.locator(".cfg-list").waitFor()
  await page.locator(".customer-preview").getByText("教学泵房", { exact: true }).waitFor()
  assert.equal(await page.locator(".cfg-slot").count(), 3, "top, main and side slots are visible")
  assert.equal(await page.locator("uni-button").filter({ hasText: "保存本层" }).count(), 1, "a supported real layer exposes its save action")
  await page.locator(".layer-status-card").getByText("哪些层已配", { exact: true }).waitFor()
  const geometry = await page.evaluate(() => {
    const rect = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height } }
    const steps = [...document.querySelectorAll(".stepper uni-button")].map(el => el.getBoundingClientRect())
    return { statebar: rect(".statebar"), ledger: rect(".design-ledger"), stage: rect(".stage"), cfgList: rect(".cfg-list"), preview: rect(".cfg-preview"), frame: rect(".cfg-frame"), custHead: rect(".cust-head"), kpiRow: rect(".kpi-row"), canvas: rect(".canvas-grid"), firstStep: rect(".stepper uni-button"), firstCard: rect(".canvas-main .screen-card"), flowNode: rect(".flow .flow-node"), stepSpan: steps.at(-1).right - steps[0].left }
  })
  const near = (actual, expected, label) => assert(Math.abs(actual - expected) <= 2, `${label}: expected ${expected}±2, got ${actual}`)
  if (process.env.DESIGN_DEBUG) console.log(JSON.stringify({ geometry }, null, 2))
  if (process.env.DESIGN_DEBUG) console.log(JSON.stringify(await page.locator(".stepper").evaluate(el => [...el.children].map(child => { const r = child.getBoundingClientRect(); const s = getComputedStyle(child); return { tag: child.tagName, x: r.x, width: r.width, flex: s.flex, margin: s.margin, padding: s.padding } })), null, 2))
  near(geometry.statebar.height, 24, "statebar height")
  near(geometry.ledger.x, 14, "ledger x")
  near(geometry.ledger.width, 272, "ledger width")
  near(geometry.stage.x, 300, "stage x")
  near(geometry.firstStep.x, 300, "stepper x")
  near(geometry.firstStep.height, 30, "step height")
  near(geometry.stepSpan, 588, "compact stepper width")
  near(geometry.cfgList.x, 300, "slot panel x")
  near(geometry.cfgList.width, 290, "slot panel width")
  near(geometry.preview.x, 602, "customer preview x")
  near(geometry.preview.width, 664, "customer preview width")
  near(geometry.kpiRow.height, 61, "KPI row height")
  const cfgStyles = await page.locator(".cfg-list").evaluate(el => ({ minHeight: getComputedStyle(el).minHeight, maxHeight: getComputedStyle(el).maxHeight }))
  assert.deepEqual(cfgStyles, { minHeight: "auto", maxHeight: "720px" }, "slot panel has no hardcoded minimum height")
  const cfgRowStyles = await page.locator(".cfg-row").first().evaluate(el => ({ gap: getComputedStyle(el).gap, padding: getComputedStyle(el).padding, fontSize: getComputedStyle(el).fontSize }))
  assert.deepEqual(cfgRowStyles, { gap: "5px", padding: "5px 2px", fontSize: "12.5px" }, "block rows use prototype spacing")
  assert.deepEqual(await page.locator(".kpi-row .kpi > span").allTextContents(), ["总功率", "出口压力", "在线设备", "活动告警", "通信成功率", "数据新鲜度"], "saved point bindings are preserved and remaining KPI slots use real automatic concepts")
  assert.equal(await page.locator(".kpi-row").getByText("设备对象", { exact: true }).count(), 0, "generic object counters are not KPIs")
  assert.equal(await page.locator(".flow .flow-node").count(), 3, "three-stage process visual is restored")
  const flowStyles = await page.locator(".flow .flow-node").first().evaluate(el => ({ display: getComputedStyle(el).display, gap: getComputedStyle(el).gap, padding: getComputedStyle(el).padding }))
  assert.deepEqual(flowStyles, { display: "flex", gap: "6px", padding: "14px" }, "process nodes use prototype spacing")
  await page.locator(".flow .flow-node").nth(0).getByText("泵房", { exact: true }).waitFor()
  await page.locator(".flow .flow-node").nth(2).getByText("1 号泵", { exact: true }).waitFor()
  await page.locator(".kpi-row").getByText("10.8", { exact: false }).waitFor()
  await page.locator(".kpi-row").getByText("1 / 1", { exact: true }).waitFor()
  await page.locator(".kpi-row").getByText("99.0%", { exact: true }).waitFor()
  await page.locator(".customer-preview .mini-table").getByText("12.5", { exact: false }).first().waitFor()
  await page.locator(".customer-preview .alarm-mini").filter({ hasText: "出口压力低" }).first().waitFor()
  assert.equal(await page.locator(".customer-preview").getByText("12.4", { exact: false }).count(), 0, "prototype sample metrics never enter the preview")
  assert((await page.locator(".design-ledger").boundingBox()).height >= 600, "left ledger keeps the full workspace height")
  await page.setViewportSize({ width: 1024, height: 768 })
  if (process.env.DESIGN_SCREENSHOT_DIR) {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.screenshot({ path: `${process.env.DESIGN_SCREENSHOT_DIR}/uniapp-visual-design-final-1280.png` })
    await page.setViewportSize({ width: 1024, height: 768 })
    await page.screenshot({ path: `${process.env.DESIGN_SCREENSHOT_DIR}/uniapp-visual-design-final-1024.png` })
  }
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "1024px viewport has no horizontal page overflow")
  assert.deepEqual(errors, [], "no browser runtime errors")
  console.log(JSON.stringify({ ok: true, viewports: ["1280x800", "1024x768"], errors }))
} finally {
  await browser.close()
}
