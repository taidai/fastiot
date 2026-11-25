import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { DELIVERY_PREVIEW_DEPLOYMENTS, resolveDeliveryPreview, setDeliveryPreviewScenarios, selectDeliveryPreviewProject, deliveryPreviewState } from "../../api/preview/delivery.ts"

const data = (path, method = "GET", body) => {
  const report = resolveDeliveryPreview(path, method, body)
  assert.equal(report.handled, true, `${method} ${path}`)
  assert.equal(report.status || 200, 200, `${method} ${path}`)
  return report.data
}

test('delivery presentation state is isolated per project and restored on return', () => {
  deliveryPreviewState.publishNote = '泵房发布说明'
  selectDeliveryPreviewProject('site-energy')
  assert.equal(deliveryPreviewState.publishNote, '')
  assert.equal(deliveryPreviewState.importStep, 'idle')
  deliveryPreviewState.publishNote = '储能发布说明'
  selectDeliveryPreviewProject('site-pump')
  assert.equal(deliveryPreviewState.publishNote, '泵房发布说明')
  deliveryPreviewState.publishNote = ''
})

test("historical versions do not bypass the first-publish import prerequisite", () => {
  const result = resolveDeliveryPreview("/projects/import", "POST", { package: data("/projects/export") })
  assert.equal(result.status, 409)
  assert.equal(result.data.error, "publish_required")
})

test("prototype failure scenarios block publishing and importing until reset", () => {
  setDeliveryPreviewScenarios({ preflightFail: true, dependencyMismatch: true })
  try {
    assert.equal(data("/delivery/gates?site=site-pump").status, "fail")
    const publish = resolveDeliveryPreview("/projects/publish", "POST", { site: "site-pump", confirm: false })
    assert.equal(publish.status, 409)
    assert.equal(publish.data.error, "preflight_failed")
    const imported = resolveDeliveryPreview("/projects/import", "POST", { parent: "root-project", package: data("/projects/export?site=site-pump") })
    assert.equal(imported.status, 400)
    assert.match(imported.data.error, /^incompatible_dependency:/)
  } finally { setDeliveryPreviewScenarios({ preflightFail: false, dependencyMismatch: false }) }
  assert.equal(data("/delivery/gates?site=site-pump").status, "pass")
})

test("delivery preview handles only known API routes", () => {
  assert.deepEqual(resolveDeliveryPreview("/not-an-api", "GET"), { handled: false })
  assert.deepEqual(resolveDeliveryPreview("/projects/publish", "GET"), { handled: false })
})

test("snapshot, versions, package and preflight contain the final prototype delivery state", () => {
  const snapshot = data("/snapshot")
  assert.equal(snapshot.user.policy.admin, true)
  assert.deepEqual(snapshot.roots, ["root-project"])
  assert.ok(snapshot.nodes.some(node => node.id === "site-pump" && node.type === "site"))
  assert.ok(snapshot.nodes.some(node => node.id === "site-energy" && node.type === "site"))

  const versions = data("/projects/versions?site=site-pump").versions
  assert.deepEqual(versions.map(item => item.id), ["v2026.11.21-1", "v2026.11.19-1", "v2026.11.17-1"])
  assert.equal(versions[0].operator, "张工")
  assert.equal(versions[0].preflight_status, "pass")

  const pkg = data("/projects/export?site=site-pump")
  assert.equal(pkg.format, "fastiot.project-package")
  assert.equal(pkg.format_version, 2)
  assert.equal(pkg.point_count, 36)
  assert.ok(pkg.dependencies.some(item => item.kind === "industry" && item.id === "ems" && item.version === "1.0.0"))
  assert.ok(pkg.stripped.some(item => item.reason === "凭据"))

  const gates = data("/delivery/gates?site=site-pump")
  assert.equal(gates.status, "pass")
  assert.ok(gates.gates.some(gate => gate.key === "dependencies"))
  assert.equal(data("/delivery/preflight?site=site-pump").latest.status, "pass")
})

test("industry, accounts and audit expose realistic prototype records", () => {
  const industry = data("/industry/packages").packages[0]
  assert.equal(industry.id, "ems")
  assert.equal(industry.version, "1.0.0")
  assert.equal(industry.installed, true)
  assert.ok(industry.provides.includes("功率分配、仲裁与联合调度"))

  const users = data("/users")
  assert.deepEqual(users.users.map(user => user.email), ["张工", "李工", "王管理员"])
  assert.ok(users.actions.includes("account_manage"))
  const audit = data("/audit?limit=200")
  assert.ok(audit.records.some(record => record.object === "v2026.11.19-1"))
  assert.ok(audit.actions.includes("项目发布"))

  assert.equal(data("/industry/packages", "POST", { id: "ems", version: "1.0.0" }).installed, true)
  assert.equal(data("/industry/packages/ems", "DELETE").installed, false)
  assert.equal(data("/industry/packages").packages[0].installed, false)
  assert.equal(data("/industry/packages", "POST", { id: "ems", version: "1.0.0" }).installed, true)
})

