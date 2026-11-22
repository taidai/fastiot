import { chromium } from 'playwright-core'

const file = 'file:///C:/Users/chent/Desktop/2026%E6%88%90%E6%9E%9C/AAAAA%E5%BC%80%E6%BA%90/easyems/frontend/prototype/overview-hd/%E8%AE%BE%E8%AE%A1%20%C2%B7%20%E7%8E%BB%E7%92%83%E7%A3%A8%E7%A0%82.html'
const browser = await chromium.launch({ channel: 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })
const errors = []
page.on('pageerror', e => errors.push(String(e)))
await page.goto(file)
const ok = [], fail = []
async function step(name, fn) {
  try { await fn(); ok.push(name) } catch (e) { fail.push(name + ': ' + e.message.split('\n')[0]) }
}
async function cfgRow(text) { return page.locator('.cfg-row', { hasText: text }) }

await step('打开指标组绑定', async () => {
  await page.evaluate(() => { state.workbench = 'device'; state.page = 'ledger'; state.step = 'ui'; state.selD = 'area-pump'; state.cfgDraft = null; state.bindBlock = null; render() })
  await page.waitForTimeout(100)
  await (await cfgRow('指标组')).getByText('配置').click()
  await page.waitForTimeout(100)
  const drawer = await page.textContent('.cfg-list')
  if (!drawer.includes('指标（4–6 个）')) throw new Error('面板未打开')
  const checked = await page.$$eval('.cfg-list .bind-opt input', els => els.filter(e => e.checked).length)
  if (checked !== 6) throw new Error('默认勾选 ' + checked)
})

await step('取消一个指标后预览为 5 格', async () => {
  await page.locator('.bind-opt', { hasText: '通信成功率' }).locator('input').click()
  await page.waitForTimeout(100)
  const count = await page.$$eval('.cfg-frame .kpi', els => els.length)
  if (count !== 5) throw new Error('预览指标数 ' + count)
  await page.click('.bind-head button')
})

await step('最近数据列与行数', async () => {
  await (await cfgRow('最近数据')).getByText('配置').click()
  await page.waitForTimeout(100)
  await page.locator('.bind-opt', { hasText: '电流 A' }).locator('input').click()
  await page.locator('.cfg-list').getByText('3 行').click()
  await page.waitForTimeout(100)
  const preview = await page.textContent('.cfg-frame')
  if (preview.includes('电流 A')) throw new Error('预览仍显示电流列')
  const rows = await page.evaluate(() => {
    const card = [...document.querySelectorAll('.cfg-frame .screen-card')].find(c => c.textContent.includes('最近数据'))
    return card ? card.querySelectorAll('tbody tr').length : -1
  })
  if (rows !== 3) throw new Error('预览行数 ' + rows)
  await page.click('.bind-head button')
})

await step('趋势序列', async () => {
  await (await cfgRow('趋势')).getByText('配置').click()
  await page.waitForTimeout(100)
  await page.locator('.cfg-list .chips button', { hasText: '目标压力' }).click()
  await page.waitForTimeout(100)
  const preview = await page.textContent('.cfg-frame')
  if (!preview.includes('趋势 · 目标压力')) throw new Error('趋势未切换')
  await page.click('.bind-head button')
})

await step('设备清单列', async () => {
  await (await cfgRow('设备清单')).getByText('配置').click()
  await page.waitForTimeout(100)
  await page.locator('.bind-opt', { hasText: '更新' }).locator('input').click()
  await page.waitForTimeout(100)
  const heads = await page.$$eval('.cfg-frame .screen-card table th', els => els.map(e => e.textContent.trim()))
  if (heads.includes('更新')) throw new Error('预览仍有更新列')
  await page.click('.bind-head button')
})

await step('自动块只读说明', async () => {
  await (await cfgRow('告警')).getByText('配置').click()
  await page.waitForTimeout(100)
  const drawer = await page.textContent('.cfg-list')
  if (!drawer.includes('自动绑定')) throw new Error('缺少自动说明')
  if (await page.$$eval('.cfg-list .bind-opt input', els => els.length)) throw new Error('自动块不应有输入')
  await page.click('.bind-head button')
})

await step('保存后客户视图按绑定渲染', async () => {
  await page.click('text=保存本层')
  await page.waitForTimeout(100)
  await page.evaluate(() => { state.workbench = 'customer'; state.custLayer = 'area-pump'; render() })
  await page.waitForTimeout(100)
  const kpis = await page.$$eval('.kpi', els => els.length)
  if (kpis !== 5) throw new Error('客户 KPI ' + kpis)
  const canvas = await page.textContent('.cust-canvas')
  if (canvas.includes('电流 A')) throw new Error('客户表仍有电流列')
  if (!canvas.includes('趋势 · 目标压力')) throw new Error('客户趋势未变')
})

console.log('PASS: ' + ok.join(' | '))
if (fail.length) console.log('FAIL: ' + fail.join(' | '))
if (errors.length) console.log('ERRORS: ' + errors.join(' | '))
process.exit(fail.length ? 1 : 0)
