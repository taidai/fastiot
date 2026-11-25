// Actual React -> real Go API. No DOM data patches or fabricated responses.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'

const base = process.env.RULE_FIXTURE_URL || 'http://127.0.0.1:18023'
const fixture = await (await fetch(base + '/__fixture')).json()
assert.equal(fixture.fixture, 'ticket13-private-simulator')
assert.equal(fixture.writes, 0)
const api = async (path, method='GET', data) => {
 const response = await fetch(base+'/api'+path, {method,headers:{'Content-Type':'application/json',Authorization:`Bearer ${fixture.token}`},body:data===undefined?undefined:JSON.stringify(data)})
 return {status:response.status,body:await response.json()}
}
const point = (node,type) => node.points.find(p=>p.type===type && !(p.tombstone%2))
const readRule = async () => (await api('/snapshot')).body.nodes.find(n=>n.id===fixture.rule)
const seed = JSON.parse(point(await readRule(),'draft').text)
assert.ok(seed.table.rows.some(row => row.conditions === null && row.actions === null), 'fresh Go nil-array seed required; do not normalize fixture')
const browser = await chromium.launch({channel:process.env.BROWSER_CHANNEL||'msedge',headless:true})
try {
 const context=await browser.newContext({viewport:{width:1440,height:900}})
 await context.addInitScript(token=>{sessionStorage.setItem('fastems-token',token);localStorage.setItem('fastems-workbench','delivery')},fixture.token)
 const page=await context.newPage();page.setDefaultTimeout(15000)
 const errors=[];page.on('pageerror',error=>errors.push(error.message))
 const open=async()=>{await page.goto(base);await page.locator('.console-topnav button').filter({hasText:'策略',exact:true}).click();await page.locator('.rule-editor').waitFor();await page.getByRole('button',{name:'表格视图',exact:true}).click()}
 const response=path=>page.waitForResponse(r=>r.url().endsWith(path)&&r.request().method()!=='GET')
 const settle=()=>page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
 const save=async seconds=>{
  await page.locator('.rule-table').getByLabel('持续确认秒').first().fill(String(seconds))
  const pending=response(`/rules/${fixture.rule}/draft`)
  await page.getByRole('button',{name:'保存草稿',exact:true}).click()
  const r=await pending;await settle();return {status:r.status(),body:await r.json(),sent:r.request().postDataJSON()}
 }
 const publish=async()=>{
  await page.getByRole('button',{name:'发布当前规则',exact:true}).click()
  const pending=response(`/rules/${fixture.rule}/publish`)
  await page.getByRole('button',{name:'确认发布',exact:true}).click()
  const r=await pending;await settle();assert.equal(r.status(),200);return r.request().postDataJSON()
 }
 await open()
 assert.equal(await page.locator('.rule-table').getByLabel('持续确认秒').count(), 2, 'null fallback arrays render safely')
 const first=await save(1);assert.equal(first.status,200)
 const second=await save(2);assert.equal(second.status,200)
 assert.equal(second.sent.revision,first.body.revision)
 const publication=await publish();assert.equal(publication.revision,second.body.revision)
 let stored=await readRule()
 assert.equal(JSON.parse(point(stored,'applied').text).revision,second.body.revision)
 assert.equal(point(stored,'enabled')?.value||0,0)
 console.log('PASS raw nil arrays; continuous saves200/200; UI publication200; remains disabled')

 await page.getByRole('button',{name:'版本与历史',exact:true}).click()
 const restoreResponse=response(`/rules/${fixture.rule}/restore`)
 await page.getByRole('button',{name:'恢复到草稿',exact:true}).first().click()
 const restored=await restoreResponse;assert.equal(restored.status(),200)
 assert.equal(restored.request().postDataJSON().revision,second.body.revision)
 const restoredBody=await restored.json();await settle()
 assert.equal(await page.locator('.rule-table').getByLabel('持续确认秒').count(),2,'restored null fallback arrays render safely')
 stored=await readRule()
 assert.equal(JSON.parse(point(stored,'applied').text).revision,second.body.revision,'restore changes only draft')
 const restoredPublication=await publish()
 assert.equal(restoredPublication.revision,restoredBody.revision)
 assert.equal(restoredPublication.table.rows[1].conditions,null,'publication keeps exact saved nil-array wire snapshot')
 const third=await save(3);assert.equal(third.status,200)
 assert.equal(third.sent.revision,restoredBody.revision)
 console.log('PASS restore expected revision/response revision; exact restored publication; next save200')

 const remote=JSON.parse(point(await readRule(),'draft').text)
 remote.table.rows[0].hold_ms=4000
 const advanced=await api(`/rules/${fixture.rule}/draft`,'PUT',remote);assert.equal(advanced.status,200)
 const conflict=await save(5);assert.equal(conflict.status,409)
 assert.equal(await page.locator('.rule-table').getByLabel('持续确认秒').first().inputValue(),'5')
 assert.match(await page.locator('.rule-state').innerText(),/有未保存修改/)
 assert.equal(JSON.parse(point(await readRule(),'draft').text).table.rows[0].hold_ms,4000)
 console.log('PASS real concurrent409 preserves local dirty5 and remote saved4')

 await open()
 let releaseResponse, persistedResponse
 const release=new Promise(resolve=>{releaseResponse=resolve})
 const persisted=new Promise(resolve=>{persistedResponse=resolve})
 const draftPath=`**/api/rules/${fixture.rule}/draft`
 await page.route(draftPath,async route=>{const real=await route.fetch();persistedResponse(real.status());await release;await route.fulfill({response:real})})
 await page.locator('.rule-table').getByLabel('持续确认秒').first().fill('6')
 const delivered=response(`/rules/${fixture.rule}/draft`)
 await page.getByRole('button',{name:'保存草稿',exact:true}).click()
 assert.equal(await persisted,200)
 await page.locator('.rule-table').getByLabel('持续确认秒').first().fill('7')
 releaseResponse();const late=await delivered;const lateBody=await late.json();await settle()
 await page.unroute(draftPath)
 assert.equal(await page.locator('.rule-table').getByLabel('持续确认秒').first().inputValue(),'7')
 assert.match(await page.locator('.rule-state').innerText(),/有未保存修改/)
 const afterLate=await save(7);assert.equal(afterLate.status,200);assert.equal(afterLate.sent.revision,lateBody.revision)
 console.log('PASS edit during held genuine save response remains dirty; subsequent save uses new revision')

 await page.getByLabel('新规则作用范围',{exact:true}).selectOption('self')
 const createResponse=response('/api/rules')
 await page.getByRole('button',{name:'+ 新建决策表',exact:true}).click()
 const created=await createResponse;assert.equal(created.status(),200);assert.equal(created.request().postDataJSON().document.scope,'self')
 const createdBody=await created.json()
 await page.locator('.rule-editor h2').filter({hasText:'新决策表'}).waitFor()
 const emptySave=response(`/rules/${createdBody.id}/draft`)
 await page.getByRole('button',{name:'保存草稿',exact:true}).click()
 const empty=await emptySave;assert.equal(empty.status(),200)
 assert.deepEqual(empty.request().postDataJSON().bindings,{points:{},actions:{}},'palette nodes are not persisted as unused scope references')
 assert.equal((await(await fetch(base+'/__fixture')).json()).writes,0)
 assert.deepEqual(errors,[])
 console.log('PASS explicit self creation200/empty save200; all actual React calls; device writes=0; no page errors')
} finally {await browser.close()}
