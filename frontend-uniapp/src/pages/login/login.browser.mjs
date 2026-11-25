// Real loopback HTTP boundary, not Playwright request interception. No devices
// or production backend are connected. Go token validation is tested separately.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'

const root = fileURLToPath(new URL('../../../../internal/web/dist/uniapp/', import.meta.url))
const requests = []
let expired = false
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const json = (status, data) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)) }
  if (url.pathname.startsWith('/api/')) {
    let text = ''
    for await (const chunk of req) text += chunk
    requests.push({ method: req.method, path: url.pathname, authorization: req.headers.authorization })
    if (url.pathname === '/api/login') {
      const body = JSON.parse(text)
      if (body.password === 'offline-fixture') return req.socket.destroy()
      if (body.password === 'failure-fixture') return json(500, { error: 'fixture_server_failure' })
      if (body.email !== 'operator' || body.password !== 'valid-fixture') return json(403, { error: 'invalid_credentials' })
      expired = false
      // Delay exposes double-submit and ensures the keyboard busy guard is exercised.
      return setTimeout(() => json(200, { token: 'http-live-session' }), 150)
    }
    if (expired || req.headers.authorization !== 'Bearer http-live-session') return json(401, { error: 'unauthorized' })
    if (req.method !== 'GET') return json(403, { error: 'fixture_rejects_all_business_writes' })
    if (url.pathname === '/api/snapshot') return json(200, {
      user: { user_id: 'fixture-operator', policy: { role: 'viewer', admin: false }, actions: [] }, roots: ['fixture-site'],
      nodes: [{ id: 'fixture-site', type: 'site', parent: '', points: [{ type: 'description', key: '', text: 'HTTP 隔离验收现场', time: '' }] }],
    })
    if (url.pathname === '/api/ems/state') return json(200, { values: [], evaluated_at: new Date().toISOString() })
    if (url.pathname === '/api/ems/quality') return json(200, { channels: [], devices: [] })
    if (url.pathname === '/api/ems/alarms') return json(200, { alarms: [] })
    if (url.pathname === '/api/device-ledger') return json(200, { devices: [] })
    return json(403, { error: 'forbidden' })
  }
  const relative = url.pathname.replace(/^\/uniapp\/?/, '')
  const candidate = path.resolve(root, relative || 'index.html')
  if (!candidate.startsWith(root)) { res.writeHead(404); return res.end() }
  try {
    const file = path.extname(candidate) ? candidate : path.join(root, 'index.html')
    const data = await readFile(file)
    const type = { '.js': 'application/javascript', '.css': 'text/css', '.html': 'text/html', '.svg': 'image/svg+xml' }[path.extname(file)] || 'application/octet-stream'
    res.writeHead(200, { 'Content-Type': type }); res.end(data)
  } catch { res.writeHead(404); res.end() }
})
await new Promise(resolve => server.listen(process.argv.includes('--serve') ? 4411 : 0, '127.0.0.1', resolve))
const base = `http://127.0.0.1:${server.address().port}/uniapp/`

