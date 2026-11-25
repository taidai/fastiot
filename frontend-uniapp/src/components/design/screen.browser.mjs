import assert from "node:assert/strict"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || "msedge", headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  const errors = []
  const writes = []
  const screens = new Map()
  let failNextSave = false
  let failHeldSave = false
  let holdNextSave = false
  let releaseSave
  let heldStarted
  page.on("pageerror", error => errors.push(error.message))
  await page.route(url => url.pathname.startsWith("/api/"), async route => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api/, "")
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    if (path === "/login") return json({ token: "mock-token" })
    if (path === "/snapshot") return json({
      user: { user_id: "admin", policy: { admin: true, role: "admin" } }, roots: ["site-1"],
      nodes: [
        { id: "site-1", type: "site", parent: "", points: [{ type: "description", key: "", time: "", text: "教学泵房" }] },
        { id: "area-1", type: "area", parent: "site-1", points: [{ type: "description", key: "", time: "", text: "一层泵房" }] },
        { id: "area-2", type: "area", parent: "site-1", points: [{ type: "description", key: "", time: "", text: "二层泵房" }] },
        { id: "device-1", type: "device", parent: "area-1", points: [{ type: "description", key: "", time: "", text: "1 号泵" }] },
        { id: "pressure", type: "variable", parent: "device-1", points: [{ type: "description", key: "", time: "", text: "出口压力" }, { type: "units", key: "", time: "", text: "MPa" }] },
        { id: "power", type: "variable", parent: "device-1", points: [{ type: "description", key: "", time: "", text: "总功率" }, { type: "units", key: "", time: "", text: "kW" }] },
      ],
    })
    if (path === "/ems/state") return json({ evaluated_at: "2026-11-23T10:00:00Z", values: [
      { device_id: "device-1", point_id: "pressure", value: { kind: "number", number: 12.5 }, quality: "good", source_time: "2026-11-23T10:00:00Z" },
      { device_id: "device-1", point_id: "power", value: { kind: "number", number: 10.8 }, quality: "good", source_time: "2026-11-23T10:00:00Z" },
    ] })
    if (path === "/ems/quality") return json({ channels: [], devices: [{ device_id: "device-1", health: "healthy", window_5m: { polls: 100, failures: 0, success_rate: 100 } }] })
    if (path === "/ems/alarms") return json({ alarms: [] })
    if (path.startsWith("/ems/screen/")) {
      const id = decodeURIComponent(path.slice("/ems/screen/".length))
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON()
        writes.push({ id, body })
        if (failNextSave) { failNextSave = false; return json({ error: "version_conflict" }, 409) }
        if (holdNextSave) {
          holdNextSave = false
          heldStarted?.()
          await new Promise(resolve => { releaseSave = resolve })
        }
        if (failHeldSave) { failHeldSave = false; return json({ error: "version_conflict" }, 409) }
        if (body.version !== (screens.get(id)?.version || 0)) return json({ error: "version_conflict" }, 409)
        const applied = body.apply_to_same ? 3 : 1
        const saved = { ...body, version: (screens.get(id)?.version || 0) + 1 }
        delete saved.apply_to_same
        screens.set(id, saved)
        return json({ saved: true, version: saved.version, applied })
      }
      if (screens.has(id)) return json(screens.get(id))
      if (screens.has("site-1") && id !== "site-1") return json({ ...screens.get("site-1"), inherited_from: "site-1" })
      if (id === "site-1") return json({ title: "教学画面", columns: 3, point_ids: ["power", "pressure"] })
      return json({ error: "screen_not_found" }, 404)
    }
    return json({})
  })
  await page.goto("http://127.0.0.1:5174/uniapp/?live=1#/pages/login/index")
  await page.locator("input").nth(0).fill("admin", { force: true })
  await page.locator("input").nth(1).fill("fixture-password-not-a-real-account", { force: true })
  await page.locator("uni-button").filter({ hasText: "登录" }).first().click()
  await page.waitForURL(/pages\/overview\/index/)
  await page.goto("http://127.0.0.1:5174/uniapp/?live=1#/pages/design/index")
  await page.locator(".stepper").waitFor()
  await page.locator(".stepper uni-button").filter({ hasText: "配界面" }).waitFor({ timeout: 5000 }).catch(async error => { console.log(await page.locator("body").innerText()); console.log(errors); throw error })
  await page.locator(".stepper uni-button").filter({ hasText: "配界面" }).click()
  await page.locator("uni-button").filter({ hasText: "保存本层" }).waitFor()

  assert.equal(await page.locator(".kpi-row .kpi").count(), 6)
  await page.locator('.cfg-row[data-block="recent"] uni-button').filter({ hasText: "↑" }).click()
  assert.deepEqual(await page.locator('.cfg-slot[data-slot="main"] .cfg-row').evaluateAll(rows => rows.map(row => row.getAttribute("data-block"))), ["flow", "recent", "devices"])
  await page.locator('.cfg-row[data-block="kpi"] uni-button').filter({ hasText: "配置" }).click()
  await page.getByLabel("指标 1 绑定").selectOption("point:pressure")
  assert.equal(await page.locator(".kpi-row .kpi > span").first().innerText(), "出口压力")
  assert((await page.locator(".kpi-row .kpi").first().innerText()).includes("12.5"), "binding changes update the real customer preview immediately")
  await page.locator("uni-button").filter({ hasText: "保存本层" }).click()
  await page.locator(".save-status").filter({ hasText: "已保存本层" }).waitFor()
  assert.equal(writes.length, 1)
  assert.equal(writes[0].body.version, 0)
  assert.deepEqual(writes[0].body.slots.main, ["flow", "recent", "devices"])
  assert.equal(writes[0].body.point_ids[0], "pressure")

  const beforeReset = writes.length
  await page.locator("uni-button").filter({ hasText: "恢复默认" }).click()
  await page.locator(".save-status").filter({ hasText: "尚未保存" }).waitFor()
  assert.equal(writes.length, beforeReset, "restore default is an explicit unsaved draft change")

  await page.locator("uni-button").filter({ hasText: "保存并应用到同类层" }).click()
  await page.locator(".save-status").filter({ hasText: "含本层" }).waitFor()
  assert.equal(writes.at(-1).body.apply_to_same, true)

  await page.locator(".design-ledger .node").filter({ hasText: "一层泵房" }).click()
  await page.locator(".cfg-bar").getByText("继承自 教学泵房", { exact: false }).waitFor()
  await page.locator("uni-button").filter({ hasText: "保存本层" }).click()
  await page.locator(".save-status").filter({ hasText: "已保存本层" }).waitFor()
  assert.equal(writes.at(-1).id, "area-1")
  assert.equal(writes.at(-1).body.version, 0, "an inherited document becomes version zero of the child")

  failNextSave = true
  await page.locator("uni-button").filter({ hasText: "保存本层" }).click()
  await page.locator(".save-error").filter({ hasText: "版本" }).waitFor()
  assert.equal(await page.locator(".save-status").count(), 0, "failed writes never show success")

  await page.locator('.cfg-row[data-block="kpi"] uni-button').filter({ hasText: "配置" }).click()
  holdNextSave = true
  const editStarted = new Promise(resolve => { heldStarted = resolve })
  await page.locator("uni-button").filter({ hasText: "保存本层" }).click()
  await editStarted
  await page.getByLabel("指标 1 绑定").selectOption("point:power")
  releaseSave()
  await page.locator(".save-status").filter({ hasText: "后续修改" }).waitFor()
  assert.equal(await page.getByLabel("指标 1 绑定").inputValue(), "point:power", "a late save response cannot overwrite edits made while it was in flight")

  holdNextSave = true
  const switchStarted = new Promise(resolve => { heldStarted = resolve })
  await page.locator("uni-button").filter({ hasText: "保存本层" }).click()
  await switchStarted
  await page.locator(".design-ledger .node").filter({ hasText: "二层泵房" }).click()
  releaseSave()
  await page.locator(".cfg-bar").getByText("二层泵房", { exact: false }).waitFor()
  assert.equal(await page.locator(".save-status").count(), 0, "a late save response never reports success in a different layer")
  assert.equal(await page.locator(".cfg-bar").getByText("本层已配", { exact: false }).count(), 0, "a late response cannot mark the new layer as saved")

  holdNextSave = true
  failHeldSave = true
  const failureStarted = new Promise(resolve => { heldStarted = resolve })
  await page.locator("uni-button").filter({ hasText: "保存本层" }).click()
  await failureStarted
  await page.locator(".design-ledger .node").filter({ hasText: "教学泵房" }).click()
  releaseSave()
  await page.locator(".cfg-bar").getByText("教学泵房 · 现场", { exact: false }).waitFor()
  await page.waitForTimeout(100)
  assert.equal(await page.locator(".save-error").count(), 0, "a late failure cannot report an error in a different layer")
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "1280px viewport has no horizontal page overflow")
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ ok: true, writes: writes.length, errors }))
} finally {
  await browser.close()
}
