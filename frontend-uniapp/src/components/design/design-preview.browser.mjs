import assert from "node:assert/strict"
import { createRequire } from "node:module"

const require = createRequire(new URL("../../../package.json", import.meta.url))
const { chromium } = require("playwright-core")
const base = process.env.DESIGN_PREVIEW_URL || "http://127.0.0.1:5174/uniapp/pages/design/index"
let browser
try {
  try { browser = await chromium.launch({ channel: "chrome", headless: true }) }
  catch { browser = await chromium.launch({ channel: "msedge", headless: true }) }
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const apiRequests = []
  await context.route('**/api/**', route => { apiRequests.push(route.request().url()); return route.abort() })
  const page = await context.newPage()
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  const previewURL = new URL(base)
  previewURL.searchParams.set('preview', '1')
  await page.goto(previewURL.href, { waitUntil: "domcontentloaded" })
  await page.locator(".stepper").waitFor()
  for (const [step, expected] of [["建现场", "从哪种骨架开始"], ["搭台账", "模板库"], ["接设备", "第 3 步 · 接设备"], ["配策略", "决策表"], ["配界面", "块目录"], ["交付", "项目包里有什么"]]) {
    await page.locator(".stepper uni-button").filter({ hasText: step }).click()
    await page.waitForFunction(text => document.querySelector(".stage")?.innerText.includes(text), expected)
    assert.match(await page.locator(".stage").innerText(), new RegExp(expected))
  }
  assert.match(await page.locator(".stage").innerText(), /40 个采集点/)
  assert.equal(await page.locator(".deploy-table tbody tr").count(), 2)
  assert.match(await page.locator(".delivery-step").innerText(), /2 条在线/)
  await page.locator(".stepper uni-button").filter({ hasText: "配界面" }).click()
  await page.locator(".prototype-chart i").first().waitFor()
  assert.equal(await page.locator(".prototype-chart i").count(), 12)
  const today = page.locator(".screen-card").filter({ has: page.locator("h5", { hasText: "今日执行" }) })
  assert.match(await today.innerText(), /14:00 · 饮水线冲洗/)
  assert.match(await today.innerText(), /回水 36℃，无需预循环/)
  assert.equal(await today.locator(".alarm-mini").count(), 3)
  assert.doesNotMatch(await today.innerText(), /后端未提供/)
  await page.locator(".stepper uni-button").filter({ hasText: "接设备" }).click()
  await page.locator("uni-button").filter({ hasText: "打开绑定" }).first().click()
  await page.locator(".pipeline uni-button").filter({ hasText: "验证" }).click()
  assert.match(await page.locator(".stage").innerText(), /L1|L2/)
  assert.deepEqual(apiRequests, [])
  assert.equal(await page.evaluate(() => localStorage.getItem('fastems-token')), null)
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ ok: true, checks: ["six design steps", "package content", "prototype delivery checks", "preview today runs", "12-bar prototype trend", "profile verification"] }))
} finally {
  if (browser) await browser.close()
}
