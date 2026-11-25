// Requires the opt-in real Go TestDailyStatsBrowserBackend on loopback 18102.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'
const origin = 'http://127.0.0.1:18102'
let fixture
const deadline = Date.now() + 90000
for (;;) {
  try { fixture = await (await fetch(origin + '/__fixture')).json(); break } catch {}
  if (Date.now() > deadline) throw new Error('Isolated daily Go fixture did not start')
  await new Promise(resolve => setTimeout(resolve, 500))
}
assert.equal(fixture.fixture, 'ticket12-named-volume')
const evidence = fileURLToPath(new URL('../../../../.scratch/ticket12/', import.meta.url))
await mkdir(evidence, { recursive: true })
let browser
try {
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  const errors = [], requests = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('request', req => { if (req.url().includes('/api/')) requests.push({ url: req.url(), method: req.method(), project: req.headers()['x-fastiot-project'] }) })
  page.setDefaultTimeout(15000)
  await page.goto(origin + '/uniapp/#/pages/login/index')
  await page.locator('.login input[type="text"]').fill('daily-viewer@fixture.local')
  await page.locator('.login input[type="password"]').fill('daily-browser-test-only')
  await page.keyboard.press('Enter')
  await page.locator('[data-daily-stat="energy"] b').filter({ hasText: /^13/ }).waitFor()
  const stat = key => page.locator(`[data-daily-stat="${key}"]`)
  assert.match(await stat('energy').innerText(), /13.*kWh/s)
  assert.match(await stat('energy').innerText(), /部分统计.*有效覆盖/)
  assert.match(await stat('energy').getAttribute('title'), /UTC.*缺失不补零.*重启前缺口不补齐/)
  assert.match(await stat('runtime').innerText(), /0.33.*h/s)
  assert.match(await stat('communication').innerText(), /50.*%/s)
  assert.match(await stat('communication').getAttribute('title'), /成功 1 \/ 尝试 2/)
  assert.match(await stat('freshness').innerText(), /有效观测|观测已过期/)
  assert.match(await stat('freshness').getAttribute('title'), /平台成功采集时间.*不是页面刷新时间/)
  await page.screenshot({ path: evidence + 'daily-desktop.png', fullPage: true })
  await page.reload()
  await stat('energy').locator('b').filter({ hasText: /^13/ }).waitFor()
  for (const [width, height] of [[1280, 800], [1024, 768], [375, 812]]) {
    await page.setViewportSize({ width, height })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false, `overflow ${width}`)
    assert.equal(await stat('energy').isVisible(), true)
  }
  await page.screenshot({ path: evidence + 'daily-mobile.png', fullPage: true })
  const checks = await page.evaluate(async fixture => {
    const headers = { Authorization: `Bearer ${localStorage.getItem('fastems-token')}` }
    const projects = await (await fetch('/api/projects', { headers })).json()
    headers['X-Fastiot-Project'] = projects.projects[0].id
    const empty = await (await fetch(`/api/overview/daily-stats?layer=${fixture.empty}`, { headers })).json()
    const foreign = await fetch(`/api/overview/daily-stats?layer=${fixture.root}`, { headers: { ...headers, 'X-Fastiot-Project': 'other-project' } })
    const noLayer = await fetch('/api/overview/daily-stats', { headers })
    return { empty, foreign: foreign.status, noLayer: noLayer.status }
  }, fixture)
  assert.equal(checks.empty.energy.value, null)
  assert.equal(checks.empty.energy.reason, 'binding_missing')
  assert.equal(checks.foreign, 409)
  assert.equal(checks.noLayer, 400)
  assert.ok(requests.some(req => req.url.includes('/overview/daily-stats?') && req.project))
  assert.deepEqual(requests.filter(req => !['GET', 'HEAD'].includes(req.method) && !req.url.endsWith('/login')), [])
  assert.deepEqual(errors, [])
  console.log('PASS real Go daily stats: recorded reset energy/runtime, partial coverage/retention, live communication window, observation freshness, refresh, four viewports, unavailable and project gate; no business writes')
} finally { await browser?.close() }
