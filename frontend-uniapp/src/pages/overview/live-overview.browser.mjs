// Browser seam: real Go HTTP when FASTIOT_GO_API is set. The HTTP fixture is
// only for deterministic unavailable endpoints and multi-project races.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'

const root = fileURLToPath(new URL('../../../../internal/web/dist/uniapp/', import.meta.url))
const upstream = process.env.FASTIOT_GO_API
const requests = []
let failure = ''
let screenFailure = false
let historyFailure = false
let holdHistory = false
let releaseHistory
const node = (id, parent, type, name, points = []) => ({ id, parent, type, points: [{ type: 'description', text: name }, ...points] })
const nodes = [node('root', '', 'siteRoot', 'Overview test'), node('north', 'root', 'site', 'North'), node('south', 'root', 'site', 'South'), node('pump', 'north', 'device', 'North pump'), node('spare', 'north', 'device', 'Unbound spare'), node('south-pump', 'south', 'device', 'South pump'), node('channel', 'root', 'modbus', 'Test channel'), node('zero', 'channel', 'modbusIo', 'Zero pressure', [{ type: 'units', text: 'MPa' }, { type: 'id', value: 1 }]), node('missing', 'channel', 'modbusIo', 'Missing pressure', [{ type: 'id', value: 1 }])]
nodes.push(node('unbound-value', 'channel', 'modbusIo', 'Unbound telemetry', [{ type: 'id', value: 3 }]))
nodes.push(node('unit', 'north', 'unit', 'North unit', [{ type: 'easyemsUnitMembers', text: JSON.stringify([{ role: '驱动', node_id: 'pump', required: true }, { role: '备用', required: false }]) }]))
const devices = [{ channel_id: 'channel', server_id: 1, business_node_id: 'pump', name: 'North pump', point_count: 2, health: 'healthy' }, { channel_id: 'channel', server_id: 2, business_node_id: 'south-pump', name: 'South pump', point_count: 1, health: 'offline' }]
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const json = (status, data) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)) }
  if (url.pathname.startsWith('/api/')) {
    requests.push({ path: url.pathname, method: req.method, project: req.headers['x-fastiot-project'] })
    let body = ''; for await (const part of req) body += part
    if (screenFailure && url.pathname.startsWith('/api/ems/screen/')) return json(403, { error: 'forbidden' })
    if (historyFailure && url.pathname === '/api/history') return json(500, { error: 'test_history_failed' })
    if (holdHistory && url.pathname === '/api/history' && url.searchParams.get('aggregate') === 'raw') {
      holdHistory = false
      await new Promise(resolve => { releaseHistory = resolve })
      return json(200, { results: [{ records: [{ point_id: url.searchParams.get('node_id'), source_time: new Date().toISOString(), quality: 'good', value: 987, unit: 'MPa' }] }] })
    }
    if (failure && url.pathname === '/api/ems/state') {
      if (failure === 'offline') return req.socket.destroy()
      return json(failure === 'denied' ? 403 : 500, { error: failure === 'denied' ? 'forbidden' : 'test_query_failed' })
    }
    if (upstream) {
      try {
        const response = await fetch(upstream + req.url, { method: req.method, headers: { 'Content-Type': 'application/json', ...(req.headers.authorization ? { Authorization: req.headers.authorization } : {}), ...(req.headers['x-fastiot-project'] ? { 'X-Fastiot-Project': req.headers['x-fastiot-project'] } : {}) }, body: req.method === 'GET' ? undefined : body })
        res.writeHead(response.status, { 'Content-Type': 'application/json' }); res.end(Buffer.from(await response.arrayBuffer()))
      } catch { json(502, { error: 'test_backend_unavailable' }) }
      return
    }
    if (url.pathname === '/api/login') return json(200, { token: 'overview-test-only' })
    if (url.pathname === '/api/projects') return json(200, { user_id: 'test', projects: [{ id: 'root', root_id: 'root', name: 'Overview test', scope_root_ids: ['root'], sites: [], dependencies: [], runtime: { kind: 'local', root_id: 'root' } }] })
    if (url.pathname === '/api/snapshot') return json(200, { roots: ['root'], nodes, user: { user_id: 'test', policy: { admin: true } } })
    if (url.pathname === '/api/device-ledger') return json(200, { devices })
    if (url.pathname === '/api/ems/state') return json(200, { evaluated_at: new Date().toISOString(), values: [{ device_id: 'channel/3', point_id: 'unbound-value', value: { kind: 'number', number: 777 }, quality: 'good', source_time: new Date().toISOString() }, { device_id: 'channel/1', point_id: 'zero', value: { kind: 'number', number: 0 }, quality: 'good', source_time: new Date().toISOString(), time_source: 'platform_acquisition' }] })
    if (url.pathname === '/api/ems/quality') return json(200, { channels: [], devices: [] })
    if (url.pathname === '/api/ems/alarms') return json(200, { alarms: [] })
    if (url.pathname.startsWith('/api/ems/screen/')) return json(404, { error: 'screen_not_found' })
    if (url.pathname === '/api/history') return json(200, { results: [{ records: [] }] })
    if (url.pathname === '/api/overview/handover') return json(200, {})
    return json(404, { error: 'fixture_not_implemented' })
  }
  const file = path.resolve(root, url.pathname.replace(/^\/uniapp\/?/, '') || 'index.html')
  if (!file.startsWith(root)) { res.writeHead(404); return res.end() }
  try { const content = await readFile(file); res.writeHead(200, { 'Content-Type': { '.js': 'application/javascript', '.css': 'text/css', '.html': 'text/html' }[path.extname(file)] || 'application/octet-stream' }); res.end(content) }
  catch { res.writeHead(404); res.end() }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
