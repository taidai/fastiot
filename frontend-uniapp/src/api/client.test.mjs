import test from 'node:test'
import assert from 'node:assert/strict'
import { build } from 'esbuild'
import { fileURLToPath } from 'node:url'

// Bundle the actual application modules, including the preview resolver. Only
// uni's host I/O is substituted; no API client, store or resolver is mocked.
let generation = 0
async function application(preview = false) {
  globalThis.location = new URL(`http://localhost/uniapp/${preview ? '?preview=1' : ''}`)
  const storage = new Map([['fastems-token', 'live-session']])
  const storageReads = []
  const redirects = []
  const requests = []
  const listeners = new Map()
  globalThis.uni = {
    $on(event, listener) { listeners.set(event, listener) },
    $emit(event) { listeners.get(event)?.() },
    getStorageSync(key) { storageReads.push(key); return storage.get(key) || '' },
    setStorageSync(key, value) { storage.set(key, value) },
    removeStorageSync(key) { storage.delete(key) },
    redirectTo(options) { redirects.push(options.url) },
    request(options) { requests.push(options) },
  }
  const result = await build({
    stdin: { contents: 'export * from "./client.ts"; export * from "../stores/session.ts"', resolveDir: fileURLToPath(new URL('.', import.meta.url)), loader: 'ts' },
    bundle: true, write: false, platform: 'node', format: 'esm',
  })
  const api = await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text + `\n//# sourceURL=client-test-${generation++}.mjs`).toString('base64')}`)
  return { api, storage, storageReads, redirects, requests }
}

async function pendingRequest(app, path = '/snapshot') {
  const promise = app.api.request(path)
  // Attach a handler immediately: rejected concurrent requests must be observed.
  promise.catch(() => {})
  while (!app.requests.length) await new Promise(resolve => setImmediate(resolve))
  return { promise, request: app.requests.shift() }
}

test('preview login, reads, writes and logout never touch live credentials or HTTP', async () => {
  const app = await application(true)
  assert.equal(app.api.session.token, '')
  await app.api.login('preview', 'inert-fixture')
  await app.api.request('/snapshot')
  await assert.rejects(app.api.request('/unsupported-physical-write', 'POST', {}), { status: 501 })
  app.api.logout()
  assert.equal(app.storage.get('fastems-token'), 'live-session')
  assert.deepEqual(app.storageReads, [])
  assert.deepEqual(app.requests, [])
})

test('concurrent 401s clear memory and storage once; late responses cannot restore the session', async () => {
  const app = await application()
  app.api.session.snapshot = { nodes: [{ id: 'private-object' }] }
  app.api.session.values = [{ point_id: 'private-value' }]
  const first = await pendingRequest(app)
  const second = await pendingRequest(app)
  const late = await pendingRequest(app)
  first.request.success({ statusCode: 401, data: { error: 'unauthorized' } })
  second.request.success({ statusCode: 401, data: { error: 'unauthorized' } })
  late.request.success({ statusCode: 200, data: { nodes: [{ id: 'private-object' }] } })
  await assert.rejects(first.promise, { status: 401 })
  await assert.rejects(second.promise, { status: 401 })
  await assert.rejects(late.promise, { code: 'session_changed' })
  assert.equal(app.storage.has('fastems-token'), false)
  assert.equal(app.api.session.token, '')
  assert.equal(app.api.session.snapshot, null)
  assert.deepEqual(app.api.session.values, [])
  assert.deepEqual(app.redirects, ['/pages/login/index'])
})

test('failed live login is visible, unauthenticated and does not redirect or substitute a preview token', async () => {
  const app = await application()
  const promise = app.api.login('operator', 'wrong-fixture')
  const request = app.requests.shift()
  assert.equal(request.url, '/api/login')
  assert.equal(request.header.Authorization, undefined)
  request.success({ statusCode: 401, data: { error: 'invalid_credentials' } })
  await assert.rejects(promise, { status: 401, code: 'invalid_credentials' })
  assert.equal(app.storage.get('fastems-token'), 'live-session')
  assert.deepEqual(app.redirects, [])
})

test('polling reports failures and stops on expiry without unhandled rejections', async context => {
  context.mock.timers.enable({ apis: ['setInterval'] })
  const app = await application()
  context.after(() => app.api.stopPolling())
  app.api.startPolling()
  const replies = {
    '/api/snapshot': { nodes: [], roots: [], user: {} },
    '/api/ems/state': { values: [], evaluated_at: 'fixture-time' },
    '/api/ems/quality': { channels: [], devices: [] },
    '/api/ems/alarms': { alarms: [] },
  }
  app.requests.splice(0).forEach(request => request.success({ statusCode: 200, data: replies[request.url] }))
  await new Promise(resolve => setImmediate(resolve))
  context.mock.timers.tick(2000)
  app.requests.splice(0).forEach(request => request.success({ statusCode: 403, data: { error: 'forbidden' } }))
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(app.api.session.error, '当前账号无权执行此操作')
  context.mock.timers.tick(2000)
  app.requests.splice(0).forEach(request => request.success({ statusCode: 401, data: { error: 'expired' } }))
  await new Promise(resolve => setImmediate(resolve))
  context.mock.timers.tick(4000)
  assert.equal(app.requests.length, 0)
  assert.deepEqual(app.redirects, ['/pages/login/index'])
})

test('live failures stay failures and an old 401 cannot revoke a newer login', async () => {
  const app = await application()
  for (const status of [403, 500]) {
    const { promise, request } = await pendingRequest(app)
    request.success({ statusCode: status, data: { error: 'denied' } })
    await assert.rejects(promise, { status })
    assert.equal(app.storage.get('fastems-token'), 'live-session')
  }
  const offline = await pendingRequest(app)
  offline.request.fail(new Error('offline'))
  await assert.rejects(offline.promise, /offline/)
  const old = await pendingRequest(app)
  const login = app.api.login('operator', 'valid-fixture')
  app.requests.shift().success({ statusCode: 200, data: { token: 'new-live-session' } })
  assert.equal(await login, 'new-live-session')
  old.request.success({ statusCode: 401, data: { error: 'expired' } })
  await assert.rejects(old.promise, { status: 401 })
  assert.equal(app.storage.get('fastems-token'), 'new-live-session')
  assert.deepEqual(app.redirects, [])
})
