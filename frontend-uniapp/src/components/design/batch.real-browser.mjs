// Real Go HTTP + private TCP simulator only. No mocked browser responses.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'

const base = process.env.FASTIOT_BATCH_URL || 'http://127.0.0.1:18106'
let browser, page, ready = false
try {
  let fixture
  const deadline = Date.now() + 90000
  for (;;) {
    try { fixture = await (await fetch(base + '/__fixture')).json(); if (fixture.fixture === 'ticket06-isolated-store') { ready = true; break } } catch {}
    if (Date.now() > deadline) throw new Error('isolated batch fixture not ready')
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  const api = async (path, method = 'GET', body) => {
    const response = await fetch(base + '/api' + path, { method, headers: { Authorization: `Bearer ${fixture.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
    assert.equal(response.status, 200, await response.clone().text())
    return response.json()
  }
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  page.setDefaultTimeout(12000)
  const errors = [], writes = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('response', async response => { if (response.status() >= 400) console.log('HTTP failure', response.status(), response.url(), await response.text()) })
  page.on('request', request => { if (request.url().includes('/api/') && request.method() !== 'GET') writes.push({ url: request.url(), body: request.postDataJSON() }) })
  await page.addInitScript(token => localStorage.setItem('fastems-token', token), fixture.token)
  const openBatch = async () => {
    await page.locator('.design-ledger .node').filter({ hasText: 'Access fixture' }).click()
    await page.locator('.stepper .step').filter({ hasText: '接设备' }).click()
    await page.locator('.access-step uni-button').filter({ hasText: /^批量接入$/ }).click()
    await page.locator('.batch-access').waitFor()
  }
  await page.goto(base + '/uniapp/#/pages/design/index')
  await openBatch()
  const batch = page.locator('.batch-access')
  const button = text => batch.locator('uni-button').filter({ hasText: new RegExp(`^${text}$`) })
  const header = 'device,profile,channel,station\n'
  const row = (device, station) => `${device},access-sim,${fixture.channel},${station}\n`
  const conflictCSV = header + row(fixture.device, 1) + row(fixture.third, 1)
  await batch.locator('textarea').fill(conflictCSV)
  assert.equal(await batch.locator('textarea').inputValue(), conflictCSV, 'CSV must not be truncated to uni-app default 140 characters')
  await button('CSV 预检').click()
  await batch.getByText('预检阻断', { exact: true }).first().waitFor()
  assert.equal(await batch.getByText('预检阻断', { exact: true }).count(), 2, 'every conflicting station must be blocked before writes')
  const modelPath = `/modbus/${fixture.channel}/units/1/model`
  assert.equal((await api(modelPath)).points.length, 0)
  await batch.locator('textarea').fill('x'.repeat(262145))
  await batch.getByText('CSV 超过 256 KiB，请拆分后重新粘贴；本次不会导入', { exact: true }).waitFor()
  await batch.locator('textarea').fill(header + row(fixture.device, 1).repeat(257))
  await button('CSV 预检').click()
  await batch.getByText('每批最多 256 台，请拆分 CSV', { exact: true }).waitFor()
  assert.equal((await api(modelPath)).points.length, 0, 'oversized import must not write')
  await batch.locator('textarea').fill(header + row(fixture.device, 1) + row(fixture.second, 2) + row(fixture.third, 248))
  await button('CSV 预检').click()
  await batch.locator('.batch-diff').first().waitFor()
  await batch.locator('.batch-diff summary').first().click()
  assert.match(await batch.innerText(), /水箱液位/)
  assert.match(await batch.innerText(), /100/)
  assert.equal((await api(modelPath)).points.length, 0, 'preview must not write')
  await button('批量套用可导入项').click()
  await batch.locator('.notice').filter({ hasText: '已保存 2' }).waitFor()
  assert.match(await batch.locator('.notice').filter({ hasText: '已保存 2' }).innerText(), /阻断 1/)
  assert(writes.some(write => write.url.endsWith('/access/batch/reviewed') && write.body.dry_run === false && write.body.preview_version), 'new UI must submit the reviewed version')
  const saved = name => batch.locator('.saved-bindings tbody tr').filter({ hasText: name })
  await saved('模拟仪表').waitFor()
  await saved('模拟仪表').locator('uni-button').filter({ hasText: /^L1 只读验证$/ }).click()
  await saved('模拟仪表').getByText('L1：通过', { exact: false }).waitFor()
  const pointID = (await api(modelPath)).model.point_map['水箱液位']
  const sampleDeadline = Date.now() + 12000
  while (!(await api('/ems/state')).values.some(value => value.point_id === pointID && value.quality === 'good')) {
    if (Date.now() > sampleDeadline) throw new Error('no first real sample')
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  await saved('模拟仪表').locator('uni-button').filter({ hasText: /^L2 必需点验证$/ }).click()
  await saved('模拟仪表').getByText('L2：通过', { exact: false }).waitFor()
  await saved('缺席从站').locator('uni-button').filter({ hasText: /^L1 只读验证$/ }).click()
  await saved('缺席从站').getByText('L1：未通过', { exact: false }).waitFor()
  await saved('缺席从站').locator('uni-button').filter({ hasText: /^L2 必需点验证$/ }).click()
  await saved('缺席从站').getByText('L2：未通过', { exact: false }).waitFor()
  const before = await api(modelPath)
  await page.reload(); await openBatch()
  await saved('模拟仪表').getByText('L2：通过', { exact: false }).waitFor()
  await saved('缺席从站').getByText('L2：未通过', { exact: false }).waitFor()
  assert.deepEqual((await api(modelPath)).model.point_map, before.model.point_map)
  // Retry the same input through a fresh preview: no duplicate creation.
  await batch.locator('textarea').fill(header + row(fixture.device, 1) + row(fixture.second, 2))
  await button('CSV 预检').click()
  await batch.getByText('预检阻断', { exact: true }).first().waitFor()
  assert.equal((await api(modelPath)).points.length, before.points.length)
  const model = (await api(modelPath)).model
  delete model.point_map['水箱液位']
  await api(modelPath, 'PUT', model)
  await button('刷新已保存结果').click()
  await saved('模拟仪表').getByText('已失效', { exact: false }).first().waitFor()
  await saved('模拟仪表').locator('uni-button').filter({ hasText: /^L2 必需点验证$/ }).click()
  await saved('模拟仪表').getByText('必需点未映射', { exact: false }).waitFor()
  await fetch(base + '/__offline', { method: 'POST' })
  await saved('模拟仪表').locator('uni-button').filter({ hasText: /^L1 只读验证$/ }).click()
  await saved('模拟仪表').getByText('L1：未通过', { exact: false }).waitFor()
  for (const width of [1440, 1280, 1024, 375]) {
    await page.setViewportSize({ width, height: 900 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `overflow at ${width}`)
  }
  assert.deepEqual(errors, [])
  assert.equal(writes.some(write => /\/commands|\/value$/.test(write.url)), false)
  console.log(JSON.stringify({ ok: true, realGo: true, reviewedBatch: true, conflict: true, mixedResults: true, perDeviceL1L2: true, reload: true, retry: true, requiredPoint: true, offline: true, errors }))
} catch (error) {
  console.log('page on failure', await page?.locator('body').innerText())
  throw error
} finally {
  await browser?.close()
  if (ready) await fetch(base + '/__finish', { method: 'POST' }).catch(() => {})
}
