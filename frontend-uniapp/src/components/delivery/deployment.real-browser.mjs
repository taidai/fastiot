import assert from "node:assert/strict"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

// Ticket22: the 部署记录 tab may only show backend-derived records.
// Raw API reads below are independent evidence checks; the records themselves are
// created through the visible button, never by writing state fields.
const origin = process.env.T22_ORIGIN || "http://127.0.0.1:18240"
const fixture = await (await fetch(`${origin}/__fixture`)).json()
const token = fixture.token
const read = async path => {
  const response = await fetch(`${origin}/api${path}`, { headers: { authorization: `Bearer ${token}` } })
  return JSON.parse(await response.text())
}
const records = async () => (await read(`/delivery/deployments?site=${fixture.site}`)).records

const browser = await chromium.launch({ channel: "msedge", headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const button = name => page.locator("uni-button").filter({ hasText: new RegExp(`^${name}$`) })
const errors = []
page.on("pageerror", error => errors.push(error.message))
try {
  assert.deepEqual(await records(), [], "fixture starts without deployment records")
  await page.addInitScript(value => localStorage.setItem("fastems-token", value), token)
  await page.goto(`${origin}/uniapp/?live=1#/pages/delivery/index`)
  await page.getByText("预检与门槛", { exact: true }).waitFor()
  await page.waitForFunction(site => document.querySelector(".site-select")?.value === site, fixture.site, { timeout: 30000 })
  await button("部署记录").click()
  await page.getByText(/暂无部署记录/).waitFor()
  assert.equal(await page.locator(".deployment-row").count(), 0)

  // Pre-acquisition: the server derives "applied" with explicit gaps and never grants control.
  await page.waitForFunction(() => [...document.querySelectorAll("uni-button")].some(item => item.textContent === "记录本次部署" && !item.hasAttribute("disabled")))
  await button("记录本次部署").click()
  await page.locator(".notice.warn").filter({ hasText: "配置已应用，采集尚未恢复" }).waitFor()
  await page.locator(".deployment-row").first().waitFor()
  const applied = page.locator(".deployment-row").first()
  await applied.getByText("配置已应用", { exact: true }).waitFor()
  await applied.getByText("控制未授权", { exact: true }).waitFor()
  await applied.getByText(/缺口：/).waitFor()
  const firstRecords = await records()
  assert.equal(firstRecords.length, 1)
  assert.equal(firstRecords[0].status, "applied")
  assert.equal(firstRecords[0].configuration_applied, true)
  assert.equal(firstRecords[0].acquisition_restored, false)
  assert.equal(firstRecords[0].control_authorized, false)
  assert.ok(firstRecords[0].acquisition_gaps.length > 0)
  assert.equal(firstRecords[0].source_version, fixture.source_version)
  assert.equal(firstRecords[0].source_fingerprint, fixture.source_fingerprint)

  // Rebind + verify run against the real APIs inside the fixture; not a product path.
  const rebind = await fetch(`${origin}/__rebind`, { method: "POST" })
  assert.equal(rebind.status, 204)

  await button("记录本次部署").click()
  await page.getByText(/采集已恢复/).first().waitFor()
  await page.waitForFunction(() => document.querySelectorAll(".deployment-row").length === 2)
  const running = page.locator(".deployment-row").first()
  await running.getByText("运行中（采集已恢复）", { exact: true }).waitFor()
  await running.getByText("控制未授权", { exact: true }).waitFor()
  const secondRecords = await records()
  assert.equal(secondRecords.length, 2)
  assert.equal(secondRecords[0].status, "running")
  assert.equal(secondRecords[0].acquisition_restored, true)
  assert.equal(secondRecords[0].control_authorized, false)
  assert.equal(secondRecords[0].acquisition_gaps.length, 0)
  assert.equal(secondRecords[0].acquisition_evidence.length, 1)
  assert.equal(secondRecords[0].acquisition_evidence[0].health, "healthy")
  assert.equal(secondRecords[0].acquisition_evidence[0].level, "L2")
  assert.equal(secondRecords[1].id, firstRecords[0].id, "records are append-only")

  // Refresh keeps the records and the three-way distinction.
  await page.reload()
  await page.getByText("预检与门槛", { exact: true }).waitFor()
  await button("部署记录").click()
  await page.waitForFunction(() => document.querySelectorAll(".deployment-row").length === 2)
  assert.equal(await page.locator(".deployment-row").filter({ hasText: "运行中（采集已恢复）" }).count(), 1)
  assert.equal(await page.locator(".deployment-row").filter({ hasText: "配置已应用" }).count(), 1)
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ ok: true, applied: { status: firstRecords[0].status, gaps: firstRecords[0].acquisition_gaps.length }, running: { status: secondRecords[0].status, evidence: secondRecords[0].acquisition_evidence[0].device_id, health: secondRecords[0].acquisition_evidence[0].health }, controlAuthorized: secondRecords[0].control_authorized, errors }))
} finally {
  await browser.close()
  await fetch(`${origin}/__finish`, { method: "POST" })
}
