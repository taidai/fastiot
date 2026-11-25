import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const base = 'http://127.0.0.1:18087'
let browser, ready = false
try {
  let fixture
  const deadline = Date.now() + 90000
  for (;;) {
    try { fixture = await (await fetch(base + '/__fixture')).json(); if (fixture.fixture === 'ticket07-private-simulator') { ready = true; break } } catch {}
    if (Date.now() > deadline) throw new Error('isolated command fixture not ready')
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  page.setDefaultTimeout(12000)
  const errors = [], mutations = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('request', r => { if (r.url().includes('/api/') && r.method() !== 'GET') mutations.push(r.url()) })
  await page.addInitScript(token => { if (location.protocol === 'http:') localStorage.setItem('fastems-token', token) }, fixture.token)
  const open = async () => {
    await page.locator('.ledger .node').filter({ hasText: '操作模拟设备' }).click()
    await page.locator('.open-operations').click()
    await page.locator('.command-button').waitFor()
  }
  await page.goto(base + '/uniapp/#/pages/overview/index')
  await open()
  const drawerBounds = await page.locator('.operation-drawer').boundingBox()
  assert.ok(drawerBounds && drawerBounds.x >= 800 && drawerBounds.y <= 20 && drawerBounds.x + drawerBounds.width <= 1280, `drawer must anchor to viewport, not filtered screen block: ${JSON.stringify(drawerBounds)}`)
  assert.equal(await page.locator('.write-submit').count(), 0, 'drawer must not expose generic write bypass')
  await page.locator('.parameter-row input').fill('20')
  await page.locator('.command-button').click()
  assert.equal(mutations.length, 0, 'opening confirmation must not dispatch')
  await page.locator('.operation-confirm').click()
  await page.locator('.operation-result').filter({ hasText: '已回读' }).waitFor()
  const state = await (await fetch(base + '/__fixture')).json()
  assert.deepEqual(state.writes, [[1, 10, 20]], 'only the authorized declared write reaches the private simulator')
  await page.locator('.operation-records').filter({hasText:'已回读'}).waitFor()
  assert.match(await page.locator('.operation-records').innerText(), /已回读/)
  await page.reload()
  await open()
  await page.locator('.operation-records').filter({hasText:'已回读'}).waitFor()
  assert.match(await page.locator('.operation-records').innerText(), /已回读/)
  await page.locator('.manual-toggle').first().click()
  await page.locator('.operation-confirm').click()
  await page.locator('.operation-result').filter({ hasText: '已人工接管' }).waitFor()
  const afterTakeover = mutations.length
  await page.reload()
  await open()
  assert.equal(mutations.length, afterTakeover, 'reload/navigation must not resume automatic control')
  await page.locator('.manual-toggle').filter({ hasText: '恢复自动' }).click()
  await page.locator('.operation-confirm').click()
  await page.locator('.operation-result').filter({ hasText: '已恢复自动' }).waitFor()
  assert.deepEqual((await (await fetch(base + '/__fixture')).json()).writes, [[1,10,20]], 'mode changes must not write physical outputs')
  await fetch(base + '/__uncertain', {method:'POST'})
  await page.locator('.parameter-row input').fill('25')
  await page.locator('.command-button').click()
  await page.locator('.operation-confirm').click()
  await page.locator('.operation-result').filter({hasText:'结果未知'}).waitFor()
  const retainedID = await page.evaluate(channel => sessionStorage.getItem(`operation-request:${channel}/1`), fixture.channel)
  assert.ok(retainedID, '503 must retain the request identity')
  const commandURL = `${base}/api/modbus/${fixture.channel}/units/1/commands/${retainedID}`
  const headers = {Authorization:`Bearer ${fixture.token}`, 'Content-Type':'application/json'}
  let uncertain
  const until = Date.now() + 5000
  do {
    uncertain = await (await fetch(commandURL,{headers})).json()
    if (uncertain.state === 'unknown') break
    await new Promise(resolve => setTimeout(resolve,100))
  } while (Date.now() < until)
  assert.equal(uncertain.state,'unknown')
  assert.equal(uncertain.reason,'command_journal_unavailable')
  const beforeQuery = mutations.length
  await page.locator('.query-command').click()
  await page.locator('.operation-result').filter({hasText:'结果未知'}).waitFor()
  assert.equal(mutations.length,beforeQuery,'query must not submit again')
  const retry = async () => {
    const response = await fetch(commandURL.substring(0,commandURL.lastIndexOf('/')),{method:'POST',headers,body:JSON.stringify({request_id:retainedID,command_id:'set',parameters:{target:25}})})
    assert.equal(response.status,200)
    assert.equal((await response.json()).state,'unknown')
  }
  await retry()
  await page.goto('about:blank')
  await fetch(base + '/__restart_commands',{method:'POST'})
  await retry()
  await page.goto(base + '/uniapp/#/pages/overview/index')
  await open()
  await page.locator('.operation-records').filter({hasText:'结果未知'}).waitFor()
  await page.locator('.query-command').click()
  await page.locator('.operation-result').filter({hasText:'结果未知'}).waitFor()
  const finalWrites = (await (await fetch(base + '/__fixture')).json()).writes
  assert.deepEqual(finalWrites,[[1,10,20],[1,10,25]],'lost response, journal failure, retries and restored intent must add only one physical action')
  for (const width of [1440,1280,1024,375]) {
    await page.setViewportSize({width,height:800})
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),true,`overflow at ${width}`)
  }
  await page.setViewportSize({width:1280,height:800})
  const evidence = new URL('../../../../.scratch/', import.meta.url)
  await mkdir(evidence, {recursive:true})
  await page.screenshot({path:fileURLToPath(new URL('ticket07-operation.png', evidence))})
  assert.deepEqual(errors,[])
  console.log(JSON.stringify({ok:true,mutations,simulatorWrites:finalWrites}))
} finally {
  if (browser) await browser.close()
  if (ready) await fetch(base + '/__finish',{method:'POST'}).catch(()=>{})
}
