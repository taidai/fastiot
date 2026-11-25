// Actual rule editor -> authorized runtime -> durable scoped inbox, never outbound delivery.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'
const base=process.env.RULE_FIXTURE_URL||'http://127.0.0.1:18023'
const fixture=await(await fetch(base+'/__fixture')).json()
assert.equal(fixture.fixture,'ticket13-private-simulator');assert.equal(fixture.writes,0)
const suffix=String(Date.now()),password=`isolated-notice-${suffix}`
const api=async(path,method='GET',body,token=fixture.token)=>{const response=await fetch(base+'/api'+path,{method,headers:{'Content-Type':'application/json',Authorization:`Bearer ${token}`},body:body===undefined?undefined:JSON.stringify(body)});return{status:response.status,body:await response.json()}}
const accounts={}
for(const name of ['alice','bob','outside']){
 const email=`${name}-${suffix}@example.test`
 const created=await api('/users','POST',{parent:name==='outside'?fixture.child:fixture.root,email,password,role:'viewer'});assert.equal(created.status,200)
 const logged=await api('/login','POST',{email,password});assert.equal(logged.status,200)
 accounts[name]={id:created.body.id,email,token:logged.body.token}
}
const ruleName=`Rule notice ${suffix}`
const created=await api('/rules','POST',{parent:fixture.root,description:ruleName,document:{scope:'self',table:{rows:[{row_id:'notice',conditions:[],actions:[]}]},bindings:{points:{},actions:{}}}});assert.equal(created.status,200)
const browser=await chromium.launch({channel:process.env.BROWSER_CHANNEL||'msedge',headless:true})
const errors=[]
try{
 const context=await browser.newContext({viewport:{width:1440,height:900}})
 await context.addInitScript(token=>localStorage.setItem('fastems-token',token),fixture.token)
 const page=await context.newPage();page.setDefaultTimeout(15000);page.on('pageerror',error=>errors.push(error.message))
 await page.goto(base+'/uniapp/#/pages/design/index')
 await page.locator('.stepper uni-button').filter({hasText:'配策略'}).click()
 await page.locator('.rule-tabs uni-button').filter({hasText:ruleName}).click()
 await page.locator('.row-actions uni-button').filter({hasText:'编辑',exact:true}).click()
 await page.locator('.row-editor uni-button').filter({hasText:'＋ 站内通知',exact:true}).click()
 await page.getByLabel('通知标题',{exact:true}).locator('input').fill('Rule-created maintenance notice')
 await page.getByLabel('通知正文',{exact:true}).locator('input').fill('Literal <script> text, no outbound delivery')
 await page.locator('.notification-recipients uni-label').filter({hasText:accounts.alice.id}).waitFor()
 assert.equal(await page.locator('.notification-recipients uni-label').filter({hasText:accounts.outside.id}).count(),0)
 assert.equal(await page.locator('.notification-recipients .uni-checkbox-input-checked').count(),0,'no account is implicitly selected')
 await page.locator('.notification-recipients uni-label').filter({hasText:accounts.alice.id}).click()
 const saved=page.waitForResponse(response=>response.url().endsWith(`/rules/${created.body.id}/draft`)&&response.request().method()==='PUT')
 await page.locator('.publish-bar uni-button').filter({hasText:'保存草稿',exact:true}).click();assert.equal((await saved).status(),200)
 await page.reload();await page.locator('.stepper uni-button').filter({hasText:'配策略'}).click();await page.locator('.rule-tabs uni-button').filter({hasText:ruleName}).click()
 const snapshot=(await api('/snapshot')).body
 const rule=snapshot.nodes.find(node=>node.id===created.body.id)
 const document=JSON.parse(rule.points.find(point=>point.type==='draft').text)
 const action=document.table.rows[0].actions[0]
 assert.equal(action.kind,'notify');assert.deepEqual(action.notification.recipient_ids,[accounts.alice.id]);assert.equal('value' in action,false);assert.equal('readback' in action,false);assert.deepEqual(document.bindings.actions,{})
 await page.locator('.simulation uni-button').filter({hasText:'开始模拟',exact:true}).click();await page.locator('.sim-result').filter({hasText:'站内通知'}).waitFor()
 assert.equal((await api('/notifications','GET',undefined,accounts.alice.token)).body.items.length,0)
 const published=page.waitForResponse(response=>response.url().endsWith(`/rules/${created.body.id}/publish`)&&response.request().method()==='POST')
 await page.locator('.publish-bar uni-button').filter({hasText:'发布当前规则',exact:true}).click();await page.locator('.publish-confirm uni-button').filter({hasText:'确认发布',exact:true}).click();assert.equal((await published).status(),200)
 assert.equal((await api('/notifications','GET',undefined,accounts.alice.token)).body.items.length,0,'publication is not execution')
 await page.locator('.publish-bar uni-button').filter({hasText:'启用自动',exact:true}).click()
 await page.waitForFunction(async token=>{const response=await fetch('/api/notifications',{headers:{Authorization:`Bearer ${token}`}});return response.ok&&(await response.json()).items.length===1},accounts.alice.token)
 for(const name of ['bob','outside']) assert.equal((await api('/notifications','GET',undefined,accounts[name].token)).body.items.length,0)
 const aliceContext=await browser.newContext({viewport:{width:1440,height:900}})
 const alice=await aliceContext.newPage();alice.on('pageerror',error=>errors.push(error.message))
 await alice.goto(base+'/uniapp/#/pages/login/index');await alice.locator('.login input[type="text"]').fill(accounts.alice.email);await alice.locator('.login input[type="password"]').fill(password);await alice.locator('.login input[type="password"]').press('Enter')
 await alice.locator('.inbox-trigger').click();await alice.locator('.inbox-item').waitFor()
 assert.match(await alice.locator('.inbox-state').innerText(),/未读/)
 assert.match(await alice.locator('.inbox-body').innerText(),/Literal <script>/)
 await alice.locator('.inbox-item').getByText('标记已读',{exact:true}).click()
 await alice.waitForFunction(()=>document.querySelector('.inbox-state')?.textContent.includes('已读'))
 await alice.reload();await alice.locator('.inbox-trigger').click();await alice.locator('.inbox-item').waitFor();assert.match(await alice.locator('.inbox-state').innerText(),/已读/)
 assert.equal((await(await fetch(base+'/__fixture')).json()).writes,0);assert.deepEqual(errors,[])
 console.log('PASS actual rule notify editor/select/save/reload/simulate0/publish0/enable1 -> specified account unread -> explicit read/reload; nonrecipient and outside inbox empty; device writes0')
}finally{await browser.close()}
