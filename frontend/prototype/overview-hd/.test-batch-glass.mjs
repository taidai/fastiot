import { chromium } from 'playwright-core'

const file = 'file:///C:/Users/chent/Desktop/2026%E6%88%90%E6%9E%9C/AAAAA%E5%BC%80%E6%BA%90/easyems/frontend/prototype/overview-hd/%E8%AE%BE%E8%AE%A1%20%C2%B7%20%E7%8E%BB%E7%92%83%E7%A3%A8%E7%A0%82.html'
const browser = await chromium.launch({ channel: 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1600, height: 1100 } })
const errors = []
page.on('pageerror', e => errors.push(String(e)))
await page.goto(file)
const ok = [], fail = []
async function step(name, fn) {
  try { await fn(); ok.push(name) } catch (e) { fail.push(name + ': ' + e.message.split('\n')[0]) }
}
async function go(step, sel) {
  await page.evaluate(([s, d]) => { state.workbench = 'device'; state.page = 'ledger'; state.step = s; state.selD = d; render() }, [step, sel])
  await page.waitForTimeout(120)
}

await step('切到批量接入示例', async () => {
  await page.evaluate(() => setProject('批量接入示例'))
  await page.waitForTimeout(120)
  if (!(await page.textContent('#projectName')).includes('批量接入示例')) throw new Error('项目未切换')
  await go('tree', 'site-farm')
  const rows = await page.$$eval('.ledger-table tbody tr', els => els.length)
  if (rows < 26) throw new Error('台账行数 ' + rows)
})

await step('按站号搜索', async () => {
  await page.fill('.tree-search input', '站号 44')
  await page.waitForTimeout(120)
  const text = await page.textContent('.ledger-table')
  if (!text.includes('BMS4')) throw new Error('未命中 BMS4')
  if (text.includes('BMS5')) throw new Error('多命中')
})

await step('按点位搜索', async () => {
  await page.fill('.tree-search input', 'SOC')
  await page.waitForTimeout(120)
  const reasons = await page.$$eval('.ledger-table tbody tr', els => els.map(e => e.textContent))
  if (!reasons.some(t => t.includes('BMS1') && t.includes('点位'))) throw new Error('点位命中缺失')
  await page.fill('.tree-search input', '')
  await page.waitForTimeout(100)
})

await step('批量导入与站号重复拦截', async () => {
  await go('access', 'site-farm')
  await page.evaluate(() => { state.batchMode = true; render() })
  await page.waitForTimeout(100)
  await page.click('text=批量导入…')
  await page.waitForTimeout(80)
  await page.click('text=预检')
  await page.waitForTimeout(100)
  const preview = await page.textContent('.csv-panel')
  if (!preview.includes('站号重复')) throw new Error('未拦截重复站号')
  await page.click('text=导入可导入项')
  await page.waitForTimeout(120)
  const status = await page.evaluate(() => ({ pcs7: findNode(ledger(), 'pcs7').status, bms7: findNode(ledger(), 'bms7').status, pcs8: findNode(ledger(), 'pcs8').status }))
  if (status.pcs7 !== '在线' || status.bms7 !== '在线') throw new Error('可导入项未接入 ' + JSON.stringify(status))
  if (status.pcs8 !== '未绑定') throw new Error('重复行被导入')
})

await step('勾选两台批量套用', async () => {
  for (const name of ['PCS9', 'PCS10']) {
    const row = page.locator('.batch-table tr', { hasText: name })
    await row.locator('input[type=checkbox]').check()
  }
  await page.waitForTimeout(80)
  await page.click('text=批量套用')
  await page.waitForTimeout(120)
  const result = await page.evaluate(() => ({ a: state.batchResult['pcs9'], b: state.batchResult['pcs10'] }))
  if (!result.a || result.a.status !== '待验证') throw new Error('未生成草稿 ' + JSON.stringify(result))
  if (result.a.station !== 19 || result.b.station !== 20) throw new Error('站号不对 ' + JSON.stringify(result))
})

await step('全部验证只接入已套用项', async () => {
  await page.click('text=全部验证')
  await page.waitForTimeout(120)
  const status = await page.evaluate(() => ({ a: findNode(ledger(), 'pcs9').status, b: findNode(ledger(), 'pcs10').status, c: findNode(ledger(), 'pcs11').status }))
  if (status.a !== '在线' || status.b !== '在线') throw new Error('未接入 ' + JSON.stringify(status))
  if (status.c !== '未绑定') throw new Error('未套用的也被接入')
})

await step('通道离线不伪装成功', async () => {
  const row = page.locator('.batch-table tr', { hasText: 'PCS11' })
  await row.locator('input[type=checkbox]').check()
  await page.click('.batch-bar .chips button:has-text("RTU")')
  await page.waitForTimeout(80)
  await page.click('text=批量套用')
  await page.waitForTimeout(100)
  await page.click('text=全部验证')
  await page.waitForTimeout(120)
  const status = await page.evaluate(() => findNode(ledger(), 'pcs11').status)
  if (status !== '未绑定') throw new Error('离线通道仍接入: ' + status)
  const result = await page.evaluate(() => state.batchResult['pcs11'])
  if (!result || result.status !== '通道离线') throw new Error('结果 ' + JSON.stringify(result))
})

console.log('PASS: ' + ok.join(' | '))
if (fail.length) console.log('FAIL: ' + fail.join(' | '))
if (errors.length) console.log('ERRORS: ' + errors.join(' | '))
process.exit(fail.length ? 1 : 0)
