import assert from "node:assert/strict"
import { createRequire } from "node:module"

const require = createRequire(new URL("../../../../frontend/package.json", import.meta.url))
const { chromium } = require("playwright-core")
const base = process.env.LOGIN_BASE_URL || "http://127.0.0.1:4409/uniapp/index.html#/pages/login/index"

let browser
try {
  try { browser = await chromium.launch({ channel: "chrome", headless: true }) }
  catch { browser = await chromium.launch({ channel: "msedge", headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  const errors = []
  page.on("pageerror", error => errors.push(error.message))
  await page.goto(base, { waitUntil: "domcontentloaded" })
  await page.locator(".login uni-input").first().waitFor()
  const heights = await page.locator(".login uni-input input").evaluateAll(elements => elements.map(element => element.getBoundingClientRect().height))
  assert.ok(heights.every(height => height >= 20), `native login inputs must be visible and keyboard accessible, got ${heights.join(", ")}px`)
  await page.locator(".login uni-input").nth(0).click()
  await page.keyboard.type("account-input-check")
  assert.equal(await page.locator(".login input[type='text']").inputValue(), "account-input-check")
  await page.locator(".login uni-input").nth(1).click()
  await page.keyboard.type("password-input-check")
  assert.equal(await page.locator(".login input[type='password']").inputValue(), "password-input-check")
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ ok: true, heights, typed: ["account-input-check", "password-input-check"] }))
} finally {
  if (browser) await browser.close()
}
