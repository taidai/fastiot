import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'
const base=process.env.RULE_FIXTURE_URL||'http://127.0.0.1:18043'
const fixture=await(await fetch(base+'/__fixture')).json();assert.equal(fixture.fixture,'ticket13-private-simulator');assert.equal(fixture.writes,0)
const api=async(path,method,body)=>{const response=await fetch(base+'/api'+path,{method,headers:{Authorization:`Bearer ${fixture.token}`,'Content-Type':'application/json'},body:JSON.stringify(body)});return{status:response.status,body:await response.json()}}
const suffix=Date.now(),name=`Existing allocator rule ${suffix}`,groupName=`Unsupported allocator ${suffix}`
const group=await api('/nodes','POST',{parent:fixture.root,type:'group',description:groupName});assert.equal(group.status,200)
const output=await api('/nodes','POST',{parent:group.body.id,type:'variable',description:'Allocator software member',value:0});assert.equal(output.status,200)
const config=await api(`/ems/allocators/${group.body.id}`,'PUT',{members:[{id:'member',target:{node_id:output.body.id},max_charge:10,max_discharge:10}]});assert.equal(config.status,200)
const document={scope:'descendants',table:{rows:[{row_id:'existing',conditions:[],actions:[{action_id:'allocator',value:1,readback:{timeout_ms:5000}}]}]},bindings:{points:{},actions:{allocator:{node_id:group.body.id}}}}
const created=await api('/rules','POST',{parent:fixture.root,description:name,document});assert.equal(created.status,200);document.revision=created.body.revision
const refused=await api(`/rules/${created.body.id}/publish`,'POST',document);assert.equal(refused.status,409);assert.match(refused.body.error,/unsupported_rule_adapter/)
const browser=await chromium.launch({channel:'msedge',headless:true})
try{
 const context=await browser.newContext();await context.addInitScript(token=>localStorage.setItem('fastems-token',token),fixture.token)
 const page=await context.newPage();await page.goto(base+'/uniapp/#/pages/design/index')
 await page.locator('.stepper uni-button').filter({hasText:'配策略'}).click();await page.locator('.rule-tabs uni-button').filter({hasText:name}).click();await page.locator('.row-actions uni-button').filter({hasText:'编辑',exact:true}).click()
 await page.getByText('现有动作适配尚不支持，参数只读且不可发布；可替换为已支持动作。',{exact:true}).waitFor()
 assert.equal(await page.getByLabel('输出值',{exact:true}).locator('input').isDisabled(),true)
 assert.equal(await page.locator('.publish-bar uni-button').filter({hasText:'发布当前规则',exact:true}).getAttribute('disabled'),'true')
 await page.locator('.row-editor uni-button').filter({hasText:'＋ 动作',exact:true}).click()
 const option=page.locator('.action-editor').last().locator('select[aria-label="输出点位"] option').filter({hasText:groupName+'（分配器暂不支持发布）'})
 assert.equal(await option.evaluate(element=>element.disabled),true,'new unsupported allocator option must be disabled')
 assert.equal((await(await fetch(base+'/__fixture')).json()).writes,0)
 console.log('PASS real legacy allocator retained/read-only; new allocator option disabled; publication blocked in UI and Go; device writes0')
}finally{await browser.close()}
