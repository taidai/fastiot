// Real browser -> built H5 -> Go HTTP -> isolated durable store.
// Start only ticket13-private-rule-fixture; never an existing deployment.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'

const base = 'http://127.0.0.1:18013'
const evidence = fileURLToPath(new URL('../../../../.scratch/ticket13/', import.meta.url))
await mkdir(evidence, { recursive: true })
const fixture = await (await fetch(base + '/__fixture')).json()
assert.equal(fixture.fixture, 'ticket13-private-simulator')
const api = async (path, method = 'GET', body) => {
 const response = await fetch(base + '/api' + path, { method, headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${fixture.token}` }, body: body === undefined ? undefined : JSON.stringify(body) })
 return { status: response.status, body: await response.json() }
}
const point = (node, type) => node.points.find(p => p.type === type && !(p.tombstone % 2))
const readRule = async () => (await api('/snapshot')).body.nodes.find(node => node.id === fixture.rule)
const before = await readRule()
const stale = JSON.parse(point(before, 'draft').text)
const browser = await chromium.launch({ channel: process.env.BROWSER_CHANNEL || 'msedge', headless: true })
try {
 const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
 const page = await context.newPage()
 const errors = []
 page.on('pageerror', error => errors.push(error.message))
 page.setDefaultTimeout(15000)
 await page.addInitScript(token => localStorage.setItem('fastems-token', token), fixture.token)
 await page.goto(base + '/uniapp/#/pages/design/index')
 await page.locator('.stepper uni-button').filter({ hasText: '配策略' }).click()
 await page.locator('.strategy').waitFor()
 assert.equal(await page.getByLabel('规则作用范围').inputValue(), 'descendants')
 await page.locator('.row-actions uni-button').filter({ hasText: '编辑', exact: true }).first().click()
 await page.locator('.timing-inputs input').first().fill('1.2')
 await page.locator('.publish-bar uni-button').filter({ hasText: '保存草稿', exact: true }).click()
 await page.locator('.notice').filter({ hasText: '草稿已保存' }).waitFor()
 const saved = await readRule()
 const savedDraft = JSON.parse(point(saved, 'draft').text)
 assert.notEqual(savedDraft.revision, stale.revision)
 assert.equal(savedDraft.table.rows[0].hold_ms, 1200)
 assert.equal(point(saved, 'applied').text, point(before, 'applied').text)
 assert.equal(point(saved, 'enabled')?.value || 0, 0)
 assert.equal((await api(`/rules/${fixture.rule}/draft`, 'PUT', stale)).status, 409)
 await page.locator('.simulation-grid label').filter({ hasText: '条件已持续' }).locator('input').fill('2')
 await page.locator('.simulation uni-button').filter({ hasText: '开始模拟', exact: true }).click()
 await page.locator('.sim-result').filter({ hasText: '命中第 1 行' }).waitFor()
 await page.reload()
 await page.locator('.stepper uni-button').filter({ hasText: '配策略' }).click()
 await page.locator('.rules-table').waitFor()
 assert.match(await page.locator('.rules-table').innerText(), /1.2s/)
 await page.locator('.publish-bar uni-button').filter({ hasText: '发布当前规则', exact: true }).click()
 await page.locator('.publish-confirm uni-button').filter({ hasText: '确认发布', exact: true }).click()
 await page.locator('.notice').filter({ hasText: '发布成功' }).waitFor()
 const published = await readRule()
 assert.equal(JSON.parse(point(published, 'applied').text).revision, savedDraft.revision)
 assert.equal(point(published, 'enabled')?.value || 0, 0)
 await page.screenshot({ path: evidence + 'rules-editor-desktop.png', fullPage: true })
 await page.locator('.design-ledger .node').filter({ hasText: 'Rule descendant' }).click()
 await page.locator('.panel-head .tag').filter({ hasText: '继承' }).waitFor()
 assert.equal(await page.getByLabel('规则作用范围').isDisabled(), true)
 await page.setViewportSize({ width: 375, height: 812 })
 await page.screenshot({ path: evidence + 'rules-inheritance-mobile.png', fullPage: true })
 assert.equal((await (await fetch(base + '/__fixture')).json()).writes, fixture.writes, 'save/publish/simulation never write devices')
 assert.deepEqual(errors, [])
 console.log('PASS real H5 save/reload/409/publish-separated/simulation-zero-writes/inherited-readonly; screenshots retained')
} finally {
 await browser.close()
 if (process.env.KEEP_RULE_FIXTURE !== '1') await fetch(base + '/__finish', { method: 'POST' })
}
