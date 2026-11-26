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

// The guard seeds one station through the same HTTP API a delivery engineer
// uses, so it can prove a bound-but-not-L2-verified device never reads 已接入.
async function adminToken() {
  const response = await fetch(origin + '/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'admin', password }) })
  assert.ok(response.ok, `admin login failed: ${response.status}`)
  const body = await response.json()
  assert.ok(body.token, 'admin login returned no token')
  return body.token
}
async function apiCall(token, path, init = {}) {
  const response = await fetch(origin + '/api' + path, {
    ...init,
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}`, ...(init.headers || {}) },
  })
  const text = await response.text()
  if (!response.ok) throw new Error(`${init.method || 'GET'} ${path}: ${response.status} ${text}`)
  return text ? JSON.parse(text) : null
}

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
      // The seeded screen carries four configured metrics; the hero fills up to
      // six with automatic ones, so "at least four" is the seeding signal.
      if (await page.locator('.hero .metrics .metric').count() >= 4) { seeded = true; break }
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
  // The project root is not a customer layer: the ledger must show exactly the
  // six prototype layers.
  assert.deepEqual(extras, [], `the project root must not appear as a ledger layer: ${JSON.stringify(extras)}`)
  pass('layer navigation', expectedLayers.join(' / '))

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
  // Read raw samples in the default window: an unclosed 24-hour bucket can be
  // empty for the curve even while the table still shows the previous query.
  await groups.nth(0).getByText('15分钟', { exact: true }).click()
  await groups.nth(1).getByText('1秒', { exact: true }).click()
  await drawer.locator('.table-wrap table').waitFor()
  // A freshly seeded backend can still be filling the raw window; fall back to
  // the wider range instead of weakening the assertion that rows must exist.
  const hasRows = () => page.waitForFunction(() => document.querySelectorAll('.history-drawer tbody tr').length > 0, null, { timeout: 15000 })
  try {
    await hasRows()
  } catch {
    await groups.nth(0).getByText('24小时', { exact: true }).click()
    await groups.nth(1).getByText('自动', { exact: true }).click()
    await hasRows()
  }
  // A just-seeded backend may hold a single observation; the curve only draws
  // once there is real history, so wait for a few rows before switching views.
  await page.waitForFunction(() => document.querySelectorAll('.history-drawer tbody tr').length >= 3, null, { timeout: 30000 })
  await groups.nth(2).getByText('曲线', { exact: true }).click()
  await drawer.locator('.trend').waitFor()
  // A fresh seed may hold only invalid/expired samples, in which case the curve
  // legitimately shows its quality note instead of drawing. Either outcome is
  // acceptable; silently missing BOTH would not be.
  let curveNote = 'chart'
  try {
    await drawer.locator('.trend-svg').waitFor({ timeout: 30000 })
  } catch {
    const trendText = await drawer.locator('.trend').innerText()
    assert.match(trendText, /无效|过期|单点|质量|观测/, `curve view shows neither a chart nor its quality note: ${trendText.slice(0, 120)}`)
    curveNote = '质量说明（播种样本尚未有效）'
  }
  await groups.nth(2).getByText('数据表', { exact: true }).click()
  await drawer.locator('.table-wrap table').waitFor()
  assert.equal(await drawer.locator('.trend').count(), 0, 'table view must replace the chart')
  await drawer.getByText('← 返回', { exact: true }).click()
  await drawer.waitFor({ state: 'detached' })
  await page.waitForFunction(() => document.querySelector('.hero h1')?.textContent === '教学泵房')
  assert.equal((await page.locator('.ledger .node.active').innerText()).includes('教学泵房'), true, 'closing history must return to the layer')
  pass('history drawer', `${historyTitle} · 范围/粒度/数据表↔曲线(${curveNote}) · 关闭回到层`)

  // 6. Device access labels: bound is not the same as connected.
  assert.equal(deviceAccessLabel(deviceAccessState({ bound: false })), '未绑定')
  assert.equal(deviceAccessLabel(deviceAccessState({ bound: true, verified: false })), '已绑定待验证')
  assert.equal(deviceAccessLabel(deviceAccessState({ bound: true, verified: true })), '已绑定待验证', 'a manual sign-off alone must not read 已接入')
  assert.notEqual(deviceAccessLabel(deviceAccessState({ bound: true, verified: false })), '已接入', 'a bound device without verification must not read 已接入')
  const ledgerText = await page.locator('.ledger').innerText()
  assert.match(ledgerText, /仅显示已绑定设备/, 'overview ledger must call its devices bound, not connected')
  assert.doesNotMatch(ledgerText, /已接入/, 'overview ledger must not label merely bound devices as 已接入')
  pass('device access labels', '未绑定 / 已绑定待验证 / 已接入; overview ledger keeps 已绑定')

  // 7. Design workbench access step renders the same verdicts from real ledger
  // data. Make one seeded station ledger-bound while keeping only the manual
  // sign-off: it must render 已绑定待验证, never 已接入.
  const token = await adminToken()
  const api = (path, init = {}) => apiCall(token, path, init)
  const ledgerBefore = await api('/device-ledger')
  const target = (ledgerBefore.devices || []).find(device => device.profile_id && device.business_node_id)
  assert.ok(target, 'seeded ledger must carry a bound business device')
  const unitPath = `/modbus/${encodeURIComponent(target.channel_id)}/units/${target.server_id}/model`
  const unit = await api(unitPath)
  assert.ok(unit.model && unit.profiles?.length, `unit model missing for ${target.channel_id}/${target.server_id}`)
  const pointByName = new Map((unit.points || []).filter(point => point.server_id === target.server_id).map(point => [point.name, point.id]))
  const profile = (unit.profiles || []).find(item => item.id === unit.model.profile_id) || (unit.profiles || [])[0]
  const pointMap = {}
  for (const spec of profile?.point_specs || []) if (pointByName.has(spec.name)) pointMap[spec.name] = pointByName.get(spec.name)
  const current = await api(unitPath)
  const baseModel = { name: unit.model.name, profile_id: unit.model.profile_id, point_map: pointMap, business_node_id: unit.model.business_node_id }
  // 改映射会改变映射指纹；已存在的模型必须回传当前版本号，否则 409 mapping_version_conflict。
  const versions = model => (model?.mapping_version ? { mapping_version: model.mapping_version, profile_version: model.profile_version } : {})
  await api(unitPath, { method: 'PUT', body: JSON.stringify({ ...baseModel, ...versions(current.model) }) })
  const afterMap = await api(unitPath)
  await api(unitPath, { method: 'PUT', body: JSON.stringify({
    ...baseModel, mapping_version: afterMap.model?.mapping_version, profile_version: afterMap.model?.profile_version,
    verified: true, verifier: 'parity-guard', verified_at: new Date().toISOString().slice(0, 10),
    device_serial: 'PARITY-GUARD', verification: 'parity guard: bound but not L2 verified',
  }) })
  const ledgerAfter = await api('/device-ledger')
  const bound = (ledgerAfter.devices || []).find(device => device.channel_id === target.channel_id && device.server_id === target.server_id)
  assert.equal(bound?.bound, true, 'the seeded station must be ledger-bound after mapping every point')
  assert.equal(bound?.verified, true, 'the manual sign-off must stay independent of L2 evidence')
  assert.ok(bound.required_points > 0 && bound.verified_points === 0, `unexpected L2 facts: ${JSON.stringify(bound)}`)
  assert.equal(deviceAccessState(bound), 'pending', 'a bound, manually signed-off device without L2 evidence is pending')
  for (const device of ledgerAfter.devices || []) {
    const state = deviceAccessState(device)
    if (state === 'access') assert.ok(device.required_points > 0 && device.verified_points >= device.required_points, `no L2 evidence device read 已接入: ${JSON.stringify(device)}`)
  }

  // The seeded project binds all three devices; add one unbound device so the
  // access step must distinguish all three states on real data.
  const snapshot = await api('/snapshot')
  const area = (snapshot.nodes || []).find(node => node.type === 'area')
  assert.ok(area, 'seeded snapshot must carry an area to hold the unbound device')
  await api('/nodes', { method: 'POST', body: JSON.stringify({ parent: area.id, type: 'device', description: '待绑定设备（守卫）' }) })

  await page.goto(`${origin}/uniapp/#/pages/design/index`, { waitUntil: 'domcontentloaded' })
  await page.locator('.design-ledger .node').first().waitFor({ timeout: 120000 })
  // ADR 0007: an existing project opens on the first incomplete step. The seed
  // has 搭台账 done and 接设备 pending, so the active step must be 接设备 — not
  // the default 新建现场.
  await page.waitForFunction(
    () => document.querySelector('.stepper .step.active')?.textContent?.includes('接设备'),
    null, { timeout: 60000 },
  )
  const openedStep = (await page.locator('.stepper .step.active').innerText()).replace(/\s+/g, '')
  assert.match(openedStep, /接设备/, `design must open on the first incomplete step, got ${openedStep}`)
  const siteRow = page.locator('.design-ledger .node').filter({ hasText: '教学泵房' }).first()
  await siteRow.waitFor({ timeout: 60000 })
  await siteRow.click()
  const accessStep = page.locator('.stepper .step').filter({ hasText: '接设备' })
  await accessStep.click()
  await page.locator('.access-step .ledger-panel .ledger-table tbody tr').first().waitFor({ timeout: 60000 })
  const chips = await page.locator('.access-step .ledger-panel .ledger-table tbody tr').evaluateAll(rows => rows.map(row => row.querySelector('.chip')?.textContent?.trim() || ''))
  const allowed = new Set(['未绑定', '已绑定待验证', '已接入'])
  assert.ok(chips.length >= 3, `access step must list the seeded devices: ${JSON.stringify(chips)}`)
  assert.ok(chips.every(chip => allowed.has(chip)), `access step must use the three-state vocabulary: ${JSON.stringify(chips)}`)
  assert.ok(chips.includes('未绑定'), 'an unbound device must read 未绑定')
  assert.ok(chips.includes('已绑定待验证'), 'a bound, manually signed-off device must read 已绑定待验证')
  assert.ok(!chips.includes('已接入'), 'a device without full L2 evidence must never read 已接入')
  const accessSummary = await page.locator('.access-step .step-head').innerText()
  assert.match(accessSummary, /已接入 0 \/ \d+/, `access summary must count zero connected: ${JSON.stringify(accessSummary)}`)
  assert.match(accessSummary, /台待验证/, `access summary must surface pending devices: ${JSON.stringify(accessSummary)}`)
  pass('design access three states', `${chips.join(' / ')} · ${accessSummary.replace(/\s+/g, ' ').trim()}`)

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
