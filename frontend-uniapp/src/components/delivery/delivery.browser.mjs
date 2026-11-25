import assert from "node:assert/strict"
import { resolve } from "node:path"
import { pathToFileURL } from "node:url"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

const base = "http://127.0.0.1:5174/uniapp/"
const description = text => ({ type: "description", key: "", time: "", text })
let preflightRuns = 0
let versions = [{ id: "pv-old", site_id: "site-1", site_name: "泵房", created_at: "2026-11-20T08:00:00Z", operator: "admin", note: "旧版", preflight_status: "warn", nodes: 3 }]
let industryInstalled = false
let evidenceForbidden = false
let importError = ""
let importedNodes = []
let runtimeValues = []
let runtimeChannels = []
const writes = []
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || "msedge", headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, acceptDownloads: true })
  const button = name => page.locator("uni-button").filter({ hasText: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
  const errors = []
  const downloads = []
  page.on("download", download => downloads.push(download))
  page.on("pageerror", error => errors.push(error.message))
  await page.route(url => new URL(url).pathname.startsWith("/api/"), async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api/, "")
    const method = request.method()
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    if (path === "/login") return json({ token: "delivery-test-token" })
    if (path === "/snapshot") return json({
      user: { user_id: "admin", email: "admin", policy: { admin: true, role: "admin", actions: [] }, actions: [] },
      roots: ["root-project"],
      nodes: [
        { id: "root-project", type: "group", parent: "", points: [description("交付项目")] },
        { id: "site-1", type: "site", parent: "root-project", points: [description("泵房")] },
        { id: "site-2", type: "site", parent: "root-project", points: [description("二期")] },
        { id: "channel-existing", type: "modbus", parent: "site-1", points: [description("既有总线"), { type: "protocol", key: "", text: "TCP" }, { type: "uri", key: "", text: "127.0.0.1:1502" }] },
        ...importedNodes,
      ],
    })
    if (path === "/ems/state") return json({ values: runtimeValues, evaluated_at: "2026-11-22T10:00:00Z" })
    if (path === "/ems/quality") return json({ channels: runtimeChannels, devices: [] })
    if (path === "/ems/channels") return json({ channels: [
      { id: "channel-existing", protocol: "TCP", address: "127.0.0.1:1502", devices: [] },
      { id: "channel-imported", protocol: "TCP", address: "", devices: [] },
    ] })
    if (path === "/ems/alarms") return json({ alarms: [] })
    if (path === "/delivery/gates") return json({ generated_at: "2026-11-22T10:00:00Z", status: "fail", summary: { pass: 8, warn: 1, fail: 1 }, gates: [
      { key: "channels", title: "通道", status: "fail", detail: "1 条通道未启动", object: "总线 A", next_step: "检查通道地址与协议配置" },
      { key: "permissions", title: "权限边界", status: "pass", detail: "服务端逐请求校验" },
    ] })
    if (path === "/delivery/preflight" && method === "GET") {
      const site = url.searchParams.get("site") || ""
      if (site === "site-imported") return json({ latest: { id: "pf-imported", site, generated_at: "2026-11-22T10:03:00Z", operator: "admin", status: "pass", summary: { pass: 10, warn: 0, fail: 0 }, gates: [] } })
      return json({ latest: { id: "pf-old", site: site || undefined, generated_at: "2026-11-21T10:00:00Z", operator: "admin", status: "warn", summary: { pass: 8, warn: 2, fail: 0 }, gates: [] } })
    }
    if (path === "/delivery/preflight" && method === "POST") {
      preflightRuns++
      assert.equal(url.searchParams.get("site"), "site-1")
      return json({ id: "pf-new", generated_at: "2026-11-22T10:01:00Z", operator: "admin", status: "pass", summary: { pass: 10, warn: 0, fail: 0 }, gates: [] })
    }
    if (path === "/projects/versions") return json({ versions })
    if (path === "/projects/publish") {
      const body = request.postDataJSON()
      writes.push({ path, body })
      if (!body.confirm) return json({ error: "preflight_failed" }, 409)
      const created = { id: "pv-new", site_id: body.site, site_name: "泵房", created_at: "2026-11-22T10:02:00Z", operator: "admin", note: body.note, preflight_status: "fail", nodes: 3 }
      versions = [created, ...versions]
      return json(created)
    }
    if (path === "/projects/export") return json({ id: "pp-1", name: "泵房", format: "fastiot.project-package", format_version: 2, point_count: 12, strategy_count: 3, dependencies: [{ kind: "profile", id: "pump", version: "abc" }], nodes: [{ ref: "n1" }], screens: [], stripped: [{ reason: "凭据" }], warnings: [] })
    if (path === "/projects/import") {
      if (importError) return json({ error: importError }, 400)
      const body = request.postDataJSON()
      writes.push({ path, body })
      importedNodes = [
        { id: "site-imported", type: "site", parent: "root-project", points: [description(body.package.name)] },
        { id: "channel-imported", type: "modbus", parent: "site-imported", points: [description("导入总线"), { type: "protocol", key: "", text: "TCP" }] },
        { id: "point-imported", type: "modbusIo", parent: "channel-imported", points: [description("包内点")] },
        { id: "rule-imported", type: "decisionTable", parent: "site-imported", points: [description("包内规则")] },
      ]
      return json({ site_id: "site-imported", name: body.package.name, created: 4, channels: 1, rules: 1, node_ids: ["site-imported", "channel-imported", "point-imported", "rule-imported"], version_id: "pv-import", warnings: ["需检查画面"] })
    }
    if (path === "/modbus/channel-imported" && method === "PUT") {
      const body = request.postDataJSON()
      writes.push({ path, body })
      importedNodes.push({ id: "point-new-after-rebind", type: "modbusIo", parent: "channel-imported", points: [description("重绑新增点")] })
      runtimeValues = [{ point_id: "point-new-after-rebind", device_id: "channel-imported/1", value: { kind: "number", number: 42 }, quality: "good", source_time: "2026-11-22T10:04:00Z" }]
      runtimeChannels = [{ channel_id: "channel-imported", health: "healthy", polls: 1 }]
      return json({ saved: true })
    }
    if (path === "/modbus/channel-existing" && method === "PUT") {
      writes.push({ path, body: request.postDataJSON() })
      return json({ saved: true })
    }
    if (path === "/delivery/evidence") {
      if (evidenceForbidden) return json({ error: "forbidden" }, 403)
      return route.fulfill({ status: 200, contentType: "application/zip", body: Buffer.from([0x50, 0x4b, 0x03, 0x04]) })
    }
    if (path === "/projects/rollback") {
      const body = request.postDataJSON()
      writes.push({ path, body })
      return json({ version: body.version, updated: 2, created: 1, extra: 1, preflight_status: "warn" })
    }
    if (path === "/users" && method === "GET") return json({ users: [
      { id: "u-admin", email: "admin", policy: { admin: true, role: "admin", root_node_ids: ["root-project"], actions: [] } },
      { id: "u-viewer", email: "viewer@local", policy: { admin: false, role: "viewer", root_node_ids: ["site-1"], actions: [] } },
      { id: "u-rule-only", email: "rule-only@local", policy: { admin: false, role: "viewer", root_node_ids: ["site-1"], actions: ["rule_publish"] } },
    ], actions: ["configure", "export", "account_manage", "audit_view", "site_settings"] })
    if (path === "/users" && method === "POST") {
      const body = request.postDataJSON(); writes.push({ path, body }); return json({ id: "u-new" })
    }
    if (path === "/users/u-viewer" && method === "PUT") {
      const body = request.postDataJSON(); writes.push({ path, body }); return json({ saved: true })
    }
    if (path === "/audit") return json({ records: [{ time: "2026-11-22T10:00:00Z", actor: "admin", action: "项目发布", object: "pv-old", detail: "3 个节点", result: "success" }], actions: ["项目发布", "项目回退"] })
    if (path === "/industry/packages" && method === "GET") return json({ packages: [{ id: "ems", version: "1.0.0", label: "EMS 行业包", provides: ["能源单元、设备与能力约束"], installed: industryInstalled, installed_at: industryInstalled ? "2026-11-22T10:00:00Z" : "" }] })
    if (path === "/industry/packages" && method === "POST") { industryInstalled = true; writes.push({ path, body: request.postDataJSON() }); return json({}) }
    if (path === "/industry/packages/ems" && method === "DELETE") { industryInstalled = false; writes.push({ path, body: {} }); return json({}) }
    return json({})
  })

  await page.goto(`${base}?live=1#/pages/login/index`)
  await page.locator("input").nth(0).fill("admin")
  await page.locator("input").nth(1).fill("fixture-password-not-a-real-account")
  await button("登录").click()
  await page.waitForURL(/pages\/overview\/index/)
  await page.goto(`${base}?live=1#/pages/delivery/index`)
  await page.getByText("预检与门槛", { exact: true }).waitFor()
  await page.locator(".package-grid").waitFor()
  await page.locator(".versions-card").waitFor()
  await page.locator(".import-card").waitFor()
  await page.locator(".package-contents").getByText("Profile pump@abc", { exact: true }).waitFor()
  await page.locator(".package-contents").getByText(/12 个采集点 · 画面绑定已 token 化/).waitFor()
  await page.locator(".package-contents").getByText("3 条策略", { exact: true }).waitFor()
  if (process.env.DELIVERY_SHOT_DIR) {
    for (const width of [1280, 1024]) {
      await page.setViewportSize({ width, height: width === 1280 ? 800 : 768 })
      await page.screenshot({ path: `${process.env.DELIVERY_SHOT_DIR}/uniapp-visual-delivery-fixture-${width}.png`, fullPage: true })
    }
    await page.setViewportSize({ width: 1280, height: 800 })
  }
  if (process.env.DELIVERY_PROTOTYPE_CHECK) {
    const prototype = await browser.newPage({ viewport: { width: 1280, height: 800 } })
    await prototype.goto(pathToFileURL(resolve("frontend/prototype/overview-hd/交付 · 玻璃磨砂.html")).href)
    await prototype.locator(".two-col > .card").first().waitFor()
    const expected = await prototype.evaluate(() => {
      const rect = element => { const box = element.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height } }
      return {
        side: rect(document.querySelector(".side")),
        statebar: rect(document.querySelector(".statebar")),
        left: rect(document.querySelector(".two-col > .card:nth-child(1)")),
        right: rect(document.querySelector(".two-col > .card:nth-child(2)")),
        versions: rect(document.querySelectorAll(".stage > .card")[0]),
        cross: rect(document.querySelectorAll(".stage > .card")[1]),
      }
    })
    const actual = await page.evaluate(() => {
      const rect = element => { const box = element.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height } }
      return {
        side: rect(document.querySelector(".side")),
        statebar: rect(document.querySelector(".statebar")),
        left: rect(document.querySelector(".package-card")),
        right: rect(document.querySelector(".package-contents")),
        versions: rect(document.querySelector(".versions-card")),
        cross: rect(document.querySelector(".import-card")),
      }
    })
    const deltas = {}
    const sharedHeaderDelta = actual.side.y - expected.side.y
    console.log(JSON.stringify({ prototypeGeometry: expected, currentGeometry: actual, sharedHeaderDelta }))
    for (const key of Object.keys(expected)) {
      deltas[key] = {}
      for (const prop of ["x", "y", "width", "height"]) {
        if (prop === "y" && (key === "side" || key === "statebar")) continue
        const delta = prop === "y"
          ? (actual[key].y - actual.side.y) - (expected[key].y - expected.side.y)
          : actual[key][prop] - expected[key][prop]
        deltas[key][prop] = delta
        assert(Math.abs(delta) <= 2, `${key}.${prop} differs from prototype by ${delta}px`)
      }
    }
    const styleCases = [
      ["card", ".two-col > .card:first-child", ".package-grid > .package-card:first-child", ["paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "borderRadius", "borderTopWidth", "backgroundColor", "backgroundImage", "boxShadow"]],
      ["row", ".two-col > .card:first-child .row:nth-child(2)", ".package-grid > .package-card:first-child .row:nth-child(2)", ["fontSize", "lineHeight", "paddingTop", "paddingBottom"]],
      ["primary", ".two-col .actions .primary", ".package-card .actions .primary", ["fontSize", "lineHeight", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "borderRadius"]],
      ["ghost", ".two-col .actions .ghost", ".package-card .actions .ghost", ["fontSize", "lineHeight", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "borderRadius"]],
      ["cardTitle", ".two-col > .card:nth-child(2) h5", ".package-contents h5", ["fontSize", "fontWeight", "letterSpacing", "marginBottom"]],
      ["note", ".two-col > .card:first-child .note", ".package-card .note", ["fontSize", "lineHeight", "marginTop"]],
    ]
    for (const [name, prototypeSelector, currentSelector, props] of styleCases) {
      const read = (element, props) => Object.fromEntries(props.map(prop => [prop, getComputedStyle(element)[prop]]))
      const expectedStyle = await prototype.locator(prototypeSelector).first().evaluate(read, props)
      const actualStyle = await page.locator(currentSelector).first().evaluate(read, props)
      assert.deepEqual(actualStyle, expectedStyle, `${name} computed styles`)
    }
    await prototype.close()
    console.log(JSON.stringify({ prototypeAlignment: "within 2px", deltas }))
  }
  assert.equal(await page.locator(".gate").count(), 0, "preflight details start collapsed")
  await button("展开门槛").click()
  await page.getByText("1 条通道未启动", { exact: true }).waitFor()
  await page.getByText("对象：总线 A", { exact: true }).waitFor()
  await page.getByText(/最近预检：注意/).waitFor()
  await button("运行预检").click()
  await page.getByText(/最近预检：通过/).waitFor()
  assert.equal(preflightRuns, 1)

  await page.locator(".publish-note input").fill("交付基线", { force: true })
  await button("重新发布").click()
  await button("预检未通过，仍要发布").click()
  await page.getByText(/已发布版本 pv-new/).waitFor()
  assert.equal(writes.filter(write => write.path === "/projects/publish").length, 2)
  assert.equal(writes.filter(write => write.path === "/projects/publish")[1].body.site, "site-1")

  const packageDownload = page.waitForEvent("download")
  await button("导出项目包").click()
  assert.equal((await packageDownload).suggestedFilename(), "泵房.package.json")
  await page.locator(".package-contents").getByText("Profile pump@abc", { exact: true }).waitFor()

  const evidenceDownload = page.waitForEvent("download")
  await button("导出验收证据").click()
  assert.match((await evidenceDownload).suggestedFilename(), /\.zip$/)
  evidenceForbidden = true
  await button("导出验收证据").click()
  await page.getByText("当前账号无权执行此操作", { exact: true }).waitFor()
  assert.equal(downloads.length, 2, "HTTP errors must not be downloaded as ZIP files")

  importError = "incompatible_dependency:profile:pump:required@abc123:current@def456"
  let fileChooserEvent = page.waitForEvent("filechooser")
  await button("导入到新实例").click()
  let fileChooser = await fileChooserEvent
  await fileChooser.setFiles({ name: "project.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify({ format: "fastiot.project-package", format_version: 2, name: "拒绝站", nodes: [{ ref: "n1" }] })) })
  await page.getByText("Profile 依赖版本不匹配：pump 需要 abc123，当前为 def456；已拒绝导入，未创建任何节点", { exact: true }).waitFor()
  assert.equal(await button("重绑通道与设备").count(), 0, "refused imports must not show a rebind guide")
  importError = ""

  fileChooserEvent = page.waitForEvent("filechooser")
  await button("导入到新实例").click()
  fileChooser = await fileChooserEvent
  await fileChooser.setFiles({ name: "project.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify({ format: "fastiot.project-package", format_version: 2, name: "导入站", nodes: [{ ref: "n1" }] })) })
  await page.getByText(/已导入「导入站」/).waitFor()
  const imported = writes.find(write => write.path === "/projects/import")
  assert.equal(imported.body.parent, "root-project")
  assert.equal(imported.body.package.name, "导入站")
  await button("重绑通道与设备").click()
  const rebindPanel = page.locator(".rebind-panel")
  await rebindPanel.getByText(/现场连接重绑/).waitFor()
  await rebindPanel.getByText("导入总线", { exact: true }).waitFor()
  assert.equal(await rebindPanel.getByText("既有总线", { exact: true }).count(), 0, "rebind must not fall back to an existing site's channel")
  await button("编辑现场连接").click()
  await rebindPanel.locator("uni-label").filter({ hasText: "TCP 目标地址" }).locator("input").fill("127.0.0.1:2502", { force: true })
  await button("保存现场连接").click()
  await rebindPanel.getByText("现场连接已保存", { exact: true }).waitFor()
  assert.equal(writes.filter(write => write.path === "/modbus/channel-imported").length, 1)
  assert.equal(writes.filter(write => write.path === "/modbus/channel-existing").length, 0)
  await page.getByText("运行状态（真实快照）", { exact: true }).waitFor()
  await page.getByText("采集样本 1 个", { exact: true }).waitFor()
  await page.locator(".runtime-card").getByText(/通过 · /).waitFor()
  await page.locator(".site-select").selectOption("site-2")
  await page.locator(".runtime-card").getByText(/通过 · /).waitFor()
  await page.locator(".site-select").selectOption("site-1")

  await button("部署记录").click()
  await page.getByText(/后端没有独立的/).waitFor()
  await page.getByText("已导入 导入站；不是部署", { exact: true }).waitFor()
  await button("版本回退").click()
  await page.getByText(/版本或当前工程包含计算点/).waitFor()
  const rollbackRow = page.locator(".version-row").filter({ hasText: "pv-old" })
  await rollbackRow.locator("uni-button").filter({ hasText: "回退" }).click()
  await rollbackRow.locator("uni-button").filter({ hasText: "确认回退（历史保留）" }).click()
  await page.getByText(/已回退到 pv-old：更新 2、创建 1、保留多余 1/).waitFor()
  assert.equal(writes.find(write => write.path === "/projects/rollback").body.site, "site-1")

  await button("账号与权限").click()
  await page.getByText("viewer@local", { exact: true }).first().waitFor()
  await page.getByText("角色 × 动作 × 对象", { exact: true }).waitFor()
  const viewerMatrixRow = page.locator(".permission-matrix tbody tr").filter({ hasText: "viewer@local" })
  await viewerMatrixRow.getByText("泵房", { exact: true }).waitFor()
  assert.equal(await viewerMatrixRow.locator("td").nth(4).innerText(), "拒绝", "viewer command must be denied")
  const adminMatrixRow = page.locator(".permission-matrix tbody tr").filter({ hasText: "管理员" })
  assert.equal(await adminMatrixRow.locator("td").nth(7).innerText(), "允许", "admin account management must be allowed")
  const ruleOnlyMatrixRow = page.locator(".permission-matrix tbody tr").filter({ hasText: "rule-only@local" })
  assert.equal(await ruleOnlyMatrixRow.locator("td").nth(6).innerText(), "拒绝", "rule_publish alone must not grant project version publishing")
  const viewer = page.locator(".account-table tbody tr").filter({ hasText: "viewer@local" })
  await viewer.locator("uni-button").filter({ hasText: "编辑权限" }).click()
  const accountEditor = page.locator(".accounts-card .editor").first()
  await accountEditor.locator("select").selectOption("operator")
  await accountEditor.locator(".action-grid uni-label").filter({ hasText: "审计查看" }).locator("uni-checkbox").click()
  await accountEditor.locator("uni-button").filter({ hasText: "保存权限" }).click()
  await page.getByText("权限已保存", { exact: true }).waitFor()
  assert.deepEqual(writes.find(write => write.path === "/users/u-viewer").body, { role: "operator", actions: ["audit_view"] })

  await button("＋ 创建账号").click()
  await page.locator(".create-editor input").nth(0).fill("new@local", { force: true })
  await page.locator(".create-editor input").nth(1).fill("123456789012", { force: true })
  await page.locator(".create-editor uni-button").filter({ hasText: "创建账号" }).click()
  await page.getByText("账号已创建", { exact: true }).waitFor()
  assert.equal(writes.find(write => write.path === "/users").body.parent, "root-project")

  for (const width of [760, 375]) {
    await page.setViewportSize({ width, height: 900 })
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${width}px accounts/matrix has horizontal page overflow`)
  }
  await button("项目包与版本").click()
  await button("重绑通道与设备").click()
  await page.locator(".rebind-panel").waitFor()
  for (const width of [760, 375]) {
    await page.setViewportSize({ width, height: 900 })
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${width}px package/import/rebind has horizontal page overflow`)
  }
  await page.setViewportSize({ width: 1280, height: 800 })

  await button("审计").click()
  await page.locator(".audit-list").getByText("项目发布", { exact: false }).waitFor()
  await page.locator(".audit-list").getByText(/pv-old/).waitFor()
  await button("行业包").click()
  await page.getByRole("heading", { name: "EMS 行业包", exact: true }).waitFor()
  await button("启用").click()
  await page.getByText(/EMS 行业包 已启用/).waitFor()
  await button("停用（不删除数据）").click()
  await button("确认停用（不删除数据）").click()
  await page.getByText(/EMS 行业包 已停用/).waitFor()
  const emsSwitch = page.getByRole("switch", { name: "EMS 行业包" })
  await emsSwitch.click()
  await page.waitForFunction(() => document.querySelector(".ems-toggle")?.getAttribute("aria-checked") === "true")
  await emsSwitch.click()
  await page.waitForFunction(() => document.querySelector(".ems-toggle")?.getAttribute("aria-checked") === "false")

  for (const width of [1280, 1024]) {
    await page.setViewportSize({ width, height: width === 1280 ? 800 : 768 })
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${width}px page has no horizontal overflow`)
  }
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ ok: true, preflightRuns, writes: writes.map(write => write.path), viewports: [1280, 1024, 760, 375], errors }))
} finally {
  await browser.close()
}
