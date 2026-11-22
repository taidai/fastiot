import { chromium } from 'playwright-core'
const url = 'file:///C:/Users/chent/Desktop/2026%E6%88%90%E6%9E%9C/AAAAA%E5%BC%80%E6%BA%90/easyems/frontend/prototype/overview-hd/%E4%BA%A4%E4%BB%98%20%C2%B7%20%E7%8E%BB%E7%92%83%E7%A3%A8%E7%A0%82.html'
const out = 'C:/Users/chent/Desktop/2026成果/AAAAA开源/easyems/frontend/prototype/overview-hd/'
const b = await chromium.launch({ channel:'msedge', headless:true })
const p = await b.newPage({ viewport:{width:1440,height:1100} })
const errors=[]; p.on('pageerror', e=>errors.push(String(e)))
await p.goto(url)
const results=[]
async function check(name, fn){ try{ await fn(); results.push('PASS '+name) }catch(e){ results.push('FAIL '+name+': '+e.message.split('\n')[0]) } }
await check('预检失败阻断发布', async()=>{
  await p.click('text=模拟预检失败')
  await p.click('text=发布版本')
  await p.waitForTimeout(100)
  if(await p.evaluate(()=>state.published)) throw new Error('预检失败仍发布')
  await p.click('text=取消预检失败')
})
await check('导入后必须重绑再运行', async()=>{
  await p.click('text=发布版本')
  await p.click('text=导入到新实例')
  await p.waitForTimeout(100)
  if(!(await p.textContent('#stage')).includes('现场重绑')) throw new Error('没有重绑步骤')
  await p.screenshot({path:out+'delivery-import.png'})
  await p.click('text=完成现场重绑')
  await p.click('text=在新实例运行')
  await p.waitForTimeout(100)
  if(!(await p.textContent('#stage')).includes('36 个采集点恢复')) throw new Error('未运行')
  await p.screenshot({path:out+'delivery-running.png'})
})
await check('依赖不匹配拒绝导入', async()=>{
  await p.click('text=模拟依赖不匹配')
  await p.evaluate(()=>{state.importStep='idle';render()})
  await p.click('text=导入到新实例')
  await p.waitForTimeout(100)
  if(!(await p.textContent('#stage')).includes('未创建任何节点')) throw new Error('不匹配仍导入')
})
console.log(results.join('\n'))
console.log(errors.length ? 'ERRORS '+errors.join('|') : 'no errors')
process.exit(results.some(x=>x.startsWith('FAIL')) ? 1 : 0)
