// Self-contained browser acceptance. FASTIOT_GO_API uses a disposable Go test
// server; otherwise a two-context HTTP fixture exercises switching races that
// a single-project local runtime deliberately cannot host.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'

const root = fileURLToPath(new URL('../../../internal/web/dist/uniapp/', import.meta.url))
const upstream = process.env.FASTIOT_GO_API
const requests = []
let holdSnapshot = false
let releaseSnapshot
const projects = ['a', 'b'].map(id => ({ id, root_id: id, name: `Project ${id.toUpperCase()}`, scope_root_ids: [id], runtime: { kind: 'local', root_id: id }, sites: [], dependencies: [] }))
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const json = (status, data) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)) }
  if (url.pathname.startsWith('/api/')) {
    let body = ''
    for await (const chunk of req) body += chunk
    requests.push({ method: req.method, path: url.pathname, project: req.headers['x-fastiot-project'] })
    if (upstream) {
      try {
        const result = await fetch(upstream + req.url, { method: req.method, headers: { 'Content-Type': 'application/json', ...(req.headers.authorization ? { Authorization: req.headers.authorization } : {}), ...(req.headers['x-fastiot-project'] ? { 'X-Fastiot-Project': req.headers['x-fastiot-project'] } : {}) }, body: ['GET', 'HEAD'].includes(req.method) ? undefined : body })
        res.writeHead(result.status, { 'Content-Type': result.headers.get('content-type') || 'application/json' }); res.end(Buffer.from(await result.arrayBuffer()))
      } catch { json(502, { error: 'test_backend_unavailable' }) }
      return
    }
    if (url.pathname === '/api/login') return json(200, { token: 'project-fixture-session' })
    if (!req.headers.authorization) return json(401, { error: 'unauthorized' })
    if (url.pathname === '/api/projects') return json(200, { user_id: 'fixture', projects })
    const id = req.headers['x-fastiot-project']
    if (!['a', 'b'].includes(id)) return json(409, { error: 'project_context_mismatch' })
    if (url.pathname === '/api/snapshot') {
      if (holdSnapshot && id === 'a') { holdSnapshot = false; await new Promise(resolve => { releaseSnapshot = resolve }) }
      return json(200, { roots: [id], user: { user_id: 'fixture', policy: { admin: true }, actions: [] }, nodes: [
        { id, parent: 'root', type: 'siteRoot', points: [{ type: 'description', text: `Project ${id.toUpperCase()}` }] },
        { id: `${id}-site`, parent: id, type: 'site', points: [{ type: 'description', text: `Site ${id.toUpperCase()}` }] },
      ] })
    }
    if (url.pathname === '/api/industry/packages') return json(200, { packages: [] })
    if (url.pathname === '/api/ems/state') return json(200, { values: [], evaluated_at: '' })
    if (url.pathname === '/api/ems/quality') return json(200, { channels: [], devices: [] })
    if (url.pathname === '/api/ems/alarms') return json(200, { alarms: [] })
    if (url.pathname === '/api/device-ledger') return json(200, { devices: [] })
    return json(403, { error: 'fixture_not_implemented' })
  }
  const candidate = path.resolve(root, url.pathname.replace(/^\/uniapp\/?/, '') || 'index.html')
  if (!candidate.startsWith(root)) { res.writeHead(404); return res.end() }
  try {
    const data = await readFile(candidate)
    res.writeHead(200, { 'Content-Type': { '.js': 'application/javascript', '.css': 'text/css', '.html': 'text/html' }[path.extname(candidate)] || 'application/octet-stream' }); res.end(data)
  } catch { res.writeHead(404); res.end() }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const base = `http://127.0.0.1:${server.address().port}/uniapp/`
