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

await step('第1步 选模板并建出来', async () => {
  await go('site', 'site-pump')
  await page.locator('.tpl-card', { hasText: '储能站' }).click()
  await page.waitForTimeout(80)
  if (!(await page.textContent('.outline')).includes('储能1')) throw new Error('骨架预览没更新')
  await page.click('text=按「储能站」建出来')
  await page.waitForTimeout(100)
  if (!(await page.textContent('#projectName')).includes('储能示例')) throw new Error('项目未切换')
  if (!(await page.textContent('.step-head')).includes('搭台账')) throw new Error('未进入第2步')
})

await step('第2步 台账表与复制子树', async () => {
  await go('tree', 'site-储能示例')
  await page.waitForTimeout(80)
  const rows = await page.$$eval('.ledger-table tbody tr', els => els.length)
  if (rows < 6) throw new Error('台账行数 ' + rows)
  await page.locator('.ledger-table tr', { hasText: '储能区' }).getByText('复制').click()
  await page.waitForTimeout(100)
  if (!(await page.textContent('.ledger-table')).includes('储能区 副本')) throw new Error('复制未生效')
})

await step('第3步 自动匹配与验证接入', async () => {
  await go('access', 'pcs2')
  await page.waitForTimeout(80)
  if (!(await page.textContent('.panel-head')).includes('未绑定')) throw new Error('PCS2 应为未绑定')
  await page.click('text=按型号自动匹配')
  await page.waitForTimeout(80)
  if (!(await page.textContent('.stage')).includes('6/6 已映射')) throw new Error('映射未生效')
  await page.click('[data-stage="verify"]')
  await page.click('text=运行验证并接入')
  await page.waitForTimeout(100)
  const head = await page.textContent('.panel-head')
  if (!head.includes('已接入')) throw new Error('设备未接入: ' + head)
  const node = await page.evaluate(() => { const n = findNode(ledger(), 'pcs2'); return n && n.status })
  if (node !== '在线') throw new Error('状态 ' + node)
})

await step('第3步 未映射时拦住验证', async () => {
  await page.evaluate(() => { state.step = 'access'; state.selD = 'bms2'; state.devMapped = {}; state.accessStage = 'verify'; render() })
  await page.waitForTimeout(80)
  await page.click('text=运行验证并接入')
  await page.waitForTimeout(80)
  const node = await page.evaluate(() => { const n = findNode(ledger(), 'bms2'); return n && n.status })
  if (node !== '未绑定') throw new Error('未映射却被接入: ' + node)
})

await step('第4步 作用范围与模拟命中', async () => {
  await go('strategy', 'area-ess')
  await page.waitForTimeout(80)
  await page.locator('.acc-card .chips button', { hasText: '本对象' }).click()
  await page.waitForTimeout(60)
  if (!(await page.textContent('.acc-card')).includes('仅本对象')) throw new Error('范围未切换')
  await page.locator('.acc-card .chips button', { hasText: '含下级' }).click()
  await page.evaluate(() => setSim('p', 0.3))
  await page.waitForTimeout(60)
  await page.click('text=运行模拟')
  await page.waitForTimeout(80)
  if (!(await page.textContent('.rules-table')).includes('命中')) throw new Error('未标记命中行')
  if (!(await page.textContent('.sim-result')).includes('启动 1号泵')) throw new Error('结果不对')
})

await step('第6步 包内容与版本差异', async () => {
  await go('delivery', 'site-储能示例')
  await page.waitForTimeout(80)
  const text = await page.textContent('.stage')
  for (const key of ['项目包里有什么', '部署记录', '现场绑定', '待部署']) {
    if (!text.includes(key)) throw new Error('缺 ' + key)
  }
  await page.click('text=发布版本')
  await page.waitForTimeout(100)
  if (!(await page.textContent('.stage')).includes('版本差异')) throw new Error('发布后无差异')
})

console.log('PASS: ' + ok.join(' | '))
if (fail.length) console.log('FAIL: ' + fail.join(' | '))
if (errors.length) console.log('ERRORS: ' + errors.join(' | '))
process.exit(fail.length ? 1 : 0)
