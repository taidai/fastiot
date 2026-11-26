import assert from "node:assert/strict"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

// Ticket23: drive rollback only through the visible tab and rollback controls.
// Raw API calls below are read-only evidence checks; they never change configuration.
const origin = process.env.T23_ORIGIN || "http://127.0.0.1:18230"
const fixture = await (await fetch(`${origin}/__fixture`)).json()
const token = fixture.token
const read = async path => {
  const response = await fetch(`${origin}/api${path}`, { headers: { authorization: `Bearer ${token}` } })
  return { status: response.status, body: await response.text() }
}
const siteDescription = async () => {
  const snapshot = JSON.parse((await read("/snapshot")).body)
  return snapshot.nodes.find(node => node.id === fixture.site)?.points.find(point => point.type === "description")?.text || ""
}
const evidence = async () => {
  const json = async path => JSON.parse((await read(path)).body)
  const history = await json(`/history?node_id=${fixture.variable}&type=value`)
  const command = await json(`/modbus/${fixture.bus}/units/1/commands/${fixture.command_request}`)
  const alarm = await read(`/alarm-events?occurrence_id=${fixture.alarm}`)
  const notifications = await json("/notifications")
  const audit = await json("/audit")
  const notice = (notifications.items || []).find(item => item.id === fixture.notification)
  return {
    history: history.total_matched,
    command: command.state,
    alarmEvents: alarm.status === 200 ? JSON.parse(alarm.body).events.length : 0,
    notice: notice ? { present: true, read: !!notice.read_at } : { present: false },
    rollbacks: (audit.records || []).filter(record => record.action === "项目回退").map(record => record.result),
  }
}

const before = { description: await siteDescription(), ...(await evidence()) }
assert.equal(before.description, "Current before rollback")
assert.equal(before.command, "rejected")
assert.ok(before.history > 0 && before.alarmEvents > 0 && before.notice.present && before.notice.read, JSON.stringify(before))

const browser = await chromium.launch({ channel: "msedge", headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const button = name => page.locator("uni-button").filter({ hasText: new RegExp(`^${name}$`) })
const row = id => page.locator(".version-row").filter({ hasText: id })
const rollbackTo = async id => {
  await row(id).getByText("回退", { exact: true }).click()
  await row(id).getByText("确认回退（历史保留）", { exact: true }).click()
}
const errors = []
page.on("pageerror", error => errors.push(error.message))
try {
  await page.addInitScript(value => localStorage.setItem("fastems-token", value), token)
  await page.goto(`${origin}/uniapp/?live=1#/pages/delivery/index`)
  await page.getByText("预检与门槛", { exact: true }).waitFor()
  await page.waitForFunction(site => document.querySelector(".site-select")?.value === site, fixture.site, { timeout: 30000 })
  await button("版本回退").click()
  await row(fixture.good_version).waitFor()
  await row(fixture.bad_version).waitFor()

  // Unsupported resource: the version carries a calculation point. The blocker is
  // traceable, names the resource and confirms that nothing was written.
  await rollbackTo(fixture.bad_version)
  await page.locator(".notice.error").filter({ hasText: "后端不支持回退" }).waitFor()
  await page.locator(".notice.error").filter({ hasText: "目标版本 calculationPoint" }).waitFor()
  await page.locator(".notice.error").filter({ hasText: "未写入配置，可修复阻断后重试" }).waitFor()

  // Supported version: configuration returns, runtime facts stay.
  await rollbackTo(fixture.good_version)
  const success = page.locator(".notice.success").filter({ hasText: `已回退到 ${fixture.good_version}` })
  await success.waitFor()
  const message = await success.innerText()
  assert.match(message, /回退前基线 pv-/)
  assert.match(message, /历史未回退，旧命令未回放/)

  const after = { description: await siteDescription(), ...(await evidence()) }
  assert.notEqual(after.description, "Current before rollback")
  assert.equal(after.description, "Operation site")
  assert.equal(after.command, "rejected")
  assert.ok(after.history >= before.history, `${after.history} < ${before.history}`)
  assert.ok(after.alarmEvents >= before.alarmEvents)
  assert.deepEqual(after.notice, before.notice)
  // The unsupported attempt is refused before any write, so only the performed
  // rollback appears in the audit trail.
  assert.deepEqual(after.rollbacks, ["success"])

  // Refresh keeps the result and the list now offers the saved pre-rollback baseline.
  await page.reload()
  await button("版本回退").click()
  await row(fixture.good_version).waitFor()
  const baseline = message.match(/回退前基线 (pv-\w+)/)[1]
  await row(baseline).waitFor()
  assert.deepEqual(errors, [])
  const final = await (await fetch(`${origin}/__fixture`)).json()
  assert.equal(final.physical_writes, 0)
  console.log(JSON.stringify({ ok: true, description: [before.description, after.description], history: [before.history, after.history], command: after.command, alarmEvents: [before.alarmEvents, after.alarmEvents], notice: after.notice, rollbacks: after.rollbacks, baseline, physicalWrites: final.physical_writes, errors }))
} finally {
  await browser.close()
  await fetch(`${origin}/__finish`, { method: "POST" })
}
