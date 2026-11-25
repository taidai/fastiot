// Run against TestNotificationBrowserFixture on the private loopback port only.
// The fixture proves durable creation + restart before it starts listening.
import assert from 'node:assert/strict'
import { chromium } from 'playwright-core'
const base = 'http://127.0.0.1:18131'
let browser
try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
const errors = []
async function login(email) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`${base}/uniapp/#/pages/login/index`)
  await page.locator('.login input[type="text"]').fill(email)
  await page.locator('.login input[type="password"]').fill('inbox-private-fixture')
  await page.locator('.login input[type="password"]').press('Enter')
  await page.locator('.inbox-trigger').waitFor()
  await page.locator('.inbox-trigger').focus()
  await page.locator('.inbox-trigger').press('Enter')
  await page.locator('.inbox-panel').waitFor()
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('aria-label')), '关闭站内通知')
  await page.keyboard.press('Escape')
  await page.locator('.inbox-panel').waitFor({ state: 'detached' })
  assert.equal(await page.evaluate(() => document.activeElement?.className), 'inbox-trigger')
  await page.locator('.inbox-trigger').press('Space')
  await page.locator('.inbox-panel').waitFor()
  return { context, page }
}
try {
  const { page } = await login('alice@example.test')
  await page.locator('.inbox-item').waitFor()
  assert.match(await page.locator('.inbox-state').innerText(), /未读/)
  assert.match(await page.locator('.inbox-body').innerText(), /<script>/)
  assert.equal(await page.evaluate(() => window.inboxInjected), undefined)
  await page.reload()
  await page.locator('.inbox-trigger').click()
  await page.locator('.inbox-item').waitFor()
  assert.match(await page.locator('.inbox-state').innerText(), /未读/, 'opening/reloading is not reading')
  const mutations = []
  page.on('request', request => { if (request.method() !== 'GET' && request.url().includes('/api/')) mutations.push(request.url()) })
  await page.locator('.inbox-item').getByText('标记已读', { exact: true }).press('Space')
  await page.waitForFunction(() => document.querySelector('.inbox-state')?.textContent.includes('已读'))
  assert.equal(mutations.filter(url => /\/notifications\/[^/]+\/read$/.test(url)).length, 1)
  assert.ok(mutations.every(url => url.includes('/notifications/')), 'read must not acknowledge alarms or commands')
  await page.reload()
  await page.locator('.inbox-trigger').click()
  await page.locator('.inbox-item').waitFor()
  assert.match(await page.locator('.inbox-state').innerText(), /已读/)
  for (const width of [1440, 1024, 375]) {
    await page.setViewportSize({ width, height: 900 })
    const bounds = await page.locator('.inbox-panel').boundingBox()
    assert.ok(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width, `inbox overflow at ${width}`)
  }
  const { page: bob } = await login('bob@example.test')
  await bob.locator('.inbox-item').waitFor()
  assert.match(await bob.locator('.inbox-state').innerText(), /未读/, 'Alice read cannot change Bob')
  const { page: outside } = await login('outside@example.test')
  await outside.getByText('暂无当前有权查看的站内通知', { exact: true }).waitFor()
  assert.equal(await outside.locator('.inbox-item').count(), 0)

  // Use the real account-management API, not a fixture impersonation/backdoor.
  const adminLogin = await page.request.post(`${base}/api/login`, { data: { email: 'admin', password: 'inbox-private-fixture' } })
  assert.equal(adminLogin.status(), 200)
  const adminHeaders = { Authorization: `Bearer ${(await adminLogin.json()).token}` }
  const accountResponse = await page.request.get(`${base}/api/users`, { headers: adminHeaders })
  const accounts = (await accountResponse.json()).users
  const aliceAccount = accounts.find(account => account.email === 'alice@example.test')
  const outsideAccount = accounts.find(account => account.email === 'outside@example.test')
  const move = await page.request.put(`${base}/api/users/${aliceAccount.id}`, { headers: adminHeaders, data: { role: 'viewer', parent: outsideAccount.policy.root_node_ids[0] } })
  assert.equal(move.status(), 200)
  await page.reload()
  await page.locator('.inbox-trigger').click()
  await page.getByText('暂无当前有权查看的站内通知', { exact: true }).waitFor()
  const restore = await page.request.put(`${base}/api/users/${aliceAccount.id}`, { headers: adminHeaders, data: { role: 'viewer', parent: aliceAccount.policy.root_node_ids[0] } })
  assert.equal(restore.status(), 200)
  await page.reload()
  await page.locator('.inbox-trigger').click()
  await page.locator('.inbox-item').waitFor()
  assert.match(await page.locator('.inbox-state').innerText(), /已读/, 'scope restoration must retain history')

  const previewContext = await browser.newContext()
  const preview = await previewContext.newPage()
  const previewRequests = []
  preview.on('request', request => { if (request.url().includes('/api/')) previewRequests.push(request.url()) })
  await preview.goto(`${base}/uniapp/?preview=1#/pages/overview/index`)
  await preview.locator('.inbox-trigger').click()
  await preview.locator('.inbox-item').waitFor()
  assert.match(await preview.locator('.inbox-state').innerText(), /未读/)
  await preview.getByText('标记已读', { exact: true }).click()
  await preview.waitForFunction(() => document.querySelector('.inbox-state')?.textContent.includes('已读'))
  await preview.reload()
  await preview.locator('.inbox-trigger').click()
  await preview.locator('.inbox-item').waitFor()
  assert.match(await preview.locator('.inbox-state').innerText(), /未读/)
  assert.deepEqual(previewRequests, [], 'preview must not access live API')
  assert.equal(await preview.evaluate(() => localStorage.getItem('fastems-token')), null)
  assert.deepEqual(errors, [])
  console.log('PASS private real-Go inbox browser: actual login, Enter/Space operation, durable/restarted fixture, refresh, explicit own read, second account unread, outsider hidden, actual account-scope move/restore, literal text, 1440/1024/375; preview isolated and reset. Rule producer integration not tested here.')
} finally { await browser.close() }
