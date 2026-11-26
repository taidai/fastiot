// Prototype-parity guard. Runs against a real backend seeded by
// `cmd/pumphouse -project` (see scripts/prototype-parity.sh). It asserts the
// customer overview contract that used to be locked by the deleted preview
// tests: six ledger layers, hero, three rail blocks, four daily stats with 24
// hourly bars, the history drawer, and the device access labels.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'
import { deviceAccessLabel, deviceAccessState } from '../../components/design/access.ts'

const origin = (process.env.PARITY_ORIGIN || 'http://127.0.0.1:18405').replace(/\/$/, '')
const password = process.env.PARITY_PASSWORD || 'pumphouse-demo-2026'
const evidence = process.env.PARITY_EVIDENCE || ''
const expectedLayers = ['教学泵房', '泵房', '1号泵', '2号泵', '水箱', '供水单元']
const failures = []
const checks = []
function pass(name, detail) { checks.push({ name, detail }); console.log(`PASS ${name}${detail ? ` — ${detail}` : ''}`) }
function fail(name, detail) { failures.push({ name, detail }); console.error(`FAIL ${name}${detail ? ` — ${detail}` : ''}`) }

const deadline = Date.now() + 120000
for (;;) {
  try { if ((await fetch(origin + '/api/health')).ok) break } catch {}
  if (Date.now() > deadline) throw new Error('pumphouse backend did not become healthy')
  await new Promise(resolve => setTimeout(resolve, 500))
}

