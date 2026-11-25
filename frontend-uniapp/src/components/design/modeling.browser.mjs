import assert from "node:assert/strict"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

const base = "http://127.0.0.1:5174/uniapp/"
const profile = { id: "pcs-v1", vendor: "厂商", model: "PCS V1", point_specs: [{ name: "功率", read_only: true }], command_specs: [] }
const safeProfile = { ...profile, point_specs: [...profile.point_specs, { name: "测试定值", read_only: false, note: "L3_SAFE 固定测试" }, { name: "反馈", read_only: true }], command_specs: [{ id: "safe", name: "安全测试", point: "测试定值", value: 1, readback: "反馈", readback_value: 1 }] }
let allowSafe = false
const currentProfile = () => allowSafe ? safeProfile : profile
const nodes = [
  { id: "site-1", type: "site", parent: "", points: [{ type: "description", text: "测试现场" }] },
  { id: "area-1", type: "area", parent: "site-1", points: [{ type: "description", text: "泵房" }] },
  { id: "unit-1", type: "unit", parent: "area-1", points: [{ type: "description", text: "供水单元" }, { type: "easyemsUnitMembers", text: JSON.stringify([{ role: "驱动", required: true }]) }] },
  { id: "device-1", type: "device", parent: "unit-1", points: [{ type: "description", text: "泵 1" }] },
  { id: "channel-1", type: "modbus", parent: "site-1", points: [{ type: "description", text: "总线 A" }, { type: "protocol", text: "TCP" }, { type: "uri", text: "127.0.0.1:1502" }] },
  { id: "point-1", type: "modbusIo", parent: "channel-1", points: [{ type: "description", text: "功率" }] },
]
const writes = []
async function waitForWrite(predicate, message) {
  for (let attempt = 0; attempt < 100 && !writes.some(predicate); attempt += 1) await new Promise(resolve => setTimeout(resolve, 50))
  assert.ok(writes.some(predicate), message)
}
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || "msedge", headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  const handleApi = async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api/, "")
    const method = request.method()
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    if (path === "/login") return json({ token: "mock-token" })
    if (path === "/snapshot") return json({ user: { user_id: "admin", policy: { admin: true, role: "admin" } }, roots: ["site-1"], nodes })
    if (path === "/templates") return json({ site_templates: [
      { id: "blank", name: "空白现场", description: "空白", nodes: [] },
      { id: "pump-house", name: "供水泵房", description: "泵房", nodes: [{ name: "泵房", type: "area", children: [{ name: "供水单元", type: "unit", children: [{ name: "泵驱动", type: "device" }] }] }] },
      { id: "storage", name: "储能现场", description: "储能", nodes: [{ name: "储能区", type: "area", children: [{ name: "储能单元", type: "energyUnit", children: [{ name: "PCS", type: "device" }] }] }] },
    ] })
    if (path === "/nodes" && method === "POST") {
      const body = request.postDataJSON(); writes.push({ path, body })
      return json({ id: body.site_template ? "new-site" : "new-device", created: 4 })
    }
    if (path === "/nodes/unit-1/copy") return json({ id: "unit-copy", created: 2, skipped: 0, confirm: [] })
    if (path === "/nodes/unit-1" && method === "PATCH") { writes.push({ path, body: request.postDataJSON() }); return json({ saved: true, missing_required: [] }) }
    if (path === "/access/batch") {
      const body = request.postDataJSON(); writes.push({ path, body })
      if (!body.dry_run) nodes.find(node => node.id === "channel-1").points.push({ type: "easyemsUnitModel", key: "1", text: JSON.stringify({ profile_id: "pcs-v1", business_node_id: "device-1", point_map: { 功率: "point-1" } }) })
      return json({ dry_run: body.dry_run, applied: body.dry_run ? 0 : 1, rows: [{ line: 2, device: "device-1", model: "pcs-v1", channel: "channel-1", station: 1, device_id: "device-1", profile_id: "pcs-v1", channel_id: "channel-1", valid: true, status: body.dry_run ? "ready" : "mapped", errors: [], created_points: 1, mapped: body.dry_run ? 0 : 1, missing: [] }] })
    }
    if (path === "/ems/channels") return json({ channels: [{ id: "channel-1", protocol: "tcp", address: "127.0.0.1:1502", devices: [{ server_id: 1, health: "healthy" }] }] })
    if (path === "/ems/profiles") return json({ profiles: [currentProfile()] })
    if (path === "/device-ledger") return json({ devices: [] })
    if (path === "/modbus/channel-1/units/1/model") return json({ channel_id: "channel-1", server_id: 1, model: { name: "站号 1", profile_id: "pcs-v1", point_map: { 功率: "point-1" }, verified: false }, profiles: [currentProfile()], points: [{ id: "point-1", name: "功率", server_id: 1, address: 1, io_type: "modbusInputRegister", read_only: true, scale: 1 }], commands: [] })
    if (path === "/modbus/channel-1/units/1/verify" && method === "GET") return json({ records: [] })
    if (path === "/modbus/channel-1/units/1/verify" && method === "POST") {
      const body = request.postDataJSON(); writes.push({ path, body })
      return json({ level: body.level, passed: true, reasons: [], recorded: true, points: body.level === "L2" ? [{ name: "功率", point_id: "point-1", passed: true, quality: "good", source_time: "2026-11-23T00:00:00Z" }] : undefined, command: body.level === "L3" ? { state: "confirmed" } : undefined })
    }
    if (path === "/ems/state") return json({ values: [{ device_id: "channel-1/1", point_id: "point-1", value: { kind: "number", number: 1 }, quality: "good" }] })
    if (path === "/ems/quality" || path === "/ems/alarms") return json(path.endsWith("alarms") ? { alarms: [] } : { channels: [], devices: [] })
    if (path.startsWith("/ems/screen/")) return json({ title: "测试画面", columns: 2, point_ids: [] })
    return json({})
  }
  await page.route(url => url.pathname.startsWith("/api/"), handleApi)
  const button = text => page.locator("uni-button").filter({ hasText: text }).first()
  await page.goto(`${base}?live=1#/pages/login/index`)
  await page.locator("input").nth(0).fill("admin", { force: true })
  await page.locator("input").nth(1).fill("fixture-password-not-a-real-account", { force: true })
  await button("登录").click()
  await page.waitForURL(/pages\/overview\/index/)
  await page.goto(`${base}?live=1#/pages/design/index`)
  await page.locator(".stepper").waitFor()
  await page.locator(".tpl-card").filter({ hasText: "储能现场" }).click()
  await page.locator(".actions .site-name input").fill("新储能现场", { force: true })
  await button(/建出来 →|建空白项目 →/).click()
  await page.locator(".ledger-worktable").waitFor()
  assert(writes.some(write => write.path === "/nodes" && write.body.site_template === "storage" && write.body.description === "新储能现场"), "site template creates a real backend tree")
  await button("＋ 设备").click()
  await page.locator(".create-line input").fill("PCS 2", { force: true })
  await page.locator(".create-line").getByText("创建", { exact: true }).click()
  await page.locator(".create-line").waitFor({ state: "detached" })
  assert(writes.some(write => write.path === "/nodes" && write.body.type === "device" && write.body.description === "PCS 2"), "device placeholder is created")
  const unitRow = page.locator(".ledger-worktable tr").filter({ hasText: "供水单元" }).first()
  await unitRow.getByText("复制", { exact: true }).click()
  const deviceRow = page.locator(".ledger-worktable tr").filter({ hasText: "泵 1" }).first()
  await deviceRow.locator("uni-checkbox").click()
  await button("去批量接入").click()
  await page.getByText("批量接入 · 设备占位", { exact: true }).waitFor()
  await button("生成批量草稿").click(); await button("CSV 预检").click()
  await page.getByText("可导入", { exact: true }).waitFor()
  await button("批量套用可导入项").click()
  await page.getByText(/已保存 1 台设备映射/).waitFor()
  assert(writes.some(write => write.path === "/access/batch" && write.body.dry_run === false && write.body.csv.includes("device-1")), "batch apply persists the selected device ID")
  await button("退出批量").click()
  await page.locator(".stepper uni-button").filter({ hasText: /搭台账/ }).click()
  const unit = page.locator(".ledger-worktable tr").filter({ hasText: "供水单元" }).first()
  await unit.getByText("选中", { exact: true }).click()
  await page.getByText("单元成员角色", { exact: true }).waitFor()
  await page.locator(".member-roles select").selectOption("device-1")
  await button("保存成员绑定").click()
  await waitForWrite(write => write.path === "/nodes/unit-1" && write.body.members?.[0]?.node_id === "device-1", "member role saves the real device binding")
  await page.locator(".design-ledger .node").filter({ hasText: "泵 1" }).click()
  await button(/^验证$/).click()
  if (process.env.MODEL_DEBUG) console.log(await page.locator(".access-step").innerText())
  await button("L2 必需点验证（只读）").click()
  await page.getByText("L2：通过", { exact: true }).waitFor()
  await page.getByText("当前 Profile 没有声明可安全写回读的命令，L3 不可执行。", { exact: true }).waitFor()

  allowSafe = true
  const safePage = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  safePage.on("pageerror", error => errors.push(error.message))
  await safePage.route(url => url.pathname.startsWith("/api/"), handleApi)
  const safeButton = text => safePage.locator("uni-button").filter({ hasText: text }).first()
  await safePage.goto(`${base}?live=1#/pages/login/index`)
  await safePage.locator("input").nth(0).fill("admin", { force: true })
  await safePage.locator("input").nth(1).fill("fixture-password-not-a-real-account", { force: true })
  await safeButton("登录").click()
  await safePage.waitForURL(/pages\/overview\/index/)
  await safePage.goto(`${base}?live=1#/pages/design/index`)
  await safePage.locator(".stepper").waitFor(); await safeButton(/接设备/).click()
  await safeButton("接入新站号").click()
  await safeButton(/^验证$/).click()
  await safePage.locator(".l3-box select").waitFor()
  const execute = safeButton("执行一次 L3 写回读")
  assert.equal(await execute.getAttribute("disabled"), "true", "L3 stays disabled before explicit confirmation")
  await safePage.locator(".l3-box uni-checkbox").click()
  assert.equal(await execute.getAttribute("disabled"), null, "explicit confirmation enables the declared safe command")
  await execute.click()
  await safePage.getByText("L3：通过", { exact: true }).waitFor()
  assert(writes.some(write => write.path.endsWith("/verify") && write.body.level === "L3" && write.body.command_id === "safe" && write.body.confirm === true), "L3 submits only the confirmed safe command")
  assert.deepEqual(errors, [], "no browser runtime errors")
  console.log(JSON.stringify({ ok: true, writes: writes.length, viewport: "1280x900", errors }))
} finally {
  await browser.close()
}
