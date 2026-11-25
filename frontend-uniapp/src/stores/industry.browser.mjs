// Real Go + isolated JetStream store only; no response or DOM substitution.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile, mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'

const upstream = process.env.FASTIOT_GO_API || 'http://127.0.0.1:18084'
const deadline = Date.now() + 90000
for (;;) {
  try { assert.equal(await (await fetch(upstream + '/__fixture')).text(), 'ticket04-isolated-store'); break } catch (error) { if (Date.now() >= deadline) throw error }
  await new Promise(resolve => setTimeout(resolve, 500))
}
const root = fileURLToPath(new URL('../../../internal/web/dist/uniapp/', import.meta.url))
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  if (url.pathname.startsWith('/api/')) {
    let body = ''
    for await (const chunk of req) body += chunk
    try {
      const result = await fetch(upstream + req.url, { method: req.method, headers: { 'Content-Type': 'application/json', ...(req.headers.authorization ? { Authorization: req.headers.authorization } : {}), ...(req.headers['x-fastiot-project'] ? { 'X-Fastiot-Project': req.headers['x-fastiot-project'] } : {}) }, body: ['GET', 'HEAD'].includes(req.method) ? undefined : body })
      res.writeHead(result.status, { 'Content-Type': result.headers.get('content-type') || 'application/json' }); res.end(Buffer.from(await result.arrayBuffer()))
    } catch { res.writeHead(502); res.end() }
    return
  }
  const candidate = path.resolve(root, url.pathname.replace(/^\/uniapp\/?/, '') || 'index.html')
  if (!candidate.startsWith(root)) { res.writeHead(404); return res.end() }
  try { const data = await readFile(candidate); res.writeHead(200, { 'Content-Type': { '.js': 'application/javascript', '.css': 'text/css', '.html': 'text/html' }[path.extname(candidate)] || 'application/octet-stream' }); res.end(data) }
  catch { res.writeHead(404); res.end() }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
let browser
try {
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  page.setDefaultTimeout(10000)
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${server.address().port}/uniapp/#/pages/login/index`)
  await page.locator('.login input[type="text"]').fill('admin')
  await page.locator('.login input[type="password"]').fill('industry-browser-test-only')
  await page.keyboard.press('Enter')
  const navigate = async label => {
    await page.locator('.workbench uni-button').filter({ hasText: label }).click()
    await page.locator('.workbench uni-button.active').filter({ hasText: label }).waitFor()
    await page.waitForFunction(() => document.querySelector('.project-button')?.textContent.includes('FastIOT'))
  }
  await page.locator('.headline h1').waitFor()
  for (const label of ['设计', '交付', '总览']) {
    await navigate(label)
    assert.equal(await page.locator('.industry-gate').count(), 0, 'ordinary IoT is not gated')
  }
  assert.equal((await fetch(upstream + '/__require-ems', { method: 'POST' })).status, 204)
  await page.reload()
  await page.locator('.industry-gate').waitFor()
  await mkdir('.scratch/ticket04', { recursive: true })
  for (const label of ['设计', '交付', '总览']) {
    await navigate(label)
    await page.locator('.industry-gate').waitFor()
    assert.match(await page.locator('.industry-gate').innerText(), /EMS/)
    assert.equal(await page.locator('.design-ledger, .headline, .delivery-package').count(), 0, 'dependent workbench body must not remain interactive')
    await page.screenshot({ path: `.scratch/ticket04/gate-${label}.png` })
  }
  await navigate('交付')
  await page.locator('.side uni-button').filter({ hasText: '行业包' }).click()
  await page.locator('.industry-banner uni-button').filter({ hasText: '启用' }).click()
  await page.locator('.industry-card .tag.ok').filter({ hasText: '已启用' }).waitFor()
  assert.match(await page.locator('.industry-card').innerText(), /控制授权/)
  await page.locator('.industry-toggle').click()
  await page.locator('.industry-toggle').filter({ hasText: '确认停用' }).click()
  await page.getByText('行业配置仍在使用，后端已拒绝停用', { exact: true }).waitFor()
  for (const label of ['设计', '总览', '交付']) {
    await navigate(label)
    assert.equal(await page.locator('.industry-gate').count(), 0, 'activation recovers all workbenches')
  }
  await page.reload()
  await page.locator('.project-button').filter({ hasText: 'FastIOT' }).waitFor()
  assert.equal(await page.locator('.industry-gate').count(), 0)
  assert.deepEqual(errors, [])
  console.log('PASS real Go industry gate: ordinary IoT; dependent project in all workbenches; activation; refusal to erase dependency; reload')
} finally {
  await browser?.close()
  await fetch(upstream + '/__finish', { method: 'POST' }).catch(() => {})
  await new Promise(resolve => server.close(resolve))
}
