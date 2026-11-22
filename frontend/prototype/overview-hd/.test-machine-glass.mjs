import { chromium } from 'playwright-core'
const url = 'file:///C:/Users/chent/Desktop/2026%E6%88%90%E6%9E%9C/AAAAA%E5%BC%80%E6%BA%90/easyems/frontend/prototype/overview-hd/%E8%AE%BE%E8%AE%A1%20%C2%B7%20%E7%8E%BB%E7%92%83%E7%A3%A8%E7%A0%82.html'
const out = 'C:/Users/chent/Desktop/2026成果/AAAAA开源/easyems/frontend/prototype/overview-hd/'
const b = await chromium.launch({ channel:'msedge', headless:true })
const p = await b.newPage({ viewport:{width:1440,height:1100} })
const errors=[]; p.on('pageerror', e=>errors.push(String(e)))
await p.goto(url)
await p.evaluate(() => { state.step='strategy'; state.selD='area-pump'; render() })
await p.waitForTimeout(150)
const results=[]
async function check(name, fn){ try{ await fn(); results.push('PASS '+name) }catch(e){ results.push('FAIL '+name+': '+e.message.split('\n')[0]) } }
await check('启动到运行', async()=>{
  await p.locator('.acc-card', {hasText:'状态机'}).locator('.chips button', {hasText:'启动'}).first().click()
  await p.locator('.acc-card', {hasText:'状态机'}).locator('.chips button', {hasText:'回读运行'}).click()
  await p.waitForTimeout(100)
  const text = await p.locator('.acc-card', {hasText:'状态机'}).textContent()
  if(!text.includes('当前 运行')) throw new Error(text.slice(-400))
  await p.screenshot({path:out+'machine-running.png'})
})
await check('守卫失败保持', async()=>{
  await p.locator('.acc-card', {hasText:'状态机'}).locator('.chips button', {hasText:'停止'}).first().click()
  await p.locator('.acc-card', {hasText:'状态机'}).getByText('复位模拟').click()
  await p.locator('.acc-card', {hasText:'状态机'}).locator('.sim-check input').nth(1).uncheck()
  await p.locator('.acc-card', {hasText:'状态机'}).locator('.chips button', {hasText:'启动'}).first().click()
  await p.waitForTimeout(100)
  const text = await p.locator('.acc-card', {hasText:'状态机'}).textContent()
  if(!text.includes('许可 ∧ 无故障 不满足，保持')) throw new Error('没有保持原因')
})
await check('编辑转移', async()=>{
  await p.locator('.acc-card', {hasText:'状态机'}).getByText('编辑').first().click()
  await p.fill('#tEvent','维护')
  await p.fill('#tTo','故障')
  await p.click('text=保存转移')
  await p.waitForTimeout(100)
  if(!(await p.locator('.acc-card', {hasText:'状态机'}).textContent()).includes('维护')) throw new Error('转移未保存')
})
console.log(results.join('\n'))
console.log(errors.length ? 'ERRORS '+errors.join('|') : 'no errors')
process.exit(results.some(x=>x.startsWith('FAIL')) ? 1 : 0)
