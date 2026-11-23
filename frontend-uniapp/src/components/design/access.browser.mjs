import assert from "node:assert/strict"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

const base = "http://127.0.0.1:5174/uniapp/"
let modelConfig = {
  name: "泵 1", profile_id: "vendor-v1", business_node_id: "device-1",
  point_map: { "Active Power": "point-1" }, verified: false,
  verifier: "", verified_at: "", device_serial: "", verification: "",
}
const profile = {
  id: "vendor-v1", vendor: "示例厂商", model: "P-100", protocol: "modbus", points: 1, commands: 0, verified: false,
  point_specs: [{ name: "Active Power", unit: "kW", read_only: true, role: "telemetry" }], command_specs: [],
}
const unitModel = () => ({
  channel_id: "channel-1", server_id: 1, model: modelConfig, profiles: [profile],
  points: [{ id: "point-1", name: "Active Power", server_id: 1, address: 101, io_type: "modbusInputRegister", data_format: "uint16", read_only: true, unit: "kW", scale: 1, offset: 0 }, ...extraPoints],
  commands: [], missing: [],
})
const writes = []
const extraChannels = []
const extraPoints = []
let delayPreview = false
let failDirectories = false
let browser
try {
  browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || "msedge", headless: true })
  const page = await browser.newPage({ viewport: { width: 1024, height: 768 } })
  const button = text => page.locator("uni-button").filter({ hasText: text }).first()
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  const handleApi = async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api/, "")
    const method = request.method()
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    if (path === "/login") return json({ token: "mock-token" })
    if (path === "/snapshot") return json({
      user: { user_id: "admin", policy: { admin: true, role: "admin" } }, roots: ["site-1"],
      nodes: [
        { id: "site-1", type: "site", parent: "", points: [{ type: "description", key: "", time: "", text: "测试现场" }] },
        { id: "device-1", type: "device", parent: "site-1", points: [{ type: "description", key: "", time: "", text: "泵 1" }] },
        { id: "channel-1", type: "modbus", parent: "site-1", points: [{ type: "description", key: "", time: "", text: "测试总线" }, { type: "protocol", key: "", time: "", text: "TCP" }, { type: "uri", key: "", time: "", text: "127.0.0.1:1502" }] },
      ],
    })
    if (path === "/ems/state") return json({ evaluated_at: "2026-11-22T10:00:00Z", values: [{ device_id: "channel-1/1", point_id: "point-1", value: { kind: "number", number: 12.5 }, quality: "good", source_time: "2026-11-22T10:00:00Z" }] })
    if (path === "/ems/quality") return json({ channels: [], devices: [] })
    if (path === "/ems/alarms") return json({ alarms: [] })
    if (path === "/ems/channels") {
      if (failDirectories) return json({ error: "list_failed" }, 503)
      return json({ channels: [{ id: "channel-1", node_id: "channel-1", protocol: "tcp", address: "127.0.0.1:1502", persisted: true, devices: [{ server_id: 1, health: "healthy", points: [{ id: "point-1" }] }] }, ...extraChannels] })
    }
    if (path === "/ems/profiles") return json({ profiles: [profile] })
    if (path === "/device-ledger") {
      if (failDirectories) return json({ error: "list_failed" }, 503)
      return json({ devices: [{ channel_id: "channel-1", channel_name: "测试总线", server_id: 1, name: modelConfig.name, business_node_id: "device-1", business_node_name: "泵 1", profile_id: modelConfig.profile_id, vendor: "示例厂商", model: "P-100", protocol: "TCP", address: "127.0.0.1:1502", point_count: 1, command_count: 0, health: "healthy", success_rate: 100, avg_latency_ms: 2, verified: modelConfig.verified }] })
    }
    if (path === "/modbus/channel-1/units/1/model" && method === "GET") return json(unitModel())
    if (path === "/modbus/channel-2/units/1/model" && method === "GET") return json({ channel_id: "channel-2", server_id: 1, profiles: [profile], points: [], commands: [], missing: [] })
    if (path === "/modbus" && method === "POST") {
      const body = request.postDataJSON()
      writes.push({ path, body })
      extraChannels.push({ id: "channel-2", node_id: "channel-2", protocol: body.protocol.toLowerCase(), address: body.uri, persisted: true, devices: [] })
      return json({ id: "channel-2" })
    }
    if (path === "/modbus/channel-1/io" && method === "POST") {
      const body = request.postDataJSON()
      writes.push({ path, body })
      extraPoints.push({ id: "point-2", name: body.description, server_id: body.server_id, address: body.address, io_type: body.io_type, data_format: body.data_format, read_only: body.read_only, unit: body.units, scale: body.scale, offset: body.offset })
      return json({ id: "point-2" })
    }
    if (path === "/modbus/channel-1/units/1/model" && method === "PUT") {
      const body = request.postDataJSON()
      writes.push({ path, body })
      modelConfig = { ...modelConfig, ...body }
      return json(unitModel())
    }
    if (path === "/modbus/channel-1/units/apply" && method === "POST") {
      const body = request.postDataJSON()
      if (body.dry_run && delayPreview) {
        delayPreview = false
        await new Promise(resolve => setTimeout(resolve, 300))
      }
      writes.push({ path, body })
      return json({ profile_id: body.profile_id, dry_run: body.dry_run, applied: body.dry_run ? 0 : 1, results: [{ server: 1, profile_id: body.profile_id, mapped: 1, exact: 1, scaled: 0, mismatched: 0, missing: [], blocked: [], points: [{ name: "Active Power", point_id: "point-1", verdict: "exact" }] }] })
    }
    if (path === "/modbus/channel-1/line-test") return json({ connected: true, latency_ms: 2, probed: 1, stations: [{ server_id: 1, responded: true, latency_ms: 2, probe: "holdingRegister:0", error: "" }] })
    if (path.startsWith("/ems/screen/")) return json({ title: "测试画面", columns: 2, point_ids: [] })
    return json({})
  }
  await page.route(url => url.pathname.startsWith("/api/"), handleApi)
  await page.goto(`${base}#/pages/login/index`)
  await page.locator("input").nth(0).fill("admin", { force: true })
  await page.locator("input").nth(1).fill("admin1234567", { force: true })
  await button("登录").click()
  await button("设计").click()
  await button(/接设备/).click()
  await page.locator(".card-title").filter({ hasText: "1 · 选型号" }).waitFor()
  await page.locator(".profile-summary").waitFor()
  assert(!(await page.locator(".stepper uni-button").filter({ hasText: /接设备/ }).innerText()).includes("✓"), "unverified devices do not complete the access step")
  await button("选通道").click()
  await button("＋ 新增通道").click()
  const channelInputs = page.locator(".editor-panel input")
  await channelInputs.nth(0).fill("临时 TCP 通道", { force: true })
  await button("保存通道").click()
  await page.getByText("通道已保存", { exact: true }).waitFor()
  await page.locator(".pick").filter({ hasText: "测试总线" }).click()
  await button("选型号").click()
  delayPreview = true
  await button("预览 Profile 匹配").click()
  assert.equal(await button("选通道").getAttribute("disabled"), "true", "stage and channel selection are locked while a Profile preview is pending")
  await page.getByText("完全一致", { exact: true }).waitFor()
  await button("应用 Profile").click()
  await button(/核对点位/).click()
  await button("保存点位映射").click()
  await page.getByText("点位映射已保存", { exact: true }).waitFor()
  await button("＋ 新增物理点").click()
  const pointInputs = page.locator(".editor-panel input")
  await pointInputs.nth(0).fill("备用功率点", { force: true })
  await pointInputs.nth(1).fill("102", { force: true })
  await button("保存物理点").click()
  await page.getByText(/物理点已创建/).waitFor()
  await button(/^验证$/).click()
  await button("只读连通测试").click()
  await page.getByText(/站号 1 有响应/).waitFor()
  const verificationInputs = page.locator(".verification-grid .acc-card").nth(1).locator("input")
  await verificationInputs.nth(0).fill("张工", { force: true })
  await verificationInputs.nth(1).fill("2026-11-22", { force: true })
  await verificationInputs.nth(2).fill("SN-1", { force: true })
  await page.locator(".verification-grid .acc-card").nth(1).locator("uni-checkbox").click()
  await button("选通道").click()
  await page.locator(".station-form input").fill("2", { force: true })
  await button(/^验证$/).click()
  await button("保存验证记录").click()
  await page.getByText("站号已修改，请先读取此站号", { exact: true }).waitFor()
  assert(!writes.some(write => write.path.includes("/units/2/")), "an unloaded station must not inherit the previous station's model")
  await button("选通道").click()
  await page.locator(".station-form input").fill("1", { force: true })
  await button(/^验证$/).click()
  await button("保存验证记录").click()
  await page.getByText("验证记录已保存", { exact: true }).waitFor()
  assert(writes.some(write => write.path === "/modbus" && write.body.description === "临时 TCP 通道"), "channel was created")
  assert(writes.some(write => write.path === "/modbus/channel-1/io" && write.body.description === "备用功率点" && write.body.address === 102), "physical point was created")
  assert(writes.some(write => write.path.endsWith("/units/apply") && write.body.dry_run === true), "Profile dry-run was requested")
  assert(writes.some(write => write.path.endsWith("/units/apply") && write.body.dry_run === false), "Profile apply was requested")
  assert(writes.some(write => write.path.endsWith("/model") && write.body.verified === true && write.body.device_serial === "SN-1"), "verification record was saved")
  for (const [step, title] of [["建现场", "第 1 步 · 建现场"], ["搭台账", "第 2 步 · 搭台账"], ["配策略", "第 4 步 · 配策略"], ["配界面", "第 5 步 · 配界面"], ["交付", "第 6 步 · 交付"]]) {
    await page.locator(".stepper uni-button").filter({ hasText: new RegExp(step) }).first().click()
    await page.getByText(title, { exact: true }).waitFor()
  }
  await button(/接设备/).click()
  await page.locator(".card-title").filter({ hasText: "1 · 选型号" }).waitFor()
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "1024px viewport has no horizontal page overflow")
  assert.deepEqual(errors, [], "no browser runtime errors")

  failDirectories = true
  const failurePage = await browser.newPage({ viewport: { width: 1024, height: 768 } })
  const failureErrors = []
  failurePage.on("pageerror", error => failureErrors.push(error.message))
  await failurePage.route(url => url.pathname.startsWith("/api/"), handleApi)
  await failurePage.goto(`${base}#/pages/login/index`)
  await failurePage.locator("input").nth(0).fill("admin", { force: true })
  await failurePage.locator("input").nth(1).fill("admin1234567", { force: true })
  await failurePage.locator("uni-button").filter({ hasText: "登录" }).click()
  await failurePage.locator("uni-button").filter({ hasText: "设计" }).first().click()
  await failurePage.locator(".stepper uni-button").filter({ hasText: /接设备/ }).click()
  await failurePage.getByText(/通道读取失败/).waitFor()
  await failurePage.getByText(/设备台账读取失败/).waitFor()
  assert.notEqual(await failurePage.locator(".acc-card select").first().getAttribute("disabled"), null, "Profile selection is disabled without a channel")
  assert.equal(await failurePage.locator("uni-button").filter({ hasText: "预览 Profile 匹配" }).getAttribute("disabled"), "true", "Profile preview is disabled without a channel")
  await failurePage.locator("uni-button").filter({ hasText: /设备台账/ }).first().click()
  await failurePage.locator(".pipeline uni-button").filter({ hasText: /^选通道$/ }).click()
  const failureBody = await failurePage.locator("body").innerText()
  assert(!failureBody.includes("还没有通道"), "failed channel loading is not presented as a genuine empty state")
  assert(!failureBody.includes("当前没有设备台账记录"), "failed ledger loading is not presented as a genuine empty state")
  assert.deepEqual(failureErrors, [], "no runtime errors in the failed-directory scenario")
  failDirectories = false
  console.log(JSON.stringify({ ok: true, writes: writes.length, viewport: "1024x768", errors }))
} finally {
  if (browser) await browser.close()
}