let browser
let fixtureReady = false
try {
  if (upstream) {
    const deadline = Date.now() + 90000
    for (;;) {
      try { if (await (await fetch(upstream + '/__fixture')).text() === 'ticket02-isolated-store') { fixtureReady = true; break } } catch {}
      if (Date.now() >= deadline) throw new Error('Go test backend did not become ready')
      await new Promise(resolve => setTimeout(resolve, 500))
    }
  }
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  page.setDefaultTimeout(10000)
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(base + '#/pages/login/index')
  await page.locator('.login input[type="text"]').fill(upstream ? 'admin' : 'fixture')
  await page.locator('.login input[type="password"]').fill('context-browser-test-only')
  await page.keyboard.press('Enter')
  const menu = page.locator('.project-button')
  await menu.waitFor()
  await page.waitForFunction(() => document.querySelector('.project-button')?.textContent.includes('Project A') || document.querySelector('.project-button')?.textContent.includes('FastIOT'))
  await menu.focus(); await page.keyboard.press('Enter')
  await page.getByRole('menu').waitFor()
  const projectName = upstream ? 'FastIOT' : 'Project A'
  assert.match(await menu.innerText(), new RegExp(projectName))
  await page.keyboard.press('Escape')
  assert.equal(await page.getByRole('menu').count(), 0)
  for (const label of ['设计', '交付', '总览']) {
    await page.locator('.workbench uni-button').filter({ hasText: label }).click()
    await page.locator(`.workbench uni-button.active`).filter({ hasText: label }).waitFor()
    assert.match(await menu.innerText(), new RegExp(projectName))
    if (upstream && label === '设计') await page.locator('.design-ledger .node').filter({ hasText: 'South Site' }).click()
    if (upstream && label === '交付') {
      await page.waitForFunction(() => document.querySelector('.site-select')?.selectedOptions[0]?.textContent === 'South Site')
    }
    if (upstream && label === '总览') await page.locator('.headline h1').filter({ hasText: 'South Site' }).waitFor()
  }
  await page.reload()
  await page.waitForFunction(expected => document.querySelector('.project-button')?.textContent.includes(expected), projectName)
  if (upstream) await page.locator('.headline h1').filter({ hasText: 'South Site' }).waitFor()
  assert.match(await menu.innerText(), new RegExp(projectName))
  if (!upstream) {
    await page.locator('.workbench uni-button').filter({ hasText: '设计' }).click()
    await page.locator('.site-name input, input.site-name').first().fill('unsaved draft A')
    await menu.focus(); await page.keyboard.press('ArrowDown')
    await page.waitForFunction(() => document.activeElement?.getAttribute('role') === 'menuitemradio')
    await page.keyboard.press('ArrowDown')
    assert.equal(await page.evaluate(() => document.activeElement?.textContent), 'Project B')
    await page.keyboard.press('Enter')
    await page.getByText('取消', { exact: true }).last().click()
    assert.match(await menu.innerText(), /Project A/)
    assert.equal(await page.locator('.site-name input, input.site-name').first().inputValue(), 'unsaved draft A')
    await menu.click(); await page.getByRole('menuitemradio', { name: 'Project B' }).click()
    await page.getByText('放弃并切换', { exact: true }).click()
    await page.waitForFunction(() => document.querySelector('.project-button')?.textContent.includes('Project B'))
    await page.locator('.site-name input, input.site-name').first().waitFor()
    assert.equal(await page.locator('.site-name input, input.site-name').first().inputValue(), '')
    await page.reload()
    await page.waitForFunction(() => document.querySelector('.project-button')?.textContent.includes('Project B'))
    assert.match(await menu.innerText(), /Project B/)
    // Delay an A read, switch before it arrives, then release it.
    await menu.click(); await page.getByRole('menuitemradio', { name: 'Project A' }).click()
    holdSnapshot = true
    await page.getByText('放弃并切换', { exact: true }).click()
    await page.waitForFunction(() => document.querySelector('.project-button')?.textContent.includes('Project A'))
    await menu.click(); await page.getByRole('menuitemradio', { name: 'Project B' }).click()
    await page.getByText('放弃并切换', { exact: true }).click()
    await page.waitForFunction(() => document.querySelector('.project-button')?.textContent.includes('Project B'))
    assert.ok(releaseSnapshot, 'old snapshot was held')
    releaseSnapshot()
    await page.waitForTimeout(300)
    assert.equal(await page.getByText('Site A', { exact: true }).count(), 0)
  }
  await page.goto(base + '#/pages/login/index')
  await page.locator('.login input[type="text"]').fill(upstream ? 'admin' : 'fixture')
  await page.locator('.login input[type="password"]').fill('context-browser-test-only')
  await page.keyboard.press('Enter')
  await page.waitForFunction(expected => document.querySelector('.project-button')?.textContent.includes(expected), upstream ? 'FastIOT' : 'Project B')
  if (upstream) await page.locator('.headline h1').filter({ hasText: 'South Site' }).waitFor()
  assert.ok(requests.some(request => request.path === '/api/projects'))
  assert.ok(requests.filter(request => !['/api/login', '/api/projects'].includes(request.path)).every(request => request.project), 'every business request carries project scope')
  assert.deepEqual(errors, [])
  console.log(`PASS project context browser (${upstream ? 'real Go HTTP: one local project' : 'two-context fixture: dirty discard + delayed response'})`)
} finally {
  releaseSnapshot?.()
  await browser?.close()
  if (upstream && fixtureReady) await fetch(upstream + '/__finish', { method: 'POST' }).catch(() => {})
  await new Promise(resolve => server.close(resolve))
}
