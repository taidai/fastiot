import { chromium } from 'playwright-core'

const file = 'file:///C:/Users/chent/Desktop/2026%E6%88%90%E6%9E%9C/AAAAA%E5%BC%80%E6%BA%90/easyems/frontend/prototype/overview-hd/%E8%AE%BE%E8%AE%A1%20%C2%B7%20%E7%8E%BB%E7%92%83%E7%A3%A8%E7%A0%82.html'
const browser = await chromium.launch({ channel: 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1600, height: 1200 } })
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
async function sim(patch) {
  await page.evaluate(p => Object.assign(state.sim, p), patch)
  await page.click('text=运行模拟')
  await page.waitForTimeout(120)
}

await step('四段式列与逐行原因', async () => {
  await go('strategy', 'area-pump')
  await sim({ p: 0.3, level: 46, held: 45, clock: '09:00' })
  const heads = await page.$$eval('.rules-table th', els => els.map(e => e.textContent.trim()))
  for (const h of ['条件组', '持续', '时间窗', '动作序列']) if (!heads.includes(h)) throw new Error('缺列 ' + h)
  const rows = await page.$$eval('.rules-table tbody tr', els => els.map(e => e.textContent))
  if (!rows[0].includes('跳过') || !rows[0].includes('液位')) throw new Error('第1行缺原因: ' + rows[0])
  if (!rows[1].includes('命中')) throw new Error('第2行未命中')
})

await step('持续时长参与判断', async () => {
  await sim({ held: 10 })
  const text = await page.textContent('.rules-table')
  if (!text.includes('已持续 10s')) throw new Error('未提示持续不足')
  if (!(await page.textContent('.stage')).includes('没有规则命中')) throw new Error('仍判命中')
})

await step('时间窗参与判断', async () => {
  await sim({ p: 0.4, level: 60, held: 45, clock: '09:00' })
  let text = await page.textContent('.rules-table')
  if (!text.includes('不在时间窗')) throw new Error('窗口外未跳过')
  await sim({ clock: '14:00' })
  text = await page.textContent('.rules-table')
  if (!text.includes('每日 14:00')) throw new Error('窗口未显示')
  if (!text.includes('命中')) throw new Error('窗口内未命中')
})

await step('编辑条件组', async () => {
  await page.locator('.rules-table tbody tr').nth(0).getByText('编辑').click()
  await page.waitForTimeout(120)
  if (!(await page.textContent('.rule-form')).includes('条件组')) throw new Error('编辑器未打开')
  const selects = page.locator('.rule-form select')
  await selects.nth(4).selectOption('>')
  await page.locator('.rule-form input[type=number]').nth(0).fill('90')
  await page.locator('.rule-form input[type=number]').nth(0).blur()
  await page.click('text=保存规则')
  await page.waitForTimeout(120)
  if (!(await page.textContent('.rules-table')).includes('90%')) throw new Error('条件未更新')
})

await step('添加条件与动作', async () => {
  await page.locator('.rules-table tbody tr').nth(0).getByText('编辑').click()
  await page.waitForTimeout(100)
  const before = await page.locator('.rule-form .rule-fields').count()
  await page.click('text=＋ 条件')
  await page.click('text=＋ 动作')
  await page.waitForTimeout(100)
  const after = await page.locator('.rule-form .rule-fields').count()
  if (after <= before) throw new Error(`字段组 ${before} → ${after}`)
  await page.click('text=保存规则')
})

await step('重复条件提示', async () => {
  for (let i = 0; i < 2; i++) {
    await page.click('text=＋ 添加规则')
    await page.waitForTimeout(80)
    await page.click('text=保存规则')
    await page.waitForTimeout(80)
  }
  if (!(await page.textContent('.rules-table')).includes('重复')) throw new Error('未提示重复')
})

await step('状态机转移表', async () => {
  const text = await page.textContent('.stage')
  if (!text.includes('守卫') || !text.includes('下一状态')) throw new Error('缺少转移表')
})

console.log('PASS: ' + ok.join(' | '))
if (fail.length) console.log('FAIL: ' + fail.join(' | '))
if (errors.length) console.log('ERRORS: ' + errors.join(' | '))
process.exit(fail.length ? 1 : 0)
