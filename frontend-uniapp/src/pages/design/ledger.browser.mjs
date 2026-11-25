// Real Go only: run TestLedgerBrowserBackend on isolated port 18083 first.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'

const root = fileURLToPath(new URL('../../../../internal/web/dist/uniapp/', import.meta.url))
const upstream = process.env.FASTIOT_GO_API || 'http://127.0.0.1:18083'
const requests = []
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  if (url.pathname.startsWith('/api/')) {
    let body = ''
    for await (const chunk of req) body += chunk
    try {
      const result = await fetch(upstream + req.url, { method: req.method, headers: { 'Content-Type': 'application/json', ...(req.headers.authorization ? { Authorization: req.headers.authorization } : {}), ...(req.headers['x-fastiot-project'] ? { 'X-Fastiot-Project': req.headers['x-fastiot-project'] } : {}) }, body: ['GET', 'HEAD'].includes(req.method) ? undefined : body })
      const data = Buffer.from(await result.arrayBuffer())
      requests.push({ method: req.method, path: url.pathname, project: req.headers['x-fastiot-project'], body: body ? JSON.parse(body) : null, status: result.status, result: JSON.parse(data.toString()) })
      res.writeHead(result.status, { 'Content-Type': result.headers.get('content-type') || 'application/json' }); res.end(data)
    } catch { res.writeHead(502); res.end('{"error":"test_backend_unavailable"}') }
    return
  }
  const candidate = path.resolve(root, url.pathname.replace(/^\/uniapp\/?/, '') || 'index.html')
  if (!candidate.startsWith(root)) { res.writeHead(404); return res.end() }
  try {
    const data = await readFile(candidate)
    res.writeHead(200, { 'Content-Type': { '.js': 'application/javascript', '.css': 'text/css', '.html': 'text/html' }[path.extname(candidate)] || 'application/octet-stream' }); res.end(data)
  } catch { res.writeHead(404); res.end() }
})
let browser
let fixtureReady = false
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
try {
  const deadline = Date.now() + 90000
  for (;;) {
    try { if (await (await fetch(upstream + '/__fixture')).text() === 'ticket03-isolated-store') { fixtureReady = true; break } } catch {}
    if (Date.now() >= deadline) throw new Error('isolated Go fixture not ready')
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  page.setDefaultTimeout(15000)
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  const base = `http://127.0.0.1:${server.address().port}/uniapp/`
  await page.goto(base + '#/pages/login/index')
  await page.locator('.login input[type="text"]').fill('admin')
  await page.locator('.login input[type="password"]').fill('ledger-browser-test-only')
  await page.keyboard.press('Enter')
  await page.locator('.workbench uni-button').filter({ hasText: '设计' }).click()
  await page.locator('.tpl-card').filter({ hasText: '供水泵房' }).first().click()
  await page.locator('.site-name input, input.site-name').first().fill('Ledger acceptance')
  await page.locator('uni-button').filter({ hasText: '按「供水泵房」建出来' }).click()
  await page.locator('.notice').filter({ hasText: '现场已创建' }).waitFor()
  const created = requests.find(r => r.method === 'POST' && r.body?.site_template === 'pump-house')
  assert.equal(created.status, 200)
  assert.equal(created.result.created, 5)
  const siteID = created.result.id
  await page.locator('.ledger-worktable tbody tr').filter({ hasText: '供水单元' }).locator('uni-button').filter({ hasText: '＋' }).click()
  assert.deepEqual(await page.locator('.create-line select option').allTextContents(), ['设备'])
  await page.locator('.create-line input').fill('Spare drive')
  await page.locator('.create-line uni-button.primary').click()
  await page.locator('.notice').filter({ hasText: '设备已创建' }).waitFor()
  await page.locator('.tree-search input').fill('总管压力测量')
  assert.equal(await page.locator('.ledger-worktable tbody tr').count(), 1)
  assert.match(await page.locator('.ledger-worktable tbody').innerText(), /总管压力测量/)
  await page.locator('.tree-search input').fill('')
  await page.locator('.design-ledger .node').filter({ hasText: '供水单元' }).click()
  const editor = page.locator('.member-roles')
  await editor.waitFor()
  await editor.locator('tr').filter({ hasText: '移除' }).first().locator('select').selectOption({ label: '泵驱动 · 设备' })
  await editor.locator('tr').filter({ hasText: '移除' }).nth(1).locator('select').selectOption({ label: '总管压力测量 · 设备' })
  // Runtime polling must not replace an unsaved member draft.
  await page.waitForTimeout(6000)
  assert.notEqual(await editor.locator('select').first().inputValue(), '')
  const refreshed = page.waitForResponse(response => response.url().includes('/api/ems/screen/'))
  await editor.locator('uni-button').filter({ hasText: '保存成员绑定' }).click()
  await refreshed
  await editor.locator('.notice').filter({ hasText: '成员角色已保存' }).waitFor()
  const other = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  await other.goto(base + '#/pages/login/index')
  await other.locator('.login input[type="text"]').fill('admin')
  await other.locator('.login input[type="password"]').fill('ledger-browser-test-only')
  await other.keyboard.press('Enter')
  await other.locator('.workbench uni-button').filter({ hasText: '设计' }).click()
  await other.locator('.design-ledger .node').filter({ hasText: '供水单元' }).click()
  await other.locator('.member-roles select').first().waitFor()
  const nextSave = page.waitForResponse(response => response.request().method() === 'PATCH')
  await editor.locator('uni-button').filter({ hasText: '保存成员绑定' }).click()
  assert.equal((await nextSave).status(), 200)
  await other.locator('.member-roles select').nth(1).selectOption('')
  await other.locator('.member-roles uni-button').filter({ hasText: '保存成员绑定' }).click()
  await other.locator('.member-roles .error').filter({ hasText: '成员绑定已被其他人修改' }).waitFor()
  assert.equal(await other.locator('.member-roles select').nth(1).inputValue(), '', 'rejected draft remains visible')
  await other.close()
  await page.reload()
  await page.locator('.design-ledger .node').filter({ hasText: '供水单元' }).click()
  assert.notEqual(await editor.locator('select').first().inputValue(), '')
  await page.locator('.step').filter({ hasText: '搭台账' }).click()
  const unitRow = page.locator('.ledger-worktable tbody tr').filter({ hasText: '供水单元' })
  await unitRow.locator('uni-button').filter({ hasText: '复制' }).click()
  await page.locator('.notice').filter({ hasText: '外部成员引用需重新绑定' }).waitFor()
  await page.locator('.design-ledger .node').filter({ hasText: '供水单元 副本' }).click()
  assert.notEqual(await editor.locator('select').first().inputValue(), '')
  assert.equal(await editor.locator('select').nth(1).inputValue(), '')
  await page.locator('.design-ledger .node').filter({ hasText: 'Ledger acceptance' }).click()
  for (const label of ['交付', '总览', '设计']) {
    await page.locator('.workbench uni-button').filter({ hasText: label }).click()
    if (label === '交付') await page.waitForFunction(() => document.querySelector('.site-select')?.selectedOptions[0]?.textContent === 'Ledger acceptance')
    if (label === '总览') await page.locator('.headline h1').filter({ hasText: 'Ledger acceptance' }).waitFor()
  }
  assert.ok(siteID)
  const scoped = requests.filter(r => !['/api/login', '/api/projects'].includes(r.path))
  assert.ok(scoped.length > 0 && scoped.every(r => r.project === created.project), 'one real project across workbenches')
  assert.deepEqual(errors, [])
  console.log('PASS real Go ledger browser: template, legal child, search, members, refresh feedback, two-editor conflict, reload, copy references, three workbenches')
} finally {
  await browser?.close()
  if (fixtureReady) await fetch(upstream + '/__finish', { method: 'POST' }).catch(() => {})
  await new Promise(resolve => server.close(resolve))
}
