// Real Go / existing Store / private TCP fixture; no application-response mocks.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const base = 'http://127.0.0.1:18088'
let browser, ready = false
try {
  let fixture
  const deadline = Date.now() + 90000
  for (;;) {
    try { fixture = await (await fetch(base + '/__fixture')).json(); if (fixture.fixture === 'ticket08-private-simulator') break } catch {}
    if (Date.now() > deadline) throw new Error('private L3 fixture not ready')
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  assert.equal(fixture.fixture, 'ticket08-private-simulator')
  ready = true
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  page.setDefaultTimeout(15000)
  const errors = [], posts = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('request', req => { if (req.method() === 'POST' && req.url().endsWith('/verify')) posts.push(req.postDataJSON()) })
  await page.addInitScript(token => { if (location.protocol.startsWith('http')) localStorage.setItem('fastems-token', token) }, fixture.token)
  const access = page.locator('.access-step')
  const button = text => access.locator('uni-button').filter({ hasText: new RegExp(`^${text}$`) })
  const open = async () => {
    await page.locator('.design-ledger .node').filter({ hasText: '安全验证模拟设备' }).click()
    await page.locator('.stepper .step').filter({ hasText: '接设备' }).click()
    await access.locator('.pipeline uni-button').filter({ hasText: /^验证$/ }).click()
  }
  await page.goto(base + '/uniapp/#/pages/design/index')
  await open()
  const execute = button('执行一次 L3 写回读')
  assert.equal(await execute.getAttribute('disabled'), 'true')
  assert.deepEqual((await (await fetch(base + '/__fixture')).json()).writes || [], [])
  await button('L1 连通验证（只读）').click()
  await access.locator('.line-result').filter({ hasText: 'L1：通过' }).waitFor()
  await button('L2 必需点验证（只读）').click()
  await access.locator('.line-result').filter({ hasText: 'L2：通过' }).waitFor()
  assert.deepEqual((await (await fetch(base + '/__fixture')).json()).writes || [], [])
  const confirm = access.locator('.l3-box uni-checkbox')
  await confirm.click()
  await execute.click()
  await access.locator('.line-result').filter({ hasText: 'L3：通过' }).waitFor()
  assert.ok(posts.at(-1).request_id)
  assert.ok(posts.at(-1).basis)
  const firstID = posts.at(-1).request_id
  assert.deepEqual((await (await fetch(base + '/__fixture')).json()).writes, [[1, 10, 20]])
  await page.reload(); await open()
  await access.getByText('自动验证历史', { exact: false }).waitFor()
  assert.match(await access.innerText(), new RegExp(firstID))
  await button('查询 L3 结果（不重发）').click()
  await access.locator('.line-result').filter({ hasText: '已回读' }).waitFor()
  assert.equal(posts.filter(p => p.level === 'L3').length, 1)
  await fetch(base + '/__uncertain', { method: 'POST' })
  await confirm.click(); await execute.click()
  await access.locator('.error').filter({ hasText: '结果未知' }).waitFor()
  const unknownID = posts.at(-1).request_id
  assert.notEqual(unknownID, firstID)
  await page.reload(); await open()
  assert.match(await access.innerText(), new RegExp(unknownID))
  await button('查询 L3 结果（不重发）').click()
  await access.locator('.line-result').filter({ hasText: '结果未知' }).waitFor()
  assert.equal(posts.filter(p => p.level === 'L3').length, 2)
  assert.deepEqual((await (await fetch(base + '/__fixture')).json()).writes, [[1, 10, 20], [1, 10, 20]])
  for (const width of [1440, 1280, 1024, 375]) {
    await page.setViewportSize({ width, height: 900 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `overflow at ${width}`)
  }
  await page.setViewportSize({ width: 1280, height: 800 })
  await mkdir(new URL('../../../../.scratch/', import.meta.url), { recursive: true })
  await page.screenshot({ path: fileURLToPath(new URL('../../../../.scratch/l3-browser.png', import.meta.url)), fullPage: true })
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ ok: true, L1L2ZeroWrites: true, exactWrites: [[1,10,20],[1,10,20]], persistedIDs: [firstID, unknownID], noReissue: true, widths: [1440,1280,1024,375] }))
} finally {
  await browser?.close()
  if (ready) await fetch(base + '/__finish', { method: 'POST' }).catch(() => {})
}
