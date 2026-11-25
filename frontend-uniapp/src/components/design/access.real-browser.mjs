// Run TestSingleDeviceBrowserBackend against private volumes and TCP simulator first.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'

const base = process.env.FASTIOT_ACCESS_URL || 'http://127.0.0.1:18085'
let browser, ready = false
try {
  let fixture
  const deadline = Date.now() + 90000
  for (;;) {
    try { fixture = await (await fetch(base + '/__fixture')).json(); if (fixture.fixture === 'ticket05-isolated-store') { ready = true; break } } catch {}
    if (Date.now() > deadline) throw new Error('isolated Go access fixture not ready')
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  page.setDefaultTimeout(12000)
  const errors = [], writes = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('response', async response => {
    if (response.url().endsWith('/verify') && response.request().method() === 'POST') console.log('verification', response.status(), await response.text())
  })
  page.on('request', request => { if (request.url().includes('/api/') && request.method() !== 'GET') writes.push(request.url()) })
  await page.addInitScript(token => localStorage.setItem('fastems-token', token), fixture.token)
  await page.goto(base + '/uniapp/#/pages/design/index')
  await page.locator('.design-ledger .node').filter({ hasText: '模拟仪表' }).click()
  await page.locator('.stepper .step').filter({ hasText: '接设备' }).click()
  const access = page.locator('.access-step')
  const button = text => access.locator('uni-button').filter({ hasText: new RegExp(`^${text}$`) })
  await access.locator('.pipeline').waitFor()
  assert.equal(await access.locator('.batch-access').count(), 0, 'single device must not open bulk access')
  await access.locator('.pick').filter({ hasText: '只读模拟仪表' }).click()
  await button('下一步：选通道 →').click()
  await access.locator('.pick').filter({ hasText: '私有模拟器' }).click()
  await access.locator('.pipeline uni-button').filter({ hasText: '选型号' }).click()
  await button('预览 Profile 匹配').click()
  await access.locator('.preview-panel').waitFor()
  assert.match(await access.locator('.preview-panel').innerText(), /水箱液位/)
  // Read-only preview must leave the real model and acquisition nodes untouched.
  const api = async (path, method = 'GET', body) => {
    const response = await fetch(base + '/api' + path, { method, headers: { Authorization: `Bearer ${fixture.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
    assert.equal(response.status, 200, await response.clone().text())
    return response.json()
  }
  const modelPath = `/modbus/${fixture.channel}/units/1/model`
  let model = await api(modelPath)
  assert.equal(model.model, undefined)
  assert.equal(model.points.length, 0)
  await button('应用 Profile').click()
  await access.locator('.notice').filter({ hasText: 'Profile 已应用' }).waitFor()
  assert.equal(await access.locator('.pipeline uni-button').filter({ hasText: /^验证$/ }).evaluate(node => node.classList.contains('ok')), false, 'mapping is not verification')
  await access.locator('.pipeline uni-button').filter({ hasText: '核对点位' }).click()
  const table = access.locator('.mini-table')
  assert.match(await table.innerText(), /100/)
  assert.match(await table.innerText(), /uint16/)
  assert.match(await table.innerText(), /0.1/)
  assert.match(await table.innerText(), /必需/)
  assert.match(await table.innerText(), /可选/)
  assert.match(await table.innerText(), /就地/)
  await button('下一步：验证 →').click()
  await button('L1 连通验证（只读）').click()
  await access.locator('.line-result').filter({ hasText: 'L1：通过' }).waitFor()
  // Wait on the real observation shown by the UI, not an arbitrary delay.
  await access.locator('.quality-panel tbody tr').filter({ has: page.locator('td:first-child', { hasText: '水箱液位' }) }).locator('.chip').filter({ hasText: '有效' }).waitFor()
  await button('L2 必需点验证（只读）').click()
  await access.locator('.line-result').filter({ hasText: 'L2：通过' }).waitFor()
  model = await api(modelPath)
  assert.equal(model.model.business_node_id, fixture.device)
  assert.equal(model.model.verified, false, 'automated evidence is not manual field acceptance')
  await page.reload()
  await page.locator('.design-ledger .node').filter({ hasText: '模拟仪表' }).click()
  await page.locator('.stepper .step').filter({ hasText: '接设备' }).click()
  await access.locator('.pipeline uni-button').filter({ hasText: /^验证$/ }).click()
  await access.getByText('自动验证历史', { exact: false }).waitFor()
  assert.match(await access.innerText(), /L2/)
  assert.equal(await access.locator('.pipeline uni-button').filter({ hasText: /^验证$/ }).evaluate(node => node.classList.contains('ok')), true, 'reload restores current L2 evidence')
  await access.locator('.pipeline uni-button').filter({ hasText: '核对点位' }).click()
  await access.locator('tbody tr').filter({ has: page.locator('td:first-child', { hasText: '水箱液位' }) }).locator('select').selectOption('')
  await button('保存点位映射').click()
  await access.locator('.notice').filter({ hasText: '点位映射已保存' }).waitFor()
  await button('下一步：验证 →').click()
  await access.getByText('已失效', { exact: false }).first().waitFor()
  await button('L2 必需点验证（只读）').click()
  await access.locator('.line-result').filter({ hasText: '必需点未映射' }).waitFor()
  await fetch(base + '/__offline', { method: 'POST' })
  await button('L1 连通验证（只读）').click()
  await access.locator('.line-result').filter({ hasText: 'L1：未通过' }).waitFor()
  for (const width of [1440, 1280, 1024, 375]) {
    await page.setViewportSize({ width, height: 900 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `overflow at ${width}`)
  }
  assert.deepEqual(errors, [])
  assert.equal(writes.some(url => /\/commands|\/value$/.test(url)), false)
  console.log(JSON.stringify({ ok: true, realGo: true, singleDevice: true, previewApply: true, profileFields: true, persisted: true, L1L2: true, missingRequired: true, invalidatedEvidence: true, offline: true, errors }))
} finally {
  await browser?.close()
  if (ready) await fetch(base + '/__finish', { method: 'POST' }).catch(() => {})
}
