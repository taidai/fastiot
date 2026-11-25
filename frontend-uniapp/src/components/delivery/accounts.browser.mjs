// Owns an ephemeral real-Go fixture: private copied-source volume, no device
// clients, no host data mount. Never point this check at an existing instance.
import assert from 'node:assert/strict'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'

const exec = promisify(execFile)
const name = `fastiot-ticket19-browser-${process.pid}`
const base = 'http://127.0.0.1:18099'
const password = 'ticket19-isolated-fixture'
const fixture = spawn('docker', ['run', '--rm', '--name', name, '-p', '127.0.0.1:18099:18099', '-v', 'fastiot-ticket19-source:/src', '-v', 'fastiot-go-build:/root/.cache/go-build', '-v', 'easyems-go-mod:/go/pkg/mod', '-w', '/src', '-e', 'FASTIOT_ACCOUNT_BROWSER=1', 'golang:1.27.3', 'go', 'test', './internal/appapi', '-run', '^TestAccountsBrowserFixture$', '-v', '-count=1', '-timeout=10m'])
let logs = '', browser
fixture.stdout.on('data', chunk => { logs += chunk })
fixture.stderr.on('data', chunk => { logs += chunk })
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
const errors = []
async function login(context, email) {
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`${base}/uniapp/#/pages/login/index`)
  await page.locator('.login input[type="text"]').fill(email)
  await page.locator('.login input[type="password"]').fill(password)
  await page.locator('.login input[type="password"]').press('Enter')
  await page.waitForFunction(() => !!localStorage.getItem('fastems-token'))
  await page.locator('.login').waitFor({ state: 'detached' })
  await page.goto(`${base}/uniapp/#/pages/delivery/index`)
  await page.locator('.side').getByText('账号与权限', { exact: true }).click().catch(async error => { console.error(await page.locator('body').innerText(), errors); throw error })
  await page.locator('.account-table tbody tr').first().waitFor()
  return page
}
try {
  for (let attempt = 0; attempt < 120; attempt++) {
    if (fixture.exitCode !== null) throw new Error(logs)
    if (logs.includes('isolated account browser fixture ready')) break
    if (attempt === 119) throw new Error(`fixture startup timeout\n${logs}`)
    await pause(1000)
  }
  try { browser = await chromium.launch({ channel: 'chrome', headless: true }) }
  catch { browser = await chromium.launch({ channel: 'msedge', headless: true }) }
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  let adminBearer = ''
  context.on('request', request => { if (request.url().includes('/api/users')) adminBearer = request.headers().authorization || adminBearer })
  const page = await login(context, 'admin')
  const operator = page.locator('.permission-matrix tbody tr').filter({ hasText: 'operator@example.test' })
  await page.locator('.matrix-card select').selectOption({ label: 'Account fixture A' })
  await page.waitForFunction(() => [...document.querySelectorAll('.permission-matrix tbody tr')].find(row => row.textContent.includes('operator@example.test'))?.textContent.includes('授权允许'))
  assert.match(await operator.innerText(), /拒绝 · 动作未授权/)
  const token = await page.evaluate(() => localStorage.getItem('fastems-token'))
  const requests = []
  page.on('request', request => { if (request.url().includes('/api/')) requests.push({ method: request.method(), url: request.url() }) })
  await page.getByText('只读检查权限', { exact: true }).click()
  await page.waitForFunction(() => !document.body.innerText.includes('检查中…'))
  assert.equal(await page.evaluate(() => localStorage.getItem('fastems-token')), token)
  assert.ok(requests.some(request => request.url.includes('/authorization?')))
  assert.ok(requests.every(request => request.method === 'GET'), 'explanation must not send mutations')
  assert.equal(await page.getByText('以李工身份检查', { exact: true }).count(), 0, 'preview identity affordance must not appear live')

  await page.getByText('＋ 创建账号', { exact: true }).click()
  await page.locator('.create-editor input').nth(0).fill('browser@example.test')
  await page.locator('.create-editor input').nth(1).fill(password)
  await page.locator('.create-editor select').nth(1).selectOption({ label: 'Account fixture A' })
  await page.locator('.create-editor').getByText('创建账号', { exact: true }).click()
  const row = page.locator('.account-table tbody tr').filter({ hasText: 'browser@example.test' })
  await row.waitFor()
  await row.getByText('编辑权限', { exact: true }).click()
  const editor = page.locator('.editor').filter({ hasText: '编辑权限' })
  await editor.locator('select').nth(0).selectOption('implementer')
  await editor.locator('select').nth(1).selectOption({ label: 'Account fixture B' })
  await editor.getByText('设备命令', { exact: true }).click()
  await editor.getByText('保存权限', { exact: true }).click()
  await page.getByText('权限已保存', { exact: true }).waitFor()
  await page.reload()
  await page.locator('.side').getByText('账号与权限', { exact: true }).click()
  await row.waitFor()
  assert.match(await row.innerText(), /Account fixture B/)
  assert.match(await row.innerText(), /白名单 1 项/)
  await page.locator('.matrix-card select').selectOption({ label: 'Account fixture B' })
  await page.waitForFunction(() => [...document.querySelectorAll('.permission-matrix tbody tr')].find(row => row.textContent.includes('browser@example.test'))?.textContent.includes('授权允许'))
  const matrix = page.locator('.permission-matrix tbody tr').filter({ hasText: 'browser@example.test' })
  assert.match(await matrix.innerText(), /拒绝 · 动作未授权/)
  const outsideID = await page.locator('.matrix-card select').inputValue()
  const accountResponse = await page.request.get(`${base}/api/users`, { headers: { Authorization: adminBearer } })
  const accounts = (await accountResponse.json()).users
  const hiddenID = accounts.find(user => user.email === 'viewer@example.test').id
  const operatorID = accounts.find(user => user.email === 'operator@example.test').id
  await mkdir(new URL('../../../../.scratch/ticket19/', import.meta.url), { recursive: true })
  await page.screenshot({ path: fileURLToPath(new URL('../../../../.scratch/ticket19/accounts-1440.png', import.meta.url)), fullPage: true })
  for (const width of [1280, 1024, 375]) {
    await page.setViewportSize({ width, height: 900 })
    assert.ok(await page.locator('.matrix-card select').isVisible())
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)
    assert.equal(overflow, false, `page overflow at ${width}`)
  }

  const restrictedContext = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  let bearer = ''
  restrictedContext.on('request', request => { if (request.url().includes('/api/users')) bearer = request.headers().authorization || bearer })
  const restricted = await login(restrictedContext, 'manager@example.test')
  const accountText = await restricted.locator('.accounts-card').innerText()
  assert.ok(!accountText.includes('viewer@example.test') && !accountText.includes('browser@example.test') && !accountText.includes('Account fixture B'))
  const insideID = await restricted.locator('.matrix-card select').inputValue()
  for (const [userID, target] of [[hiddenID, insideID], [operatorID, outsideID], ['missing', insideID], [operatorID, 'missing']]) {
    const response = await restricted.request.get(`${base}/api/users/${userID}/authorization?target=${target}`, { headers: { Authorization: bearer } })
    assert.equal(response.status(), 403)
    assert.deepEqual(await response.json(), { error: 'forbidden' })
  }
  assert.deepEqual(errors, [])
  console.log('PASS real-Go browser: create, role/whitelist/scope edit, reload, server matrix, no impersonation/mutations, scoped account leakage denied; viewports 1440/1280/1024/375')
} catch (error) {
  console.error(logs)
  throw error
} finally {
  await browser?.close()
  await exec('docker', ['stop', '-t', '2', name]).catch(() => {})
}