let browser
let page
try {
  if (upstream) {
    const deadline = Date.now() + 60000
    for (;;) {
      try { if (await (await fetch(upstream + '/__fixture')).text() === 'ticket09-isolated-observations') break } catch {}
      if (Date.now() >= deadline) throw new Error('isolated Go backend did not become ready')
      await new Promise(resolve => setTimeout(resolve, 500))
    }
  }
  try { browser = await chromium.launch({ channel: 'chrome', headless: true, timeout: 15000 }) } catch { browser = await chromium.launch({ channel: 'msedge', headless: true, timeout: 15000 }) }
  page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  page.setDefaultTimeout(12000)
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${server.address().port}/uniapp/#/pages/login/index`)
  await page.locator('.login input[type="text"]').fill('admin')
  await page.locator('.login input[type="password"]').fill('overview-browser-test-only')
  await page.keyboard.press('Enter')
  await page.locator('.ledger .node').filter({ hasText: 'North' }).first().waitFor()
  assert.doesNotMatch(await page.locator('.metrics').innerText(), /Unbound telemetry|777/, 'root cannot promote unbound telemetry to live metrics')
  await page.locator('.ledger .node').filter({ hasText: /现场.*North/ }).first().click()
  await page.locator('.headline h1').filter({ hasText: 'North' }).waitFor()
  await page.waitForFunction(() => [...document.querySelectorAll('.metric')].some(el => el.textContent.includes('Zero pressure') && el.querySelector('.v')?.textContent.startsWith('0')))
  assert.equal(await page.locator('.ledger').getByText('Unbound spare', { exact: true }).count(), 0)
  assert.equal(await page.locator('.ledger .node').filter({ hasText: 'North pump' }).count(), 1, 'one stable device layer, not duplicate binding and business rows')
  assert.match(await page.locator('.metrics').innerText(), /Missing pressure/)
  assert.match(await page.locator('.metrics').innerText(), /无数据/)
  if (upstream) await page.locator('.lower .panel').nth(1).locator('tbody td.num').filter({ hasText: /^0$/ }).waitFor()
  console.log('PASS default layer exposes bound zero and missing points, hides unbound devices')
  if (process.env.FASTIOT_SCREENSHOT) await page.screenshot({ path: process.env.FASTIOT_SCREENSHOT, fullPage: true })
  for (const width of [1280, 1024, 375]) {
    await page.setViewportSize({ width, height: width === 375 ? 812 : 800 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `no page overflow at ${width}`)
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  assert.match(await page.locator('.flow').innerText(), /North pump/)
  assert.doesNotMatch(await page.locator('.flow').innerText(), /South pump/)
  await page.locator('.ledger .node').filter({ hasText: 'North unit' }).click()
  await page.waitForFunction(() => document.querySelector('.flow')?.textContent.includes('驱动'))
  assert.match(await page.locator('.metrics').innerText(), /Zero pressure/)
  assert.match(await page.locator('.flow').innerText(), /未绑定成员/)
  if (upstream) {
    const stale = page.locator('.metric').filter({ hasText: 'Stale pressure' })
    assert.match(await stale.innerText(), /过期/)
    assert.match(await stale.locator('.v').innerText(), /—/)
  }
  await page.locator('.ledger .node').filter({ hasText: /现场.*South/ }).click()
  await page.locator('.headline h1').filter({ hasText: 'South' }).waitFor()
  assert.doesNotMatch(await page.locator('.metrics').innerText(), /Zero pressure/)
  if (upstream) await page.locator('.lower tbody').filter({ hasText: '离线' }).first().waitFor()
  if (!upstream) {
    holdHistory = true
    await page.locator('.ledger .node').filter({ hasText: /现场.*North/ }).click()
    const deadline = Date.now() + 10000
    while (!releaseHistory && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 50))
    assert.ok(releaseHistory, 'held old-layer history response')
    await page.locator('.ledger .node').filter({ hasText: /现场.*South/ }).click()
    releaseHistory(); releaseHistory = undefined
    await page.waitForTimeout(200)
    assert.doesNotMatch(await page.locator('.lower').innerText(), /987/)
  }
  await page.reload()
  await page.locator('.headline h1').filter({ hasText: 'South' }).waitFor()
  for (const state of ['denied', 'error', 'offline']) {
    failure = state
    await page.waitForFunction(expected => document.querySelector('.page-error')?.textContent.includes(expected), state === 'denied' ? '无权' : state === 'offline' ? '连接中断' : '查询失败')
    assert.doesNotMatch(await page.locator('.online').innerText(), /在线 \d/)
    assert.doesNotMatch(await page.locator('.metrics').innerText(), /23/)
  }
  failure = ''
  await page.waitForFunction(() => !document.querySelector('.page-error'))
  await page.locator('.ledger .node').filter({ hasText: /现场.*North/ }).click()
  historyFailure = true
  await page.locator('.lower .error').filter({ hasText: '最近数据读取失败' }).waitFor()
  historyFailure = false
  await page.waitForFunction(() => ![...document.querySelectorAll('.lower .error')].some(el => el.textContent.includes('最近数据读取失败')))
  screenFailure = true
  await page.reload()
  await page.locator('.page-error').filter({ hasText: '画面读取失败' }).waitFor()
  assert.doesNotMatch(await page.locator('.metrics').innerText(), /Zero pressure/)
  screenFailure = false
  await page.reload()
  await page.locator('.metric').filter({ hasText: 'Zero pressure' }).waitFor()
  assert.ok(requests.filter(item => !['/api/login', '/api/projects'].includes(item.path)).every(item => item.project))
  assert.equal(requests.filter(item => item.method !== 'GET' && item.path !== '/api/login').length, 0, 'no business or physical writes')
  assert.deepEqual(errors, [])
  console.log(`PASS overview scope/refresh/denied/query/connection recovery (${upstream ? 'real Go + fault proxy' : 'HTTP fixture'})`)
} catch (error) {
  if (page) console.error('Visible failure state:', await page.locator('body').innerText())
  throw error
} finally {
  await browser?.close(); server.closeAllConnections(); await new Promise(resolve => server.close(resolve))
  if (upstream) await fetch(upstream + '/__finish', { method: 'POST' }).catch(() => {})
}
