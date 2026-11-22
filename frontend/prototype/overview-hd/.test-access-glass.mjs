import { chromium } from 'playwright-core'

const file = 'file:///C:/Users/chent/Desktop/2026%E6%88%90%E6%9E%9C/AAAAA%E5%BC%80%E6%BA%90/easyems/frontend/prototype/overview-hd/%E8%AE%BE%E8%AE%A1%20%C2%B7%20%E7%8E%BB%E7%92%83%E7%A3%A8%E7%A0%82.html'
const out = 'C:/Users/chent/Desktop/2026成果/AAAAA开源/easyems/frontend/prototype/overview-hd/'
const browser = await chromium.launch({ channel: 'msedge', headless: true })
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
const errors = []
page.on('pageerror', e => errors.push(String(e)))
await page.goto(file)
await page.evaluate(() => setProject('批量接入示例'))
const ok = [], fail = []
async function step(name, fn) {
  try { await fn(); ok.push(name) } catch (e) { fail.push(name + ': ' + e.message.split('\n')[0]) }
}
async function go(sel) {
  await page.evaluate(id => { state.workbench='device'; state.page='ledger'; state.step='access'; state.selD=id; state.accessStage='profile'; state.safeWrite=false; render() }, sel)
  await page.waitForTimeout(120)
}

await step('Profile 展开声明', async () => {
  await go('pcs7')
  const text = await page.textContent('.stage')
  for (const key of ['Profile 声明','安全写点','必需 5','未声明可写的点一律只读']) {
    if (!text.includes(key)) throw new Error('缺 ' + key)
  }
  await page.screenshot({ path: out + 'access-profile.png' })
})

await step('通道工作区', async () => {
  await page.click('[data-stage="channel"]')
  await page.waitForTimeout(100)
  if (!(await page.textContent('.stage')).includes('本站连接')) throw new Error('缺连接信息')
  await page.screenshot({ path: out + 'access-channel.png' })
})

await step('自动匹配并查看点位字段', async () => {
  await page.click('[data-stage="points"]')
  await page.click('text=按 Profile 自动匹配')
  await page.waitForTimeout(100)
  const text = await page.textContent('.stage')
  if (!text.includes('6/6 已映射')) throw new Error('未生成 6 行映射')
  for (const key of ['类型','倍率','必需','读写']) if (!text.includes(key)) throw new Error('缺列 ' + key)
  await page.screenshot({ path: out + 'access-points.png' })
})

await step('默认只跑 L1/L2', async () => {
  await page.click('[data-stage="verify"]')
  await page.click('text=运行验证并接入')
  await page.waitForTimeout(120)
  const text = await page.textContent('.stage')
  if (!text.includes('L1 连通') || !text.includes('通过 · 12 ms')) throw new Error('L1 未通过')
  if (!text.includes('通过 · 5/5 必需点')) throw new Error('L2 未按必需点通过')
  if (!text.includes('未执行（默认不写）')) throw new Error('默认仍写回读')
  const status = await page.evaluate(() => findNode(ledger(), 'pcs7').status)
  if (status !== '在线') throw new Error('设备未接入: ' + status)
  await page.screenshot({ path: out + 'access-verify.png' })
})

await step('勾选后才执行 L3', async () => {
  await page.locator('.bind-opt input').check()
  await page.click('text=重新验证')
  await page.waitForTimeout(120)
  if (!(await page.textContent('.stage')).includes('通过 · 有功功率 回读一致')) throw new Error('L3 未按安全点执行')
})

await step('离线通道不接入', async () => {
  await go('pcs8')
  await page.click('[data-stage="channel"]')
  await page.locator('.pick', { hasText: 'Modbus RTU' }).click()
  await page.click('[data-stage="points"]')
  await page.click('text=按 Profile 自动匹配')
  await page.click('[data-stage="verify"]')
  await page.click('text=运行验证并接入')
  await page.waitForTimeout(120)
  if ((await page.evaluate(() => findNode(ledger(), 'pcs8').status)) !== '未绑定') throw new Error('离线通道仍被接入')
})

console.log('PASS: ' + ok.join(' | '))
if (fail.length) console.log('FAIL: ' + fail.join(' | '))
if (errors.length) console.log('ERRORS: ' + errors.join(' | '))
process.exit(fail.length ? 1 : 0)
