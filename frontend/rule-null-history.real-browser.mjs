// Legitimate API nil-array history must remain readable without rewriting it.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'
const base=process.env.RULE_FIXTURE_URL||'http://127.0.0.1:18023'
const fixture=await(await fetch(base+'/__fixture')).json()
assert.equal(fixture.fixture,'ticket13-private-simulator');assert.equal(fixture.writes,0)
const api=async(path,method='GET',body)=>{const response=await fetch(base+'/api'+path,{method,headers:{'Content-Type':'application/json',Authorization:`Bearer ${fixture.token}`},body:body===undefined?undefined:JSON.stringify(body)});return{status:response.status,body:await response.json()}}
const name=`Null rows history ${Date.now()}`
const document={scope:'self',table:{rows:null},bindings:{points:{},actions:{}}}
const created=await api('/rules','POST',{parent:fixture.root,description:name,document});assert.equal(created.status,200);document.revision=created.body.revision
assert.equal((await api(`/rules/${created.body.id}/publish`,'POST',document)).status,200)
const nilRevision=document.revision
const nilDocument=JSON.parse(JSON.stringify(document))
document.table.rows=[{row_id:'populated',conditions:[],actions:[]}]
const saved=await api(`/rules/${created.body.id}/draft`,'PUT',document);assert.equal(saved.status,200);document.revision=saved.body.revision
assert.equal((await api(`/rules/${created.body.id}/publish`,'POST',document)).status,200)
const readRule=async()=>(await api('/snapshot')).body.nodes.find(node=>node.id===created.body.id)
const point=(node,type)=>node.points.find(item=>item.type===type&&!(item.tombstone%2))
const browser=await chromium.launch({channel:process.env.BROWSER_CHANNEL||'msedge',headless:true})
try{
 const context=await browser.newContext({viewport:{width:1440,height:900}})
 await context.addInitScript(token=>{sessionStorage.setItem('fastems-token',token);localStorage.setItem('fastems-workbench','delivery')},fixture.token)
 const page=await context.newPage();page.setDefaultTimeout(15000)
 const errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.goto(base);await page.locator('.console-topnav button').filter({hasText:'策略',exact:true}).click()
 await page.locator('.rule-tabs button').filter({hasText:name,exact:true}).click()
 await page.locator('.rule-editor h2').filter({hasText:name,exact:true}).waitFor()
 await page.getByRole('button',{name:'版本与历史',exact:true}).click()
 await page.locator('.version-line').filter({hasText:'0 行'}).waitFor()
 const restoredResponse=page.waitForResponse(response=>response.url().endsWith(`/rules/${created.body.id}/restore`)&&response.request().method()==='POST')
 await page.locator('.version-line').filter({hasText:'0 行'}).getByRole('button',{name:'恢复到草稿',exact:true}).click()
 assert.equal((await restoredResponse).status(),200)
 await page.locator('.rule-state').filter({hasText:'0 行'}).waitFor()
 assert.equal(await page.locator('.rule-editor').count(),1)
 assert.deepEqual(errors,[])
 const current=await readRule()
 assert.equal(JSON.parse(point(current,'draft').text).table.rows,null,'restore must retain saved null wire representation')
 const history=current.points.filter(item=>item.type==='history').map(item=>JSON.parse(item.text)).find(item=>item.id===nilRevision)
 assert.deepEqual(history.document,nilDocument,'opening/restoring history must not rewrite immutable source')
 assert.equal((await(await fetch(base+'/__fixture')).json()).writes,0)
 console.log('PASS real React null-row publication history opens/counts/restores; exact stored null wire unchanged; pageErrors0; writes0')
}finally{await browser.close()}