if (process.argv.includes('--serve')) {
  console.log(`Isolated auth fixture ready at ${base}`)
} else {
  let browser
  try {
    try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
    catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    await context.addInitScript(() => {
      window.liveTokenReads = 0
      const getItem = Storage.prototype.getItem
      Storage.prototype.getItem = function(key) {
        if (key === 'fastems-token') window.liveTokenReads++
        return getItem.call(this, key)
      }
    })
    await page.goto(`${base}#/pages/overview/index`)
    await page.locator('.login').waitFor()
    assert.deepEqual(requests, [], 'a protected entry without a session must go straight to login')
    assert.equal(await page.locator('.preview-page').count(), 0)
    const heights = await page.locator('.login uni-input input').evaluateAll(elements => elements.map(element => element.getBoundingClientRect().height))
    assert.ok(heights.every(height => height >= 20), `keyboard inputs must be visible: ${heights}`)
    await page.locator('.login uni-input').nth(0).click()
    await page.keyboard.type('operator')
    assert.equal(await page.locator('.login input[type="text"]').inputValue(), 'operator')
    const password = page.locator('.login input[type="password"]')
    for (const [value, message] of [['wrong-fixture', 'invalid_credentials'], ['failure-fixture', 'fixture_server_failure'], ['offline-fixture', '登录失败']]) {
      await password.fill(value)
      await page.keyboard.press('Enter')
      await page.locator('.login .error').filter({ hasText: message }).waitFor().catch(async error => { console.error({ requests, page: await page.locator('body').innerText(), errors }); throw error })
      assert.equal(await page.locator('.preview-page').count(), 0)
      assert.equal(await page.evaluate(() => localStorage.getItem('fastems-token')), null)
    }
    await password.fill('valid-fixture')
    const beforeLogin = requests.filter(r => r.path === '/api/login').length
    await page.keyboard.press('Enter')
    await page.keyboard.press('Enter')
    await page.getByText('HTTP 隔离验收现场', { exact: true }).first().waitFor()
    assert.equal(requests.filter(r => r.path === '/api/login').length - beforeLogin, 1, 'Enter while busy must not log in twice')
    assert.ok(requests.some(r => r.path === '/api/snapshot' && r.authorization === 'Bearer http-live-session'))
    assert.ok(requests.filter(r => r.path === '/api/login').every(r => !r.authorization))
    assert.equal(await page.locator('.preview-page').count(), 0)
    expired = true
    await page.locator('.login').waitFor({ timeout: 8000 })
    assert.equal(await page.evaluate(() => localStorage.getItem('fastems-token')), null)
    const afterExpiry = requests.length
    await page.waitForTimeout(2300)
    assert.equal(requests.length, afterExpiry, 'expired polling must stop')
    await page.reload()
    await page.locator('.login').waitFor()
    // A valid live session survives preview login, actions, navigation and reload.
    await page.locator('.login input[type="text"]').fill('operator')
    await password.fill('valid-fixture')
    await page.keyboard.press('Enter')
    await page.getByText('HTTP 隔离验收现场', { exact: true }).first().waitFor()
    const liveToken = await page.evaluate(() => localStorage.getItem('fastems-token'))
    await page.goto(`${base}#/pages/login/index`)
    await page.getByRole('link', { name: '进入隔离原型预览' }).click()
    await page.locator('.preview-page').waitFor()
    await page.getByText('原型预览', { exact: true }).first().waitFor()
    const beforePreview = requests.length
    await page.locator('[data-layer="d1"]').click()
    await page.locator('[data-op-open]').click()
    await page.locator('[data-op="启动"]').click()
    await page.locator('.drawer.open .x').click()
    await page.locator('.topbar .workbench uni-button').filter({ hasText: '设计' }).click()
    await page.locator('.stepper').waitFor()
    await page.locator('.topbar .workbench uni-button').filter({ hasText: '交付' }).click()
    await page.locator('.variant-head').first().waitFor()
    await page.reload()
    await page.locator('.variant-head').first().waitFor()
    await page.goto(`${base}?preview=1#/pages/login/index`)
    await page.locator('.login input[type="text"]').fill('preview')
    await password.fill('inert-fixture')
    await page.keyboard.press('Enter')
    await page.locator('.preview-page').waitFor()
    assert.equal(await page.evaluate(() => window.liveTokenReads), 0, 'preview must not read a live credential')
    assert.equal(await page.evaluate(() => localStorage.getItem('fastems-token')), liveToken)
    assert.equal(requests.length, beforePreview, 'preview must send zero API requests, including physical writes')
    await page.goto(`${base}?preview=1#/pages/login/index`)
    await page.getByRole('link', { name: '返回真实登录' }).click()
    await page.locator('.login').waitFor()
    assert.equal(new URL(page.url()).searchParams.has('preview'), false)
    assert.equal(await page.evaluate(() => localStorage.getItem('fastems-token')), liveToken)
    assert.deepEqual(errors, [])
    assert.deepEqual(requests.filter(r => r.method !== 'GET' && r.path !== '/api/login'), [])
    console.log(JSON.stringify({ ok: true, checks: ['default live routing', 'keyboard login and busy guard', '403/500/offline without fallback', 'HTTP bearer session', '401 expiry stops polling', 'explicit labelled preview', 'preview navigation/reload/login token isolation', 'zero physical writes'], requests: requests.length, heights }))
    for (const [script, variable, route] of [
      ['../overview/overview-preview.browser.mjs', 'PREVIEW_BASE_URL', 'overview'],
      ['../../components/design/design-preview.browser.mjs', 'DESIGN_PREVIEW_URL', 'design'],
      ['../../components/delivery/delivery-preview.browser.mjs', 'DELIVERY_PREVIEW_URL', 'delivery'],
    ]) {
      const { stdout } = await promisify(execFile)(process.execPath, [fileURLToPath(new URL(script, import.meta.url))], {
        env: { ...process.env, [variable]: `${base}pages/${route}/index` },
      })
      process.stdout.write(stdout)
    }
  } finally {
    if (browser) await browser.close()
    await new Promise(resolve => server.close(resolve))
  }
}
