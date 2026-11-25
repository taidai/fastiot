import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'

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
  page.on('response', async response => { if (response.url().includes('/commands')) console.log('[DEBUG-ticket07-browser]', response.status(), await response.text()) })
  page.on('pageerror', e => errors.push(e.message))
  page.on('request', r => { if (r.url().includes('/api/') && r.method() !== 'GET') mutations.push(r.url()) })
  await page.addInitScript(token => localStorage.setItem('fastems-token', token), fixture.token)
  const open = async () => {
    await page.locator('.ledger .node').filter({ hasText: '操作模拟设备' }).click()
    await page.locator('.open-operations').click()
    await page.locator('.command-button').waitFor()
  }
  await page.goto(base + '/uniapp/#/pages/overview/index')
  await open()
  assert.equal(await page.locator('.write-submit').count(), 0, 'drawer must not expose generic write bypass')
  await page.locator('.parameter-row input').fill('20')
  await page.locator('.command-button').click()
  assert.equal(mutations.length, 0, 'opening confirmation must not dispatch')
  await page.locator('.operation-confirm').click()
  await page.locator('.operation-result').filter({ hasText: '已回读' }).waitFor()
  const state = await (await fetch(base + '/__fixture')).json()
  assert.deepEqual(state.writes, [[1, 10, 20]], 'only the authorized declared write reaches the private simulator')
  assert.match(await page.locator('.operation-records').innerText(), /已回读/)
  await page.reload()
  await open()
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
  for (const width of [1440,1280,1024,375]) {
    await page.setViewportSize({width,height:800})
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),true,`overflow at ${width}`)
  }
  assert.deepEqual(errors,[])
  console.log(JSON.stringify({ok:true,mutations,simulatorWrites:state.writes}))
} finally {
  if (browser) await browser.close()
  if (ready) await fetch(base + '/__finish',{method:'POST'}).catch(()=>{})
}
