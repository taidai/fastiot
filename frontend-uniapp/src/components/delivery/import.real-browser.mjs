import assert from "node:assert/strict"
import { chromium } from "../../../../frontend/node_modules/playwright-core/index.mjs"

const origin = "http://127.0.0.1:18220"
async function getJSON(path) {
  let lastError
  for (let attempt = 0; attempt < 20; attempt++) {
    try {
      const response = await fetch(`${origin}${path}`)
      if (!response.ok) throw new Error(`${path}: HTTP ${response.status}`)
      return await response.json()
    } catch (error) {
      lastError = error
      await new Promise(resolve => setTimeout(resolve, 250))
    }
  }
  throw lastError
}
const fixture = await getJSON("/__fixture")
const sourcePackage = await getJSON("/__source-package")
const missingPackage = await getJSON("/__missing-package")
const tamperedPackage = await getJSON("/__tampered-package")
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || "msedge", headless: true })
const button = name => page.locator("uni-button").filter({ hasText: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on("pageerror", error => errors.push(error.message))

async function upload(pkg, name) {
  const chooserEvent = page.waitForEvent("filechooser")
  await button("导入到新实例").click()
  const chooser = await chooserEvent
  await chooser.setFiles({ name, mimeType: "application/json", buffer: Buffer.from(JSON.stringify(pkg)) })
}
async function nodeCount() {
  return (await getJSON("/__fixture")).node_count
}

try {
  await page.goto(`${origin}/uniapp/?live=1#/pages/login/index`)
  await page.locator("input").nth(0).fill(fixture.username)
  await page.locator("input").nth(1).fill(fixture.password)
  await button("登录").click()
  await page.waitForURL(/pages\/overview\/index/)
  await page.goto(`${origin}/uniapp/?live=1#/pages/delivery/index`)
  await page.getByText("跨实例交付", { exact: true }).waitFor()
  await page.waitForFunction(() => [...document.querySelectorAll("uni-button")].some(item => item.textContent === "导入到新实例" && !item.hasAttribute("disabled")))

  await upload(missingPackage, "missing-dependency.json")
  await page.getByText("缺少 Profile 依赖 browser-missing-profile@private-test-version；已拒绝导入，未创建任何节点", { exact: true }).waitFor()
  assert.equal(await nodeCount(), fixture.initial_node_count, "dependency refusal leaves no runnable residue")
  assert.equal(await button("重绑通道与设备").count(), 0)

  await upload(tamperedPackage, "tampered.json")
  await page.getByText("项目包完整性校验失败", { exact: true }).waitFor()
  assert.equal(await nodeCount(), fixture.initial_node_count, "tamper refusal leaves no runnable residue")
  assert.equal(await button("重绑通道与设备").count(), 0)

  await upload(sourcePackage, "external-published.json")
  await page.getByText(/已导入「L3 site」/).waitFor()
  const importCard = page.locator(".import-card")
  await importCard.getByText("导入证据（已保存）", { exact: true }).waitFor()
  await importCard.getByText(fixture.source_version, { exact: true }).waitFor()
  await importCard.getByText(fixture.source_fingerprint, { exact: true }).waitFor()
  assert.equal(await importCard.locator(".chip").filter({ hasText: /部署成功|控制已授权|运行中/ }).count(), 0)

  await button("重绑通道与设备").click()
  const rebind = page.locator(".rebind-panel")
  await rebind.getByText("地址未绑定", { exact: false }).waitFor()
  await button("编辑现场连接").click()
  await rebind.locator('input[placeholder="127.0.0.1:1502"]').fill("127.0.0.1:18118")
  await button("保存现场连接").click()
  await rebind.getByText("现场连接已保存", { exact: true }).waitFor()

  await button("验证模拟采集").click()
  await importCard.getByText("采集验证通过；证据已保存", { exact: true }).waitFor()
  await importCard.getByText(/L2 · 通过/).waitFor()
  await importCard.getByText(/\/1$/).waitFor()

  await page.reload()
  await page.waitForTimeout(500)
  await page.goto(`${origin}/uniapp/?live=1#/pages/delivery/index`)
  await importCard.getByText("导入证据（已保存）", { exact: true }).waitFor()
  await importCard.getByText(fixture.source_fingerprint, { exact: true }).waitFor()
  await importCard.getByText("采集验证通过；证据已保存", { exact: true }).waitFor()
  assert.equal(await importCard.locator(".chip").filter({ hasText: /部署成功|控制已授权|运行中/ }).count(), 0)
  assert.equal(await importCard.getByText(/原型演练/).count(), 0)

  const overflows = []
  for (const width of [1280, 1024, 375]) {
    await page.setViewportSize({ width, height: width === 1280 ? 800 : width === 1024 ? 768 : 812 })
    overflows.push(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth))
  }
  assert.deepEqual(overflows, [false, false, false])
  if (process.env.T21_SHOT) await page.screenshot({ path: process.env.T21_SHOT, fullPage: true })
  assert.deepEqual(errors, [])
  const finalFixture = await getJSON("/__fixture")
  assert.equal(finalFixture.physical_writes, 0)
  assert.equal(finalFixture.simulator_writes, 0)
  console.log(JSON.stringify({ ok: true, sourceVersion: fixture.source_version, sourceFingerprint: fixture.source_fingerprint, negativeNodeCount: fixture.initial_node_count, overflows, physicalWrites: 0, simulatorWrites: 0, errors }))
} finally {
  await browser.close()
  await fetch(`${origin}/__finish`, { method: "POST" })
}
