// Real Go only: run TestHistoryBrowserBackend on loopback port 18090 first.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile, mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'

const upstream = process.env.FASTIOT_GO_API || 'http://127.0.0.1:18090'
const root = fileURLToPath(new URL('../../../../internal/web/dist/uniapp/', import.meta.url))
const evidence = fileURLToPath(new URL('../../../../.scratch/ticket10/', import.meta.url))
let fixture
const deadline = Date.now() + 90000
for (;;) {
  try { fixture = await (await fetch(upstream + '/__fixture')).json(); if (fixture.fixture === 'ticket10-isolated-store') break } catch {}
  if (Date.now() > deadline) throw new Error('Isolated Go history fixture not ready')
  await new Promise(resolve => setTimeout(resolve, 500))
}
const requests = []
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  if (url.pathname.startsWith('/api/')) {
    let body = ''; for await (const chunk of req) body += chunk
    const headers = { 'Content-Type': 'application/json' }
    for (const name of ['authorization', 'x-fastiot-project']) if (req.headers[name]) headers[name] = req.headers[name]
    requests.push({ path: req.url, project: headers['x-fastiot-project'] })
    try {
      const result = await fetch(upstream + req.url, { method: req.method, headers, body: ['GET', 'HEAD'].includes(req.method) ? undefined : body })
      res.writeHead(result.status, { 'Content-Type': result.headers.get('content-type') || 'application/json' }); res.end(Buffer.from(await result.arrayBuffer()))
    } catch { res.writeHead(502); res.end('{"error":"fixture_unavailable"}') }
    return
  }
  const candidate = path.resolve(root, url.pathname.replace(/^\/uniapp\/?/, '') || 'index.html')
  if (!candidate.startsWith(root)) { res.writeHead(404); return res.end() }
  try { const data = await readFile(candidate); res.writeHead(200, { 'Content-Type': { '.js': 'application/javascript', '.css': 'text/css', '.html': 'text/html' }[path.extname(candidate)] || 'application/octet-stream' }); res.end(data) }
  catch { res.writeHead(404); res.end() }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const base = `http://127.0.0.1:${server.address().port}/uniapp/`
let browser
try {
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, acceptDownloads: true })
  page.setDefaultTimeout(15000)
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  const button = text => page.locator('uni-button').filter({ hasText: new RegExp(`^${text}$`) })
  await page.goto(base + '#/pages/login/index')
  await page.locator('.login input[type="text"]').fill('history-viewer@fixture.local')
  await page.locator('.login input[type="password"]').fill('history-browser-test-only')
  await page.keyboard.press('Enter')
  await page.locator('.project-button').waitFor()
  await page.goto(base + `#/pages/history/index?point=${fixture.point}`)
  await page.locator('.hist-sub').filter({ hasText: '运行历史' }).waitFor()
  await button('自动').click()
  await page.locator('.hist-sub').filter({ hasText: '原始观测' }).waitFor()
  assert.match(await page.locator('.hist-sub').innerText(), /实际.*原始观测.*保留每点观测\/事件各12000条/)
  assert.equal(await page.locator('select option').filter({ hasText: 'Private point' }).count(), 0)
  const table = await page.locator('.table-wrap').innerText()
  for (const text of ['过期', '无效', '质量事件', 'connection_lost', '接收时间', '迟到观测']) assert.ok(table.includes(text), text)
  assert.equal(await page.locator('.table-wrap tbody tr').count(), 8)
  await button('曲线').click()
  await page.locator('.trend-svg').waitFor()
  assert.equal(await page.locator('.series-line:visible').count(), 1)
  await page.locator('.trend-svg').focus(); await page.keyboard.press('Home')
  assert.match(await page.locator('.readout').innerText(), /0\s*MPa/)
  await page.keyboard.press('End'); assert.match(await page.locator('.readout').innerText(), /20\s*MPa/)
  await mkdir(evidence, { recursive: true })
  await page.screenshot({ path: path.join(evidence, 'history-desktop.png'), fullPage: true })
  await button('1秒').click()
  await page.locator('.hist-sub').filter({ hasText: '窗口平均' }).waitFor()
  assert.match(await page.locator('.hist-sub').innerText(), /请求1秒.*实际1秒/)
  await button('数据表').click()
  assert.match(await page.locator('.table-wrap').innerText(), /过期/)
  assert.match(await page.locator('.table-wrap').innerText(), /无效/)
  const downloadPromise = page.waitForEvent('download')
  await button('导出 CSV').click()
  const download = await downloadPromise
  const csv = await readFile(await download.path(), 'utf8')
  assert.match(csv, /quality_event/); assert.match(csv, /stale/); assert.match(csv, /invalid/)
  assert.match(csv, /观测时间,接收时间/)
  assert.match(csv, /device,true,1000,count_per_point_per_kind,12000/)
  const exportRequest = requests.filter(item => item.path.startsWith('/api/history/export?')).at(-1)
  const queryRequest = requests.filter(item => item.path.startsWith('/api/history?')).at(-1)
  assert.equal(exportRequest.path.replace('/export', ''), queryRequest.path)
  assert.equal(exportRequest.project, queryRequest.project)
  assert.ok(exportRequest.project)
  await page.setViewportSize({ width: 375, height: 812 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
  await page.screenshot({ path: path.join(evidence, 'history-mobile.png'), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.locator('select[aria-label="历史点位"]').selectOption(fixture.many)
  await button('自动').click(); await button('3天').click()
  await page.locator('.hist-foot').filter({ hasText: '匹配 10001 条' }).waitFor()
  await button('更早记录').click()
  await page.locator('.hist-foot').filter({ hasText: '第 2 页' }).waitFor()
  assert.match(requests.filter(item => item.path.startsWith('/api/history?')).at(-1).path, /before_count=/)
  await button('导出 CSV').click()
  await page.getByRole('alert').filter({ hasText: '导出超过上限' }).waitFor()
  assert.ok(!requests.filter(item => item.path.startsWith('/api/history/export?')).at(-1).path.includes('before='))
  await page.goto(base + `#/pages/history/index?point=${fixture.hidden}`)
  await page.reload()
  await page.getByRole('alert').filter({ hasText: '历史查询失败' }).waitFor()
  assert.equal(await page.locator('.table-wrap tbody tr').count(), 0)
  // Direct HTTP also verifies export authorization and foreign project rejection.
  const response = await page.evaluate(async ({ hidden }) => {
    const token = localStorage.getItem('fastems-token')
    const directory = await (await fetch('/api/projects', { headers: { Authorization: `Bearer ${token}` } })).json()
    const headers = { Authorization: `Bearer ${token}`, 'X-Fastiot-Project': directory.projects[0].id }
    const denied = await fetch(`/api/history/export?node_id=${hidden}`, { headers })
    const foreign = await fetch(`/api/history?node_id=${hidden}`, { headers: { ...headers, 'X-Fastiot-Project': 'foreign' } })
    return [denied.status, foreign.status]
  }, fixture)
  assert.deepEqual(response, [403, 409])
  assert.deepEqual(errors, [])
  console.log('PASS real Go: scoped viewer, actual intervals/retention, stale/invalid/events/gaps, bounded same-window CSV, pagination, desktop/mobile, foreign object/project rejection')
} finally {
  await browser?.close()
  await new Promise(resolve => server.close(resolve))
  await fetch(upstream + '/__finish', { method: 'POST' })
}
