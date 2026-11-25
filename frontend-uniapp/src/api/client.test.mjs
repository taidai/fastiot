import test from 'node:test'
import assert from 'node:assert/strict'
import { build } from 'esbuild'
import { fileURLToPath } from 'node:url'

// Bundle the actual application modules, including the preview resolver. Only
// uni's host I/O is substituted; no API client, store or resolver is mocked.
let generation = 0
async function application(preview = false, storage = new Map([['fastems-token', 'live-session']])) {
  globalThis.location = new URL(`http://localhost/uniapp/${preview ? '?preview=1' : ''}`)
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
    request(options) {
      if (options.url === '/api/projects') return options.success({ statusCode: 200, data: { user_id: 'fixture-user', projects: [
        { id: 'project-a', name: 'Project A', root_id: 'project-a', scope_root_ids: ['project-a'], sites: [] },
        { id: 'project-b', name: 'Project B', root_id: 'project-b', scope_root_ids: ['project-b'], sites: [] },
      ] } })
      requests.push(options)
    },
  }
  const result = await build({
    stdin: { contents: 'export * from "./client.ts"; export * from "../stores/session.ts"; export * from "../stores/projects.ts"', resolveDir: fileURLToPath(new URL('.', import.meta.url)), loader: 'ts' },
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

test('project selection fences late reads and writes and persists only authorized context', async () => {
  const app = await application()
  const old = await pendingRequest(app)
  assert.equal(old.request.header['X-Fastiot-Project'], 'project-a')
  const write = app.api.request('/nodes/a/screen', 'PUT', { version: 2 })
  write.catch(() => {})
  await new Promise(resolve => setImmediate(resolve))
  const oldWrite = app.requests.shift()
  assert.equal(oldWrite.header['X-Fastiot-Project'], 'project-a')
  assert.deepEqual(oldWrite.data, { version: 2 })
  await app.api.selectProject('project-b', async () => true)
  old.request.success({ statusCode: 200, data: { roots: ['project-a'], nodes: [] } })
  oldWrite.success({ statusCode: 200, data: { version: 3 } })
  await assert.rejects(old.promise, { code: 'project_context_changed' })
  await assert.rejects(write, { code: 'project_context_changed' })
  const current = await pendingRequest(app)
  assert.equal(current.request.header['X-Fastiot-Project'], 'project-b')
  current.request.success({ statusCode: 200, data: {} })
  await current.promise
  assert.equal(app.api.projectContext.id, 'project-b')
  assert.equal(await app.api.selectProject('not-authorized', async () => true), false)
  assert.equal(await app.api.selectProject('project-a', async () => false), false)
  assert.equal(app.api.projectContext.id, 'project-b')
})

test('refresh restores an authorized project and layer, and project change clears scoped data', async () => {
  const storage = new Map([['fastems-token', 'live-session'], ['fastiot-project-context:fixture-user', { project: 'project-b', layer: 'b-layer' }]])
  const app = await application(false, storage)
  await app.api.ensureProjectContext()
  assert.equal(app.api.projectContext.id, 'project-b')
  app.api.session.snapshot = { roots: ['project-b'], nodes: [{ id: 'b-layer' }] }
  app.api.session.layer = 'b-layer'
  app.api.session.values = [{ point_id: 'b-point' }]
  await app.api.selectProject('project-a', async () => true)
  assert.equal(app.api.session.snapshot, null)
  assert.equal(app.api.session.layer, '')
  assert.deepEqual(app.api.session.values, [])
  const reloaded = await application(false, storage)
  await reloaded.api.ensureProjectContext()
  assert.equal(reloaded.api.projectContext.id, 'project-a')
  assert.equal(reloaded.api.projectContext.layer, '')
})

test('returning to a project does not revive old responses or swallow revision conflicts', async () => {
  const app = await application()
  const old = await pendingRequest(app)
  await app.api.selectProject('project-b', async () => true)
  await app.api.selectProject('project-a', async () => true)
  old.request.success({ statusCode: 200, data: { stale: true } })
  await assert.rejects(old.promise, { code: 'project_context_changed' })
  const save = app.api.request('/ems/screen/layer-a', 'PUT', { layer: 'layer-a', version: 1 })
  save.catch(() => {})
  await new Promise(resolve => setImmediate(resolve))
  const outgoing = app.requests.shift()
  assert.equal(outgoing.header['X-Fastiot-Project'], 'project-a')
  assert.equal(outgoing.data.version, 1)
  outgoing.success({ statusCode: 409, data: { error: 'screen_version_conflict' } })
  await assert.rejects(save, { code: 'screen_version_conflict', status: 409 })
  const binary = app.api.request('/delivery/evidence', 'GET', undefined, 'arraybuffer')
  binary.catch(() => {})
  await new Promise(resolve => setImmediate(resolve))
  const download = app.requests.shift()
  assert.equal(download.responseType, 'arraybuffer')
  await app.api.selectProject('project-b', async () => true)
  download.success({ statusCode: 200, data: new ArrayBuffer(4) })
  await assert.rejects(binary, { code: 'project_context_changed' })
})

test('all workbench badges count unacknowledged conditions, not acknowledged or recovered occurrences', async () => {
  const app = await application()
  app.api.session.alarms = [
    { id: 'pending', active: true, state: 'active' },
    { id: 'owned', active: true, state: 'acknowledged' },
    { id: 'past', active: false, state: 'resolved' },
  ]
  assert.deepEqual(app.api.pendingAlarms.value.map(alarm => alarm.id), ['pending'])
})

test('unavailable alarm query clears stale pending count instead of showing a current badge', async () => {
  const app = await application()
  await app.api.ensureProjectContext()
  app.api.session.alarms = [{ id: 'old', active: true, state: 'active' }]
  const refresh = app.api.refreshAll()
  await new Promise(resolve => setImmediate(resolve))
  for (const req of app.requests.splice(0)) {
    if (req.url === '/api/ems/alarms') req.success({ statusCode: 503, data: { error: 'alarm_evidence_unconfirmed' } })
    else req.success({ statusCode: 200, data: req.url === '/api/snapshot' ? { roots: [], nodes: [] } : { values: [], channels: [], devices: [] } })
  }
  await refresh
  assert.deepEqual(app.api.session.alarms, [])
  assert.match(app.api.session.runtimeError, /alarm_evidence_unconfirmed/)
})

test('refresh validates persisted layer against the authorized snapshot', async () => {
  for (const savedLayer of ['b-layer', 'a-layer']) {
    const app = await application(false, new Map([['fastems-token', 'live-session'], ['fastiot-project-context:fixture-user', { project: 'project-b', layer: savedLayer }]]))
    const refresh = app.api.refreshAll()
    await new Promise(resolve => setImmediate(resolve))
    const responses = {
      '/api/snapshot': { roots: ['project-b'], nodes: [{ id: 'project-b' }, { id: 'b-layer', parent: 'project-b' }], user: {} },
      '/api/ems/state': { values: [], evaluated_at: '' }, '/api/ems/quality': { channels: [], devices: [] }, '/api/ems/alarms': { alarms: [] },
    }
    app.requests.splice(0).forEach(req => req.success({ statusCode: 200, data: responses[req.url] }))
    await refresh
    assert.equal(app.api.session.layer, savedLayer === 'b-layer' ? 'b-layer' : 'project-b')
    app.api.selectLayer('a-layer')
    assert.notEqual(app.api.session.layer, 'a-layer')
  }
})

test('directory failures remain visible and do not fetch unscoped business data', async () => {
  const app = await application()
  const paths = []
  globalThis.uni.request = options => { paths.push(options.url); options.success({ statusCode: 503, data: { error: 'catalog_offline' } }) }
  await app.api.refreshAll()
  assert.equal(app.api.session.error, 'catalog_offline')
  assert.equal(app.api.session.loading, false)
  assert.equal(app.api.session.snapshot, null)
  assert.deepEqual(paths, ['/api/projects'])
})

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
  await new Promise(resolve => setImmediate(resolve))
  const replies = {
    '/api/snapshot': { nodes: [], roots: [], user: {} },
    '/api/ems/state': { values: [], evaluated_at: 'fixture-time' },
    '/api/ems/quality': { channels: [], devices: [] },
    '/api/ems/alarms': { alarms: [] },
  }
  app.requests.splice(0).forEach(request => request.success({ statusCode: 200, data: replies[request.url] }))
  await new Promise(resolve => setImmediate(resolve))
  context.mock.timers.tick(2000)
  await new Promise(resolve => setImmediate(resolve))
  app.requests.splice(0).forEach(request => request.success({ statusCode: 403, data: { error: 'forbidden' } }))
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(app.api.session.error, '无权读取实时数据：当前账号无权执行此操作')
  assert.deepEqual(app.api.session.values, [])
  assert.equal(app.api.session.lastSync, '')
  context.mock.timers.tick(2000)
  await new Promise(resolve => setImmediate(resolve))
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
