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

await step('进入第5步', async () => {
  await page.evaluate(() => { state.workbench = 'device'; state.page = 'ledger'; state.step = 'ui'; state.selD = 'area-pump'; state.cfgDraft = null; render() })
  await page.waitForTimeout(100)
  if (!(await page.textContent('.cfg-bar')).includes('默认块')) throw new Error('未显示默认块')
  const preview = await page.textContent('.cfg-frame')
  if (!preview.includes('泵房')) throw new Error('预览没渲染客户屏')
})

await step('移除设备清单后预览生效', async () => {
  const row = page.locator('.cfg-row', { hasText: '设备清单' })
  await row.getByText('移除').click()
  await page.waitForTimeout(100)
  const preview = await page.textContent('.cfg-frame')
  if (preview.includes('设备清单')) throw new Error('预览仍有设备清单')
})

await step('添加今日执行块（重复不可加）', async () => {
  const chips = await page.textContent('.cfg-list .chips')
  if (chips.includes('今日执行')) throw new Error('今日执行已在块里，不应出现在目录')
})

await step('保存本层', async () => {
  await page.click('text=保存本层')
  await page.waitForTimeout(100)
  const saved = await page.evaluate(() => state.screens['area-pump'])
  if (!saved || saved.blocks.includes('devices')) throw new Error('保存内容不对: ' + JSON.stringify(saved))
  if (!(await page.textContent('.cfg-bar')).includes('本层已配')) throw new Error('状态未变')
})

await step('客户视图按配置渲染', async () => {
  await page.evaluate(() => { state.workbench = 'customer'; state.custLayer = 'area-pump'; render() })
  await page.waitForTimeout(100)
  const canvas = await page.textContent('.cust-canvas')
  if (canvas.includes('设备清单')) throw new Error('客户视图仍有设备清单')
  if (!(await page.textContent('.cust-meta')).includes('按配界面显示')) throw new Error('未标按配置')
})

await step('子层继承', async () => {
  // 水泵房下的设备 p1 没有自己的配置
  await page.evaluate(() => { state.workbench = 'device'; state.step = 'ui'; state.selD = 'p1'; state.cfgDraft = null; render() })
  await page.waitForTimeout(100)
  const bar = await page.textContent('.cfg-bar')
  if (!bar.includes('继承自')) throw new Error('未显示继承: ' + bar)
  if (bar.includes('设备清单')) throw new Error('继承应按设备层过滤掉设备清单')
})

await step('应用到同类层', async () => {
  await page.evaluate(() => { state.step = 'ui'; state.selD = 'p1'; state.cfgDraft = null; render() })
  await page.waitForTimeout(100)
  await page.click('text=保存并应用到同类层')
  await page.waitForTimeout(100)
  const count = await page.evaluate(() => Object.keys(state.screens).length)
  if (count < 4) throw new Error('同类层未写入: ' + count)
})

await step('今日执行块渲染', async () => {
  await page.evaluate(() => { state.workbench = 'customer'; state.custLayer = 'site-pump'; render() })
  await page.waitForTimeout(100)
  const side = await page.textContent('.canvas-side')
  if (!side.includes('今日执行') || !side.includes('跳过')) throw new Error('今日执行未渲染')
})

console.log('PASS: ' + ok.join(' | '))
if (fail.length) console.log('FAIL: ' + fail.join(' | '))
if (errors.length) console.log('ERRORS: ' + errors.join(' | '))
process.exit(fail.length ? 1 : 0)
