import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

const token = process.env.FASTEMS_TOKEN || ""
if (!token) throw new Error("FASTEMS_TOKEN is required")
const base = "http://127.0.0.1:5174/uniapp/"
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || "msedge", headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, acceptDownloads: true })
  const button = name => page.locator("uni-button").filter({ hasText: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  await page.addInitScript(value => localStorage.setItem("fastems-token", value), token)
  await page.goto(base)
  await page.waitForTimeout(700)
  await page.evaluate(() => { uni.redirectTo({ url: "/pages/delivery/index" }) })
  await page.getByText("预检与门槛", { exact: true }).waitFor()
  const site = await page.locator(".site-select").inputValue()
  assert.equal(await page.locator(".gate").count(), 0, "preflight details start collapsed")
  await button("展开门槛").click()
  await page.locator(".gate").first().waitFor()
  if (!site) {
    assert.equal(await button("导出项目包").getAttribute("disabled"), "true", "project root must not be used as a Site")
    await page.locator(".versions-card").getByText(/项目根不能代替 Site/).waitFor()
  }
  const gateCount = await page.locator(".gate").count()
  assert(gateCount >= 10, "real backend gate details are visible")

  const preflightResponse = page.waitForResponse(response => new URL(response.url()).pathname === "/api/delivery/preflight" && response.request().method() === "POST")
  await button("运行预检").click()
  assert.equal((await preflightResponse).status(), 200)
  await page.getByText(/最近预检：(通过|注意|失败)/).waitFor()
  await page.waitForFunction(() => [...document.querySelectorAll("uni-button")].some(item => item.textContent === "导出验收证据" && !item.hasAttribute("disabled")))

  let projectPackage = null
  if (site) {
    const packageEvent = page.waitForEvent("download")
    await button("导出项目包").click()
    const packageDownload = await packageEvent
    projectPackage = JSON.parse(await readFile(await packageDownload.path(), "utf8"))
    assert.equal(projectPackage.format, "fastiot.project-package")
    assert(projectPackage.nodes.length > 0)
    assert(Number.isInteger(projectPackage.point_count))
    assert(Number.isInteger(projectPackage.strategy_count))
  }

  const evidenceEvent = page.waitForEvent("download")
  await button("导出验收证据").click()
  const evidenceDownload = await evidenceEvent
  const evidence = await readFile(await evidenceDownload.path())
  assert.equal(evidence[0], 0x50)
  assert.equal(evidence[1], 0x4b)
  assert(evidence.length > 100)

  await button("部署记录").click()
  await page.getByText(/暂无部署记录/).waitFor()
  await button("版本回退").click()
  await page.getByText(/版本或当前工程包含计算点/).waitFor()
  await button("账号与权限").click()
  await page.locator(".account-table tbody tr").first().waitFor()
  await page.locator(".permission-matrix tbody tr").first().waitFor()
  const accounts = await page.locator(".account-table tbody tr").count()
  await button("审计").click()
  await page.locator(".audit-list .audit-row").first().waitFor()
  await button("行业包").click()
  await page.locator(".industry-card").first().waitFor()
  const industry = await page.locator(".industry-card").innerText()

  await button("项目包与版本").click()
  await button("展开门槛").click()
  await page.locator(".gate").first().waitFor()
  await page.reload()
  await page.waitForTimeout(700)
  await page.evaluate(() => { uni.redirectTo({ url: "/pages/delivery/index" }) })
  await page.getByText("预检与门槛", { exact: true }).waitFor()
  const overflows = []
  for (const width of [1280, 1024, 760, 375]) {
    await page.setViewportSize({ width, height: width === 1280 ? 800 : width === 1024 ? 768 : 900 })
    overflows.push(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth))
    if (process.env.T5_SHOT_DIR) await page.screenshot({ path: `${process.env.T5_SHOT_DIR}/uniapp-visual-delivery-${width}.png`, fullPage: true })
  }
  assert.deepEqual(overflows, [false, false, false, false])
  if (process.env.T5_SHOT) await page.screenshot({ path: process.env.T5_SHOT, fullPage: true })
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ ok: true, site: site || null, gateCount, packageNodes: projectPackage?.nodes.length || null, dependencies: projectPackage?.dependencies.length || null, evidenceBytes: evidence.length, accounts, industry: industry.split("\n").slice(0, 4), overflows, errors }))
} finally {
  await browser.close()
}
