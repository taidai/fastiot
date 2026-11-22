import { chromium } from 'playwright-core'

const file = 'file:///C:/Users/chent/Desktop/2026%E6%88%90%E6%9E%9C/AAAAA%E5%BC%80%E6%BA%90/easyems/frontend/prototype/overview-hd/%E8%AE%BE%E8%AE%A1%20%C2%B7%20%E7%8E%BB%E7%92%83%E7%A3%A8%E7%A0%82.html'
const browser = await chromium.launch({ channel: 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })
const errors = []
page.on('pageerror', e => errors.push(String(e)))
await page.goto(file)
await page.click('#wb-customer')
const ok = []
const fail = []

async function step(name, fn) {
  try { await fn(); ok.push(name) } catch (e) { fail.push(name + ': ' + e.message.split('\n')[0]) }
}

await step('搜索过滤', async () => {
  await page.fill('.cust-search input', '水箱')
  await page.waitForTimeout(100)
  const names = await page.$$eval('.cust-node', els => els.map(e => e.textContent.trim()))
  if (!names.some(n => n.includes('水箱'))) throw new Error('水箱不在结果')
  if (names.some(n => n.includes('1号泵'))) throw new Error('1号泵不应出现')
  await page.fill('.cust-search input', '')
  await page.waitForTimeout(100)
})

await step('KPI 进历史表', async () => {
  await page.click('.kpi >> nth=0')
  await page.waitForTimeout(150)
  const rows = await page.$$eval('.hist-table tbody tr', els => els.length)
  if (rows !== 30) throw new Error('行数 ' + rows)
  const heads = await page.$$eval('.hist-table th', els => els.map(e => e.textContent.trim()))
  if (!heads.includes('液位 %')) throw new Error('总览列不对: ' + heads.join(','))
  await page.click('text=返回实时')
})

await step('告警条开铃铛', async () => {
  await page.click('.alarm-strip')
  await page.waitForTimeout(100)
  const txt = await page.textContent('.drawer-right header')
  if (!txt.includes('告警中心')) throw new Error('未打开')
  await page.click('.drawer-right header button')
})

await step('切层到设备', async () => {
  await page.click('.cust-node >> text=1号泵')
  await page.waitForTimeout(100)
  if (!(await page.textContent('.cust-head h1')).includes('1号泵')) throw new Error('未切换')
  const alarmCard = await page.textContent('.canvas-side')
  if (!alarmCard.includes('本设备没有待处理报警')) throw new Error('本设备告警未收敛')
})

await step('设备页签', async () => {
  await page.click('text=设备参数')
  await page.waitForTimeout(100)
  if (!(await page.textContent('.canvas-main')).includes('来源模板')) throw new Error('参数页未渲染')
  await page.click('text=实时数据')
})

await step('切到储能站', async () => {
  await page.click('#projectMenuBtn', { force: true }).catch(() => {})
  await page.evaluate(() => { setProject('储能站 A') })
  await page.evaluate(() => { setWorkbench('customer'); state.custLayer = 'site-energy'; render() })
  await page.waitForTimeout(100)
  const kpis = await page.textContent('.kpi-row')
  if (!kpis.includes('储能 SOC')) throw new Error('能量 KPI 缺失')
  const strip = await page.textContent('.alarm-strip')
  if (!strip.includes('PCS1')) throw new Error('能量告警未切换')
})

await step('储能历史列', async () => {
  await page.click('.kpi >> nth=0')
  await page.waitForTimeout(120)
  const heads = await page.$$eval('.hist-table th', els => els.map(e => e.textContent.trim()))
  if (!heads.includes('SOC %')) throw new Error('列不对: ' + heads.join(','))
})

console.log('PASS: ' + ok.join(' | '))
if (fail.length) console.log('FAIL: ' + fail.join(' | '))
if (errors.length) console.log('ERRORS: ' + errors.join(' | '))
process.exit(fail.length ? 1 : 0)
