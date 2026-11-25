// Public seam: visible configurator -> real Go save/refetch -> customer overview.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'
const upstream = 'http://127.0.0.1:18107'
const root = fileURLToPath(new URL('../../../../internal/web/dist/uniapp/', import.meta.url))
const writes = []
const server = createServer(async (req, res) => {
 const url = new URL(req.url, 'http://localhost')
 if (url.pathname.startsWith('/api/')) {
  let body = ''; for await (const chunk of req) body += chunk
  if (req.method === 'PUT' && url.pathname.includes('/ems/screen/')) writes.push(JSON.parse(body))
  try {
   const response = await fetch(upstream + req.url, { method:req.method, headers:{'Content-Type':'application/json', ...(req.headers.authorization ? {Authorization:req.headers.authorization}:{}), ...(req.headers['x-fastiot-project'] ? {'X-Fastiot-Project':req.headers['x-fastiot-project']}: {})}, body:req.method === 'GET' ? undefined : body })
   res.writeHead(response.status, {'Content-Type':'application/json'}); res.end(Buffer.from(await response.arrayBuffer()))
  } catch { res.writeHead(502); res.end() }
  return
 }
 const file = path.resolve(root, url.pathname.replace(/^\/uniapp\/?/, '') || 'index.html')
 if (!file.startsWith(root)) { res.writeHead(404); return res.end() }
 try { const content = await readFile(file); res.writeHead(200, {'Content-Type':{'.js':'application/javascript','.css':'text/css','.html':'text/html'}[path.extname(file)] || 'application/octet-stream'}); res.end(content) } catch { res.writeHead(404); res.end() }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
let browser
try {
 let fixture
 const deadline = Date.now() + 60000
 while (!fixture) { try { fixture = await (await fetch(upstream+'/__fixture')).json() } catch { if (Date.now()>deadline) throw Error('Go fixture not ready'); await new Promise(r=>setTimeout(r,500)) } }
 assert.equal(fixture.fixture, 'ticket17-isolated-store')
 browser = await chromium.launch({channel:process.env.BROWSER_CHANNEL || 'msedge', headless:true})
 const context = await browser.newContext({viewport:{width:1440,height:900}})
 const page = await context.newPage()
 const errors=[]; page.on('pageerror',e=>errors.push(e.message)); page.setDefaultTimeout(15000)
 const base = `http://127.0.0.1:${server.address().port}/uniapp/`
 await page.goto(base+'#/pages/login/index')
 await page.locator('.login input[type="text"]').fill('admin')
 await page.locator('.login input[type="password"]').fill('overview-browser-test-only')
 await page.keyboard.press('Enter')
 await page.waitForURL(/pages\/overview\/index/)
 await page.locator('.screen-renderer').waitFor()
 assert.equal(await page.locator('.prototype-chart').count(), 0, 'live mode never renders illustrative bars')
 assert.doesNotMatch(await page.locator('.screen-renderer').innerText(), /饮水线冲洗|备用泵试转|采暖预循环/)
 await page.goto(base+'#/pages/design/index')
 await page.locator('.stepper uni-button').filter({hasText:'配界面'}).click()
 await page.locator('.design-ledger .node').filter({hasText:'Screen area'}).click()
 await page.locator('.cfg-bar').filter({hasText:'Screen area'}).waitFor()
 const login = await (await fetch(upstream+'/api/login', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({email:'admin',password:'overview-browser-test-only'})})).json()
 const api = async (id, method='GET', body) => {
  const response = await fetch(upstream+'/api/ems/screen/'+id, {method, headers:{'Content-Type':'application/json',Authorization:`Bearer ${login.token}`}, body:body ? JSON.stringify(body):undefined})
  return {status:response.status, body:await response.json()}
 }
 const edit = async type => page.locator(`.cfg-row[data-block="${type}"] uni-button`).filter({hasText:'配置'}).click()
 const back = async () => page.locator('uni-button').filter({hasText:'返回块列表'}).click()
 const label = () => page.getByLabel('指标名称').first().locator('input').or(page.locator('input[aria-label="指标名称"]')).first()
 const save = async same => { await page.locator('uni-button').filter({hasText:same ? '保存并应用到同类层':'保存本层',exact:true}).click(); await page.locator('.save-status').filter({hasText:'已保存'}).waitFor() }
 for (const type of ['recent','trend','today']) await page.locator(`.cfg-row[data-block="${type}"] uni-button`).filter({hasText:'移除'}).click()
 for (const type of ['children','devices']) { await edit(type); await page.getByLabel('块列宽').selectOption('1'); await back() }
 await page.locator('.cfg-row[data-block="devices"] uni-button').filter({hasText:'↑'}).click()
 await edit('kpi')
 for (const [index,key] of ['online_devices','active_alarms','communication_success_rate','data_freshness','today_energy','run_duration'].entries()) await page.getByLabel(`指标 ${index+1} 绑定`,{exact:true}).selectOption(`auto:${key}`)
 await label().fill('Saved screen metric')
 await page.locator('.cfg-preview .metric').filter({hasText:'今日电量'}).locator('.v').filter({hasText:/^13/}).waitFor()
 assert.match(await page.locator('.cfg-preview .metric').filter({hasText:'今日电量'}).innerText(), /部分统计.*有效覆盖/s)
 assert.match(await page.locator('.cfg-preview .metric').filter({hasText:'运行时长'}).innerText(), /—.*未配置/s)
 await save(false)
 assert.equal(writes.at(-1).layer,fixture.area)
 const previewStructure = await page.locator('.screen-renderer .screen-block').evaluateAll(blocks=>blocks.map(el=>[el.dataset.block,el.style.gridColumn]))
 await label().fill('Unsaved metric')
 await page.locator('.cfg-preview .metric').filter({hasText:'Unsaved metric'}).waitFor()
 assert.match(await page.locator('.cfg-preview .metrics').innerText(),/Unsaved metric/)
 const live = await page.context().newPage()
 await live.goto(base+'#/pages/overview/index')
 await live.locator('.ledger .node').filter({hasText:'Screen area'}).click()
 await live.locator('.metric').filter({hasText:'Saved screen metric'}).waitFor()
 await live.locator('[data-daily-stat="energy"] b').filter({hasText:/^13/}).waitFor()
 assert.match(await live.locator('.metric').filter({hasText:'今日电量'}).innerText(), /13.*部分统计.*有效覆盖/s)
 assert.match(await live.locator('[data-daily-stat="energy"]').getAttribute('title'), /UTC.*缺失不补零.*重启前缺口不补齐/)
 assert.match(await live.locator('[data-daily-stat="runtime"]').innerText(), /—.*未配置/s)
 assert.doesNotMatch(await live.locator('.metrics').innerText(),/Unsaved metric/)
 assert.deepEqual(await live.locator('.screen-renderer .screen-block').evaluateAll(blocks=>blocks.map(el=>[el.dataset.block,el.style.gridColumn])),previewStructure)
 assert.equal(await live.locator('.screen-block[data-block="today"]').count(),0)
 await label().fill('Applied metric'); await save(false)
 await live.reload(); await live.locator('.metric').filter({hasText:'Applied metric'}).waitFor()
 assert.equal((await api(fixture.area)).body.version,2)
 await back()
 await page.locator('.cfg-row[data-block="summary"] uni-button').filter({hasText:'移除'}).click()
 assert.equal(await page.locator('.cfg-preview [data-daily-stat="energy"]').count(),0)
 assert.match(await page.locator('.cfg-preview .metric').filter({hasText:'今日电量'}).innerText(), /13.*部分统计/s, 'KPI still consumes daily results without the summary block')
 assert.equal(await live.locator('[data-daily-stat="energy"]').count(),1, 'unsaved block removal is not live')
 // A saved parent reference outside its child stays visibly unbound, never remapped.
 const parent = (await api(fixture.area)).body
 parent.blocks.find(block=>block.type==='kpi').binding.metrics[0]={point_id:fixture.outside_point,label:'Parent-only binding'}
 assert.equal((await api(fixture.area,'PUT',parent)).status,200)
 await page.locator('.design-ledger .node').filter({hasText:'Screen child'}).click()
 await page.locator('.cfg-bar').filter({hasText:'继承自 Screen area'}).waitFor()
 await page.locator('.cfg-preview .metric').filter({hasText:'Parent-only binding'}).filter({hasText:'未绑定'}).waitFor()
 await page.locator('uni-button').filter({hasText:'保存本层',exact:true}).click()
 await page.locator('.save-error').filter({hasText:'绑定'}).waitFor()
 await edit('kpi'); await page.getByLabel('指标 1 绑定',{exact:true}).selectOption('auto:online_devices'); await save(false)
 assert.equal((await api(fixture.child)).body.version,1)
 assert.equal((await api(fixture.area)).body.version,3)
 // Another real HTTP session advances the source; stale editor cannot overwrite it.
 await page.locator('.design-ledger .node').filter({hasText:'Screen area'}).click()
 await page.locator('.cfg-bar').filter({hasText:'v3'}).waitFor()
 const concurrent = (await api(fixture.area)).body
 concurrent.blocks.find(block=>block.type==='kpi').binding.metrics[0]={metric:'online_devices',label:'Other editor'}
 assert.equal((await api(fixture.area,'PUT',concurrent)).status,200)
 await page.locator('uni-button').filter({hasText:'保存本层',exact:true}).click()
 await page.locator('.save-error').filter({hasText:'版本'}).waitFor()
 assert.equal(await page.locator('.save-status').count(),0)
 await page.reload(); await page.locator('.stepper uni-button').filter({hasText:'配界面'}).click()
 await page.locator('.design-ledger .node').filter({hasText:'Screen area'}).click()
 await page.locator('.cfg-bar').filter({hasText:'v4'}).waitFor()
 await save(true)
 assert.equal((await api(fixture.area)).body.version,5)
 assert.equal((await api(fixture.child)).body.version,2)
 assert.equal((await api(fixture.sibling)).body.version,1)
 await live.reload(); await live.locator('.ledger .node').filter({hasText:'Screen sibling'}).click()
 await live.locator('.metric').filter({hasText:'Other editor'}).waitFor()
 await page.locator('.design-ledger .node').filter({hasText:'Screen device'}).first().click()
 await page.locator('uni-button').filter({hasText:'恢复默认',exact:true}).click()
 await edit('deviceData')
 await page.locator('.cfg-preview .device-tabs uni-button').filter({hasText:'设备参数'}).click()
 await page.locator('.cfg-preview .device-params').waitFor()
 await page.locator('.bind-panel .catalog uni-button').filter({hasText:'设备参数'}).click()
 assert.equal(await page.locator('.cfg-preview .device-tabs uni-button').filter({hasText:'设备参数'}).count(),0)
 assert.equal(await page.locator('.cfg-preview .device-params').count(),0,'disabled tab cannot retain its old active content')
 await save(false)
 await live.reload(); await live.locator('.ledger .node').filter({hasText:'Screen device'}).first().click()
 await live.locator('.device-tabs').waitFor()
 assert.equal(await live.locator('.prototype-chart').count(),0)
 assert.match(await live.locator('[data-block="today"]').innerText(), /暂无可用的执行记录/)
 assert.equal(await live.locator('.device-tabs uni-button').filter({hasText:'设备参数'}).count(),0)
 for (const width of [1440,1280,1024,375]) {
  await live.setViewportSize({width,height:width===375?812:900})
  assert(await live.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`no overflow at ${width}`)
 }
 if (process.env.FASTIOT_SCREENSHOT) await live.screenshot({path:process.env.FASTIOT_SCREENSHOT,fullPage:true})
 assert.deepEqual(errors,[])
 console.log('PASS real Go save/refetch, shared slots/spans, dirty isolation, child inheritance/unbound repair, concurrent conflict, same-kind versions and responsive layouts')
} finally {
 await browser?.close(); await new Promise(resolve=>server.close(resolve))
 await fetch(upstream+'/__finish',{method:'POST'}).catch(()=>{})
}
