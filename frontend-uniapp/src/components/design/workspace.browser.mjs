import assert from 'node:assert/strict'
import { chromium } from '../../../../frontend/node_modules/playwright-core/index.mjs'
const base = process.env.PREVIEW_ORIGIN || 'http://127.0.0.1:5174/uniapp/'
const browser = await chromium.launch({ channel: 'msedge', headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  const requests = []
  const errors = []
  page.on('request', r => { if (new URL(r.url()).pathname.startsWith('/api/')) requests.push(r.url()) })
  page.on('pageerror', e => errors.push(e.message))
  await page.goto(`${base}#/pages/design/index`)
  await page.locator('.project-button').waitFor()
  assert.equal(await page.locator('.ems-toggle').evaluate(e => getComputedStyle(e).marginRight), '0px', 'uni-button default auto margins must not displace prototype header')
  assert.match(await page.locator('.page').evaluate(e => getComputedStyle(e).backgroundImage), /linear-gradient/, 'design has its own prototype backdrop, not the overview grid')
  await page.locator('.project-button').click()
  await page.locator('.project-menu uni-button').filter({ hasText: '储能站 A' }).click()
  await page.locator('.design-ledger').getByText('PCS1', { exact: true }).waitFor()
  await page.locator('.ems-toggle').click()
  await page.getByText('项目依赖未满足', { exact: true }).waitFor()
  await page.locator('.dependency-gate uni-button').click()
  await page.locator('.design-ledger').getByText('PCS1', { exact: true }).waitFor()
  await page.locator('.workbench uni-button').filter({ hasText: '总览' }).click()
  await page.locator('.preview-page .hero h1').filter({ hasText: '储能站 A' }).waitFor()
  assert.match(await page.locator('.energy-flow').innerText(), /并网点 PCC/)
  assert.equal(await page.locator('.device-table').getByText('1号泵', { exact: true }).count(), 0)
  await page.locator('.metric').first().click()
  await page.locator('.hist.open').waitFor()
  assert.match(await page.locator('.hist-table thead').innerText(), /光伏 kW[\s\S]*SOC %/)
  assert.doesNotMatch(await page.locator('.hist-table thead').innerText(), /出口压力/)
  await page.locator('.hist-back').click()
  await page.locator('.workbench uni-button').filter({ hasText: '设计' }).click()
  await page.locator('.design-ledger').getByText('PCS1', { exact: true }).waitFor()
  await page.locator('.project-button').click()
  await page.locator('.project-menu uni-button').filter({ hasText: '批量接入示例' }).click()
  await page.locator('.design-ledger').getByText('PCS12', { exact: true }).waitFor()
  await page.locator('.workbench uni-button').filter({ hasText: '交付' }).click()
  await page.waitForFunction(() => document.querySelector('.project-button')?.textContent.includes('批量接入示例'))
  await page.locator('.workbench uni-button').filter({ hasText: '设计' }).click()
  await page.locator('.design-ledger').getByText('PCS12', { exact: true }).waitFor()
  await page.locator('.project-button').click()
  await page.locator('.project-menu uni-button').filter({ hasText: '新建项目' }).click()
  await page.locator('.step-head').getByText('第 1 步 · 建现场', { exact: true }).waitFor()
  assert.match(await page.locator('.project-button').innerText(), /未命名项目/)
  await page.locator('.project-button').click()
  await page.locator('.project-menu uni-button').filter({ hasText: '教学泵房' }).click()
  const groups = [['建现场',['模板库']], ['接设备',['通道','点位表','型号库','通信健康与报文','设备验证']], ['配策略',['聚合计算','计算点','虚拟输入','报警类型']], ['交付',['数据保留']]]
  for (const [step, tools] of groups) {
    await page.locator('.stepper uni-button').filter({ hasText: step }).click()
    for (const tool of tools) {
      await page.locator('.tool-links uni-button, .tool-line uni-button').filter({ hasText: new RegExp(`^${tool}(?: →)?$`) }).click()
      await page.locator('.prototype-tools .variant-head b').filter({ hasText: tool }).waitFor()
      if (tool !== '聚合计算') assert.match(await page.locator('.prototype-tools').innerText(), /这一页只验证位置，不实现内容/)
      await page.locator('.prototype-tools uni-button').filter({ hasText: '回台账树' }).click()
      await page.locator('.stepper').waitFor()
    }
  }
  assert.deepEqual(requests, [], 'preview must never call real API')
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ ok: true, checks: ['projects', 'EMS gate', '24-device batch', 'workbench roundtrip', 'new project', 'no live API'] }))
} finally { await browser.close() }