test("publish, preflight and rollback update the same preview version history", () => {
  const published = data("/projects/publish", "POST", { site: "site-pump", note: "交付验收基线", confirm: false })
  assert.equal(published.note, "交付验收基线")
  assert.equal(data("/projects/versions?site=site-pump").versions[0].id, published.id)

  const report = data("/delivery/preflight?site=site-pump", "POST")
  assert.equal(report.site, "site-pump")
  assert.equal(data("/delivery/preflight?site=site-pump").latest.id, report.id)

  const rollbackResult = data("/projects/rollback", "POST", { site: "site-pump", version: "v2026.11.19-1" })
  assert.equal(rollbackResult.version, "v2026.11.19-1")
  assert.ok(rollbackResult.updated > 0)
  assert.equal(data("/projects/versions?site=site-pump").versions[0].id, rollbackResult.version)
})

test("import rejects dependency mismatches and supports scoped rebind and runtime samples", () => {
  const exported = data("/projects/export?site=site-pump")
  const mismatch = resolveDeliveryPreview("/projects/import", "POST", {
    parent: "root-project",
    package: { ...exported, dependencies: [{ kind: "industry", id: "ems", version: "0.9.0" }] },
  })
  assert.equal(mismatch.handled, true)
  assert.equal(mismatch.status, 400)
  assert.match(mismatch.data.error, /^incompatible_dependency:industry:ems/)

  const imported = data("/projects/import", "POST", { parent: "root-project", package: { ...exported, name: "教学泵房（新实例）" } })
  assert.equal(imported.name, "教学泵房（新实例）")
  assert.equal(imported.channels, 1)
  assert.ok(imported.node_ids.includes("channel-imported"))
  assert.equal(imported.version_id, "v2026.11.21-import-1")
  assert.ok(data("/snapshot").nodes.some(node => node.id === imported.site_id && node.type === "site"))
  assert.equal(data("/ems/channels").channels.find(channel => channel.id === "channel-imported").address, "")

  data("/modbus/channel-imported", "PUT", { description: "导入总线", protocol: "TCP", uri: "127.0.0.1:2502" })
  assert.equal(data("/ems/channels").channels.find(channel => channel.id === "channel-imported").address, "127.0.0.1:2502")
  assert.equal(data("/ems/state").values.some(value => value.point_id === "point-imported"), false, "rebind must not start collection")
  assert.equal(data("/delivery/preview/run", "POST").step, "running")
  assert.equal(data("/ems/state").values.find(value => value.point_id === "point-imported").quality, "good")
  assert.equal(data("/ems/quality").channels.find(channel => channel.channel_id === "channel-imported").health, "healthy")
})

test("account edits and audit filters remain stateful", () => {
  const created = data("/users", "POST", { parent: "root-project", email: "验收员", password: "123456789012", role: "viewer", actions: [] })
  assert.ok(created.id)
  data(`/users/${created.id}`, "PUT", { role: "operator", actions: ["audit_view"] })
  const user = data("/users").users.find(item => item.id === created.id)
  assert.equal(user.policy.role, "operator")
  assert.deepEqual(user.policy.actions, ["audit_view"])
  const filtered = data("/audit?limit=200&action=项目发布&result=success")
  assert.ok(filtered.records.length > 0)
  assert.ok(filtered.records.every(record => record.action === "项目发布" && record.result === "success"))
})

test("evidence JSON has prototype structure and reflects current preview state", () => {
  setDeliveryPreviewScenarios({ preflightFail: true })
  const evidence = data("/delivery/evidence?site=site-pump")
  assert.deepEqual(Object.keys(evidence), ["generatedAt", "project", "version", "preflight", "package", "importStep", "mappingDiff", "audit", "permissions"])
  assert.equal(evidence.preflight.status, "failed")
  assert.equal(evidence.mappingDiff[0].mapped, false)
  assert.equal(evidence.importStep, "running")
  assert.equal(evidence.version, data("/projects/versions?site=site-pump").versions[0].id)
  assert.ok(evidence.audit.some(item => item.action.includes("项目导入")))
  assert.deepEqual(evidence.permissions.operator, { start: "允许（仅泵房）", editPoints: "拒绝", publish: "拒绝" })
  data("/users/u-li", "PUT", { role: "viewer", actions: [] })
  assert.equal(data("/delivery/evidence").permissions.operator.start, "拒绝")
  data("/users/u-li", "PUT", { role: "operator", actions: [] })
  setDeliveryPreviewScenarios({ preflightFail: false })
  assert.equal(data("/delivery/evidence").preflight.status, "passed")
})

test("deployment rows and evidence download are preview-only while the live API gap remains", () => {
  assert.deepEqual(DELIVERY_PREVIEW_DEPLOYMENTS.map(record => [record.target, record.version, record.status]), [
    ["教学泵房（本站）", "v2026.11.19-1", "运行中"],
    ["储能站 A", "—", "待部署"],
  ])
  const page = readFileSync(new URL("../../pages/delivery/index.vue", import.meta.url), "utf8")
  assert.match(page, /PREVIEW_MODE/)
  assert.match(page, /v-if="PREVIEW_MODE"[^>]*v-for="record in DELIVERY_PREVIEW_DEPLOYMENTS"/)
  assert.match(page, /v-else[^>]*>\s*<td colspan="5"[^>]*>暂无部署记录：后端没有独立的/)
  assert.match(page, /if \(PREVIEW_MODE\)[\s\S]*fastiot-acceptance-evidence\.json/)
  assert.match(page, /request<ArrayBuffer>\([\s\S]*'arraybuffer'\)/)
  assert.match(page, /application\/zip/)
  assert.doesNotMatch(page, /iframe|交付 · 玻璃磨砂\.html/)
})
