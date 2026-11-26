// Real Go lifecycle/HTTP + browser interaction. Fault injection delays the real
// response only; no alarm or handover data is fabricated here.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { chromium } from 'playwright-core'
const upstream = process.env.FASTIOT_GO_API || 'http://127.0.0.1:18101'
const root = fileURLToPath(new URL('../../../../internal/web/dist/uniapp/', import.meta.url))
let hold = '', release, held
let fail = false
const requests = []
const server = createServer(async (req, res) => {
 const url = new URL(req.url, 'http://localhost')
 if (url.pathname.startsWith('/api/')) {
  let body = ''; for await (const part of req) body += part
  requests.push({path:url.pathname, query:url.search, method:req.method})
  if (fail && url.pathname === '/api/overview/handover') {res.writeHead(503,{'Content-Type':'application/json'});return res.end('{"error":"fixture_unavailable"}')}
  try {
   const response = await fetch(upstream + req.url, {method:req.method,headers:{'Content-Type':'application/json',...(req.headers.authorization?{Authorization:req.headers.authorization}:{}),...(req.headers['x-fastiot-project']?{'X-Fastiot-Project':req.headers['x-fastiot-project']}:{})},body:req.method==='GET'?undefined:body})
   const data = Buffer.from(await response.arrayBuffer())
   // Flush no-store headers before delaying the body, otherwise Chromium's
   // same-URL cache lock serializes the PUT behind the held GET.
   res.writeHead(response.status,{'Content-Type':'application/json','Cache-Control':'no-store'})
   res.flushHeaders()
   if (hold === req.method && url.pathname === '/api/overview/handover') {hold='';held=true;await new Promise(resolve=>{release=resolve})}
   res.end(data)
  } catch {res.writeHead(502);res.end()}
  return
 }
 const file = path.resolve(root,url.pathname.replace(/^\/uniapp\/?/,'')||'index.html')
 if (!file.startsWith(root)) {res.writeHead(404);return res.end()}
 try {const data=await readFile(file);res.writeHead(200,{'Content-Type':{'.js':'application/javascript','.css':'text/css','.html':'text/html'}[path.extname(file)]||'application/octet-stream'});res.end(data)} catch {res.writeHead(404);res.end()}
})
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
let browser
try {
 let ids
 const readyDeadline = Date.now() + 180000
 while (!ids) {
  try { ids = await (await fetch(upstream+'/__fixture', {signal:AbortSignal.timeout(3000)})).json() }
  catch (error) { if (Date.now() > readyDeadline) throw error; await new Promise(resolve=>setTimeout(resolve,500)) }
 }
 console.log('READY real Go alarm fixture')
 const control=async(route,body)=>{const response=await fetch(upstream+route,{method:'POST',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});assert.equal(response.status,204)}
 try {browser=await chromium.launch({channel:'chrome',headless:true,timeout:15000})}catch{browser=await chromium.launch({channel:'msedge',headless:true})}
 const page=await browser.newPage({viewport:{width:1440,height:900}})
 page.setDefaultTimeout(15000)
 const errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.goto(`http://127.0.0.1:${server.address().port}/uniapp/#/pages/login/index`)
 await page.locator('.login input[type="text"]').fill('admin')
 await page.locator('.login input[type="password"]').fill('overview-browser-test-only')
 await page.keyboard.press('Enter')
 const layer=async name=>{await page.locator('.ledger .node').filter({hasText:new RegExp('现场.*'+name)}).first().click();await page.locator('.headline h1').filter({hasText:name}).waitFor()}
 await page.locator('.ledger .node').filter({hasText:'North'}).first().waitFor()
 await layer('North')
 await page.locator('.alarm-mini').filter({hasText:'North high pressure'}).waitFor()
 assert.doesNotMatch(await page.locator('.side').innerText(),/South high pressure/)
 await page.locator('.bell').click()
 const dialog=page.locator('[role="dialog"][aria-label="告警中心"]')
 await dialog.locator('.alarm').filter({hasText:'South high pressure'}).waitFor()
 const north=dialog.locator('.alarm').filter({hasText:'North high pressure'}).first()
 await north.getByText('确认接手',{exact:true}).click()
 await dialog.getByText(/^活动/).click()
 await north.getByText(/已接手，仍活动/).waitFor()
 console.log('PASS acknowledged alarm remains active')
 assert.match(await page.locator('.side').innerText(), /North high pressure/, 'acknowledgement must not hide the layer live condition')
 assert.equal(await page.locator('.bell .badge').innerText(), '1', 'global badge counts pending, not all active conditions')
 await control('/__observe',{site:'North',value:47})
 await north.getByText(/异常观测 2 次/).waitFor()
 await control('/__restart')
 await page.reload()
 await page.locator('.bell').click()
 await dialog.getByText(/^活动/).click()
 await north.getByText(/异常观测 2 次/).waitFor()
 await north.getByText(/已接手，仍活动/).waitFor()
 await north.getByText('定位到对象',{exact:true}).click()
 await page.locator('.headline h1').filter({hasText:'North pump'}).waitFor()
 await page.locator('.bell').click()
 await dialog.getByText(/^活动/).click()
 await north.getByText('看历史',{exact:true}).click()
 const historyDrawer=page.locator('.history-drawer')
 await historyDrawer.waitFor()
 assert.ok(requests.some(r=>r.path==='/api/history'&&r.query.includes(ids['Zero pressure'])))
 await historyDrawer.getByText('← 返回',{exact:true}).click()
 await historyDrawer.waitFor({state:'detached'})
 await page.locator('.ledger').waitFor()
 await control('/__observe',{site:'North',value:20})
 await page.locator('.bell').click()
 await dialog.getByText(/^历史/).click()
 await north.getByText(/已恢复/).waitFor()
 await north.getByText('处置记录',{exact:true}).click()
 await dialog.locator('.occurrence-events').getByText(/已接手/).waitFor()
 await control('/__observe',{site:'North',value:48})
 await dialog.getByText(/^待接手/).click()
 await north.getByText(/异常观测 1 次/).waitFor()
 await dialog.getByText(/^历史/).click()
 await north.getByText(/已恢复/).waitFor()
 await dialog.getByText('关闭',{exact:true}).click()
 await layer('North')
 await page.locator('.handover-input textarea, textarea.handover-input').fill('North shift persisted')
 await page.waitForFunction(() => !document.querySelector('.save-handover')?.hasAttribute('disabled'))
 await page.locator('.save-handover').click()
 await page.getByText('交接已保存。',{exact:true}).waitFor()
 assert.match(await page.locator('.handover-meta').innerText(),/admin|@/)
 await page.reload();await page.getByText('North shift persisted',{exact:true}).waitFor()
 await layer('South')
 assert.doesNotMatch(await page.locator('.handover-section').innerText(),/North shift persisted/)
 // Delayed save from North must not overwrite South.
 await layer('North');hold='PUT';held=false
 await page.locator('.handover-input textarea, textarea.handover-input').fill('North delayed save')
 await page.waitForFunction(() => !document.querySelector('.save-handover')?.hasAttribute('disabled'))
 await page.locator('.save-handover').click()
 const waitHeld=async()=>{const end=Date.now()+10000;while(!held&&Date.now()<end)await new Promise(r=>setTimeout(r,30));assert.ok(held)}
 await waitHeld();await layer('South');release();release=undefined
 await page.waitForTimeout(100)
 assert.doesNotMatch(await page.locator('.handover-section').innerText(),/North delayed save|交接已保存/)
 // Delayed initial GET in the SAME layer cannot overwrite a newly saved note.
 hold='GET';held=false;await layer('North');await waitHeld()
 await page.locator('.handover-input textarea, textarea.handover-input').fill('North newest save')
 await page.waitForFunction(() => !document.querySelector('.save-handover')?.hasAttribute('disabled'))
 await page.locator('.save-handover').click()
 await page.locator('.handover-note').filter({hasText:'North newest save'}).waitFor()
 release();release=undefined;await page.waitForTimeout(150)
 assert.match(await page.locator('.handover-record').innerText(),/North newest save/)
 fail=true;await layer('South');await page.getByText(/交接记录读取失败/).waitFor();fail=false
 await control('/__restart');await page.reload();await layer('North');await page.locator('.handover-note').filter({hasText:'North newest save'}).waitFor()
 await page.locator('.bell').click()
 await dialog.getByText(/^活动/).click()
 for (const width of [1280,1024,375]) {
  await page.setViewportSize({width,height:width===375?812:800})
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`no horizontal overflow at ${width}`)
  assert.ok(await dialog.isVisible())
 }
 await page.setViewportSize({width:1440,height:900})
 if (process.env.FASTIOT_SCREENSHOT) await page.screenshot({path:process.env.FASTIOT_SCREENSHOT,fullPage:true})
 assert.deepEqual(errors,[])
 console.log('PASS real Go global/layer alarms, ack, observation counts, recovery/retrigger/history, runtime restart, locate/history10, persisted scoped handover and delayed response fences')
} finally {release?.();server.closeAllConnections();server.close();await browser?.close()}
