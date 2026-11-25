// Actual prototype join controls -> Go persisted document -> pure simulation.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'
const base=process.env.RULE_FIXTURE_URL||'http://127.0.0.1:18023'
const fixture=await(await fetch(base+'/__fixture')).json()
assert.equal(fixture.fixture,'ticket13-private-simulator');assert.equal(fixture.writes,0)
const api=async(path,method='GET',body)=>{const response=await fetch(base+'/api'+path,{method,headers:{'Content-Type':'application/json',Authorization:`Bearer ${fixture.token}`},body:body===undefined?undefined:JSON.stringify(body)});return{status:response.status,body:await response.json()}}
const ids={}
for(const [name,value] of Object.entries({A:1,B:0,C:0})){const created=await api('/nodes','POST',{parent:fixture.root,type:'variable',description:`Mixed ${name}`,value});assert.equal(created.status,200);ids[name]=created.body.id}
const conditions=Object.keys(ids).map(name=>({point_id:name,operator:'eq',value:1}))
const document={scope:'self',table:{rows:[{row_id:'mixed',conditions,hold_ms:1000,actions:[]},{row_id:'fallback',conditions:[],actions:[]}]},bindings:{points:Object.fromEntries(Object.entries(ids).map(([name,id])=>[name,{node_id:id}])),actions:{}}}
const ruleName = `Prototype mixed joins ${Date.now()}`
const created=await api('/rules','POST',{parent:fixture.root,description:ruleName,document});assert.equal(created.status,200)
const browser=await chromium.launch({channel:process.env.BROWSER_CHANNEL||'msedge',headless:true})
try{
 const context=await browser.newContext({viewport:{width:1440,height:900}})
 await context.addInitScript(token=>localStorage.setItem('fastems-token',token),fixture.token)
 const page=await context.newPage();page.setDefaultTimeout(15000)
 const errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.goto(base+'/uniapp/#/pages/design/index')
 await page.locator('.stepper uni-button').filter({hasText:'配策略'}).click()
 await page.locator('.rule-tabs uni-button').filter({hasText:ruleName}).click()
 await page.locator('.row-actions uni-button').filter({hasText:'编辑',exact:true}).first().click()
 await page.getByLabel('条件 2 连接方式',{exact:true}).selectOption('OR')
 await page.getByLabel('条件 3 连接方式',{exact:true}).selectOption('AND')
 await page.locator('.row-editor uni-button').filter({hasText:'时间条件'}).click()
 const timeEditor=page.locator('.row-editor .editor-section').nth(2)
 await timeEditor.locator('.condition-editor input').nth(0).fill('08:00')
 await timeEditor.locator('.condition-editor input').nth(1).fill('09:00')
 const saveResponse=page.waitForResponse(response=>response.url().endsWith(`/rules/${created.body.id}/draft`)&&response.request().method()==='PUT')
 await page.locator('.publish-bar uni-button').filter({hasText:'保存草稿',exact:true}).click()
 assert.equal((await saveResponse).status(),200)
 await page.reload();await page.locator('.stepper uni-button').filter({hasText:'配策略'}).click();await page.locator('.rule-tabs uni-button').filter({hasText:ruleName}).click()
 assert.match(await page.locator('.rules-table').innerText(),/从左到右/)
 const snapshot=(await api('/snapshot')).body
 const saved=snapshot.nodes.find(node=>node.id===created.body.id)
 const draft=JSON.parse(saved.points.find(point=>point.type==='draft').text)
 assert.deepEqual(draft.table.rows[0].conditions.filter(condition=>condition.kind!=='time').map(condition=>condition.join),[undefined,'OR','AND'])
 assert.equal(draft.table.rows[0].conditions.find(condition=>condition.kind==='time').end,'09:00')
 const simLabels=page.locator('.simulation-grid uni-label')
 const set=async(name,value)=>simLabels.filter({hasText:`Mixed ${name}`}).locator('input').fill(String(value))
 await simLabels.filter({hasText:'条件已持续'}).locator('input').fill('1')
 await simLabels.filter({hasText:'模拟时刻'}).locator('input').fill('2026-11-25T08:30')
 const simulate=async row=>{await page.locator('.simulation uni-button').filter({hasText:'开始模拟',exact:true}).click();await page.locator('.sim-result').filter({hasText:`命中第 ${row} 行`}).waitFor()}
 await set('A',1);await set('B',0);await set('C',0);await simulate(2)
 await set('A',0);await set('B',1);await set('C',1);await simulate(1)
 await simLabels.filter({hasText:'模拟时刻'}).locator('input').fill('2026-11-25T12:30');await simulate(2)
 const publication=page.waitForResponse(response=>response.url().endsWith(`/rules/${created.body.id}/publish`)&&response.request().method()==='POST')
 await page.locator('.publish-bar uni-button').filter({hasText:'发布当前规则',exact:true}).click();await page.locator('.publish-confirm uni-button').filter({hasText:'确认发布',exact:true}).click()
 assert.equal((await publication).status(),200)
 assert.equal((await(await fetch(base+'/__fixture')).json()).writes,0)
 assert.deepEqual(errors,[])
 console.log('PASS actual mixed join controls/save/reload: (1 OR0) AND0 -> row2, (0 OR1) AND1 -> row1; publish disabled; writes0')
}finally{await browser.close()}