let browser
let page
try {
  try { browser = await chromium.launch({ channel: 'chrome', headless: true, timeout: 20000 }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true, timeout: 20000 }) }
  page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  page.setDefaultTimeout(30000)
  const pageErrors = []
  page.on('pageerror', error => pageErrors.push(error.message))

  await page.goto(`${origin}/uniapp/#/pages/login/index`, { waitUntil: 'domcontentloaded' })
  await page.locator('.login input[type="text"]').fill('admin')
  await page.locator('.login input[type="password"]').fill(password)
  await page.keyboard.press('Enter')
  await page.locator('.hero h1').waitFor({ timeout: 120000 })
  // pumphouse answers /api/health before it finishes seeding. Wait until the
  // seeded screen itself is readable: the site hero then carries its four
  // configured metrics instead of the fallback block. The overview reads the
  // device ledger only when the snapshot first arrives, so reload while waiting.
  const seedDeadline = Date.now() + 120000
  let seeded = false
  while (Date.now() < seedDeadline) {
    if (await page.locator('.ledger .node').filter({ hasText: '水箱' }).count() === 1) {
      await page.locator('.ledger .node').filter({ hasText: '教学泵房' }).first().click()
      await page.waitForFunction(() => document.querySelector('.hero h1')?.textContent === '教学泵房', null, { timeout: 10000 }).catch(() => {})
      if (await page.locator('.hero .metrics .metric').count() === 4) { seeded = true; break }
    }
    await page.waitForTimeout(2000)
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.locator('.ledger .node').first().waitFor({ timeout: 60000 })
  }
  assert.ok(seeded, 'pumphouse teaching project did not finish seeding')
  await page.locator('.ledger .node').filter({ hasText: '教学泵房' }).first().waitFor({ timeout: 120000 })

  // 1. Layer navigation: six layers, in order and by name.
  const rowText = await page.locator('.ledger .node').evaluateAll(rows => rows.map(row => row.innerText.replace(/\s+/g, ' ').trim()))
  const named = rowText.map(text => expectedLayers.find(name => text.includes(name))).filter(Boolean)
  assert.deepEqual(named, expectedLayers, `ledger layers out of order: ${JSON.stringify(rowText)}`)
  const extras = rowText.filter(text => !expectedLayers.some(name => text.includes(name)))
  // The instance root is the project row; it is not one of the six layers.
  assert.ok(extras.length <= 1 && extras.every(text => /项目/.test(text)), `unexpected ledger rows: ${JSON.stringify(extras)}`)
  pass('layer navigation', `${expectedLayers.join(' / ')}${extras.length ? ` (+ ${extras[0]})` : ''}`)

  await page.locator('.ledger .node').filter({ hasText: '教学泵房' }).first().click()
  await page.waitForFunction(() => document.querySelector('.hero h1')?.textContent === '教学泵房')

  // 2. Hero: breadcrumb, title, online N/N, metric group with a real count.
  const crumb = (await page.locator('.hero .crumb').innerText()).trim()
  assert.ok(crumb && crumb.includes('教学泵房'), `breadcrumb missing the current layer: ${JSON.stringify(crumb)}`)
  assert.equal((await page.locator('.hero h1').innerText()).trim(), '教学泵房')
  const online = (await page.locator('.hero .online').innerText()).trim()
  assert.match(online, /^在线 \d+ \/ \d+$/, `hero online text: ${JSON.stringify(online)}`)
  const metricCount = await page.locator('.hero .metrics .metric').count()
  assert.ok(metricCount > 0, 'hero metric group is empty')
  pass('hero', `crumb=${JSON.stringify(crumb)}, title=教学泵房, ${online}, ${metricCount} metrics`)

  // 3. Right rail: pending -> today execution -> handover.
  const railText = await page.locator('.side .rail-block').evaluateAll(blocks => blocks.map(block => block.innerText.replace(/\s+/g, ' ').trim()))
  assert.equal(railText.length, 3, `expected three rail blocks, got ${JSON.stringify(railText)}`)
  assert.match(railText[0], /^待处理/, `rail 1: ${JSON.stringify(railText[0])}`)
  assert.match(railText[1], /^今日执行/, `rail 2: ${JSON.stringify(railText[1])}`)
  assert.match(railText[2], /^交接/, `rail 3: ${JSON.stringify(railText[2])}`)
  pass('right rail', '待处理 → 今日执行 → 交接')

  // 4. Daily stats: four metrics with fixed units and 24 hourly bars.
  const dailyKeys = ['energy', 'runtime', 'communication', 'freshness']
  assert.equal(await page.locator('.daily .dfig').count(), 4, 'daily stats must keep four metrics')
  for (const key of dailyKeys) assert.equal(await page.locator(`.daily .dfig[data-daily-stat="${key}"]`).count(), 1, `missing daily stat ${key}`)
  const unitText = await page.locator('.daily .dfig b small').evaluateAll(nodes => nodes.map(node => node.textContent.trim()))
  assert.deepEqual(unitText, ['kWh', 'h', '%', '秒'], `daily units: ${JSON.stringify(unitText)}`)
  const dailyValues = await page.locator('.daily .dfig > b').evaluateAll(nodes => nodes.map(node => node.textContent.trim()))
  assert.ok(dailyValues.every(value => value.length > 0), `daily values must render: ${JSON.stringify(dailyValues)}`)
  await page.waitForFunction(() => document.querySelectorAll('.daily .hour-bar').length === 24, null, { timeout: 30000 })
  assert.equal(await page.locator('.daily .hour-bar').count(), 24, 'the 24-hour strip must keep 24 buckets')
  assert.ok(await page.locator('.daily .hour-bar.has-data').count() > 0, 'the 24-hour strip must carry real history')
  pass('daily stats', `${dailyValues.join(' / ')} · ${unitText.join(' / ')} · 24 bars`)

  // 5. History drawer: open from a layer entry, switch view, close back to layer.
  await page.locator('.daily .hour-bar').first().click()
  const drawer = page.locator('.history-drawer')
  await drawer.waitFor()
  assert.equal(await drawer.getAttribute('aria-label'), '历史数据')
  const historyTitle = (await drawer.locator('.hist-head h1').innerText()).trim()
  assert.ok(historyTitle, 'history drawer must name the selected point')
  const groups = drawer.locator('.hist-bar .chips')
  assert.equal(await groups.count(), 3, 'history bar must keep range, granularity and view groups')
  const ranges = await groups.nth(0).locator('uni-button').evaluateAll(nodes => nodes.map(node => node.textContent.trim()))
  assert.deepEqual(ranges, ['15分钟', '1小时', '24小时', '3天', '7天'], `history ranges: ${JSON.stringify(ranges)}`)
  const granules = await groups.nth(1).locator('uni-button').evaluateAll(nodes => nodes.map(node => node.textContent.trim()))
  assert.deepEqual(granules, ['自动', '1秒', '5秒', '30秒', '1分钟'], `history granularities: ${JSON.stringify(granules)}`)
  const views = await groups.nth(2).locator('uni-button').evaluateAll(nodes => nodes.map(node => node.textContent.trim()))
  assert.deepEqual(views, ['数据表', '曲线'], `history views: ${JSON.stringify(views)}`)
  // A freshly seeded backend may hold only its first observation, so query the
  // raw 24-hour range instead of a granular bucket that may still be open.
  await groups.nth(0).getByText('24小时', { exact: true }).click()
  await groups.nth(1).getByText('自动', { exact: true }).click()
  await drawer.locator('.table-wrap table').waitFor()
  await page.waitForFunction(() => document.querySelectorAll('.history-drawer tbody tr').length > 0, null, { timeout: 30000 })
  await groups.nth(2).getByText('曲线', { exact: true }).click()
  await drawer.locator('.trend').waitFor()
  await drawer.locator('.trend-svg').waitFor({ timeout: 30000 })
  await groups.nth(2).getByText('数据表', { exact: true }).click()
  await drawer.locator('.table-wrap table').waitFor()
  assert.equal(await drawer.locator('.trend').count(), 0, 'table view must replace the chart')
  await drawer.getByText('← 返回', { exact: true }).click()
  await drawer.waitFor({ state: 'detached' })
  await page.waitForFunction(() => document.querySelector('.hero h1')?.textContent === '教学泵房')
  assert.equal((await page.locator('.ledger .node.active').innerText()).includes('教学泵房'), true, 'closing history must return to the layer')
  pass('history drawer', `${historyTitle} · 范围/粒度/数据表↔曲线 · 关闭回到层`)

  // 6. Device access labels: bound is not the same as connected.
  assert.equal(deviceAccessLabel(deviceAccessState({ bound: false })), '未绑定')
  assert.equal(deviceAccessLabel(deviceAccessState({ bound: true, verified: false })), '已绑定待验证')
  assert.equal(deviceAccessLabel(deviceAccessState({ bound: true, verified: true })), '已接入')
  assert.notEqual(deviceAccessLabel(deviceAccessState({ bound: true, verified: false })), '已接入', 'a bound device without verification must not read 已接入')
  const ledgerText = await page.locator('.ledger').innerText()
  assert.match(ledgerText, /仅显示已绑定设备/, 'overview ledger must call its devices bound, not connected')
  assert.doesNotMatch(ledgerText, /已接入/, 'overview ledger must not label merely bound devices as 已接入')
  pass('device access labels', '未绑定 / 已绑定待验证 / 已接入; overview ledger keeps 已绑定')

  if (evidence) await page.screenshot({ path: evidence, fullPage: true })
  assert.deepEqual(pageErrors, [], `page errors: ${pageErrors.join('\n')}`)
  console.log(JSON.stringify({ passed: true, origin, checks, evidence }, null, 2))
} catch (error) {
  fail('parity guard', error.message)
  if (page) console.error('visible state:', (await page.locator('body').innerText()).slice(0, 3000))
  if (failures.length) console.error(JSON.stringify({ passed: false, failures }, null, 2))
  throw error
} finally {
  await browser?.close()
}
