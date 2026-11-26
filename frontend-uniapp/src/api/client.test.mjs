import test from 'node:test'
import assert from 'node:assert/strict'
import { build } from 'esbuild'
import { fileURLToPath } from 'node:url'

// Bundle the actual application modules. Only uni's host I/O is substituted;
// no API client, store or resolver is mocked.
let generation = 0
async function application(storage = new Map([['fastems-token', 'live-session']])) {
  globalThis.location = new URL('http://localhost/uniapp/')
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
    stdin: { contents: 'export * from "./client.ts"; export * from "../stores/session.ts"; export * from "../stores/projects.ts"; export { watch } from "vue"', resolveDir: fileURLToPath(new URL('.', import.meta.url)), loader: 'ts' },
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

test('derived polling refreshes only observations and fails closed without replacing definitions', async () => {
  const app = await application()
  let tick
  const interval = globalThis.setInterval, clear = globalThis.clearInterval
  globalThis.setInterval = callback => { tick = callback; return 1 }
  globalThis.clearInterval = () => {}
  const snapshot = (value, definition = 'saved-config') => ({ roots: ['project-a'], nodes: [{ id: 'derived', parent: 'project-a', type: 'virtualInput', points: [{ type: 'easyemsInput', text: JSON.stringify({ value, quality: 'good' }) }, { type: 'easyemsScreen', text: definition }] }] })
  const respond = (requests, value, failSnapshot = false) => requests.forEach(request => request.success({ statusCode: failSnapshot && request.url === '/api/snapshot' ? 403 : 200, data: request.url === '/api/snapshot' ? snapshot(value, value === 1 ? 'saved-config' : 'different-server-definition') : { values: [], alarms: [], channels: [], devices: [], evaluated_at: '2026-11-25T10:00:00Z' } }))
  try {
    app.api.startPolling()
    await new Promise(resolve => setImmediate(resolve))
    respond(app.requests.splice(0), 1)
    await new Promise(resolve => setImmediate(resolve))
    tick()
    await new Promise(resolve => setImmediate(resolve))
    const poll = app.requests.splice(0)
    respond(poll, 2)
    await new Promise(resolve => setImmediate(resolve))
    assert.ok(poll.some(request => request.url === '/api/snapshot'), 'the existing runtime tick must refresh derived observations')
    const points = app.api.session.snapshot.nodes[0].points
    assert.equal(JSON.parse(points.find(point => point.type === 'easyemsInput').text).value, 2)
    assert.equal(points.find(point => point.type === 'easyemsScreen').text, 'saved-config')
    tick()
    await new Promise(resolve => setImmediate(resolve))
    respond(app.requests.splice(0), 3, true)
    await new Promise(resolve => setImmediate(resolve))
    assert.equal(app.api.session.snapshot.nodes[0].points.some(point => point.type === 'easyemsInput'), false)
    assert.match(app.api.session.runtimeError, /无权/)
    tick()
    await new Promise(resolve => setImmediate(resolve))
    const old = app.requests.splice(0)
    await app.api.selectProject('project-b', async () => true)
    respond(old, 99)
    await new Promise(resolve => setImmediate(resolve))
    assert.equal(app.api.session.snapshot, null)
  } finally {
    app.api.stopPolling()
    globalThis.setInterval = interval; globalThis.clearInterval = clear
  }
})

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
  const app = await application(storage)
  await app.api.ensureProjectContext()
  assert.equal(app.api.projectContext.id, 'project-b')
  app.api.session.snapshot = { roots: ['project-b'], nodes: [{ id: 'b-layer' }] }
  app.api.session.layer = 'b-layer'
  app.api.session.values = [{ point_id: 'b-point' }]
  await app.api.selectProject('project-a', async () => true)
  assert.equal(app.api.session.snapshot, null)
  assert.equal(app.api.session.layer, '')
  assert.deepEqual(app.api.session.values, [])
  const reloaded = await application(storage)
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
    const app = await application(new Map([['fastems-token', 'live-session'], ['fastiot-project-context:fixture-user', { project: 'project-b', layer: savedLayer }]]))
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

test('logout publishes one cleared context even when a synchronous subscriber reads with the old layer', async context => {
  context.mock.timers.enable({ apis: ['setInterval'] })
  const app = await application()
  context.after(() => app.api.stopPolling())
  app.api.startPolling()
  await new Promise(resolve => setImmediate(resolve))
  app.requests.splice(0).forEach(request => request.success({ statusCode: 200, data: {
    nodes: [{ id: 'layer-a' }], roots: ['layer-a'], values: [], channels: [], devices: [], alarms: [],
  } }))
  await new Promise(resolve => setImmediate(resolve))
  const outgoing = [], errors = []
  globalThis.uni.request = options => outgoing.push(options.url)
  // Like HandoverPanel: child props still carry the last rendered layer while
  // generation/session invalidation is synchronous, before Vue's queued render.
  const stop = app.api.watch(() => [app.api.projectContext.generation, app.api.session.token], () => {
    void app.api.request('/overview/handover?layer=layer-a').catch(error => errors.push(error))
  }, { flush: 'sync' })
  context.after(stop)
  const before = app.api.projectContext.generation
  app.api.logout()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(app.api.projectContext.generation - before, 1, 'logout must invalidate once without recursive reset')
  assert.deepEqual(outgoing, [], 'teardown must not start directory or business requests')
  assert.ok(errors.length > 0)
  assert.ok(errors.every(error => error.code === 'session_changed'), 'no stack overflow or stale reads')
  assert.equal(app.storage.has('fastems-token'), false)
  assert.equal(app.api.session.token, '')
  assert.equal(app.api.projectContext.id, '')
  assert.equal(app.api.projectContext.layer, '')
  assert.equal(app.api.session.snapshot, null)
  assert.equal(app.api.session.layer, '')
  assert.deepEqual(app.redirects, ['/pages/login/index'])
  context.mock.timers.tick(4000)
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(outgoing, [], 'logout stops polling')
})

test('context reset and activation publish a coherent owner, project and layer to synchronous readers', async context => {
  const app = await application()
  await app.api.ensureProjectContext()
  app.api.rememberLayer('layer-a')
  const paths = [], errors = [], published = []
  const send = uni.request
  uni.request = options => { paths.push(options.url); send(options) }
  const stop = app.api.watch(() => app.api.projectContext.generation, () => {
    published.push([app.api.projectContext.id, app.api.projectContext.layer])
    void app.api.request('/overview/handover?layer=layer-a').catch(error => errors.push(error))
  }, { flush: 'sync' })
  context.after(stop)
  const before = app.api.projectContext.generation
  app.api.resetProjectContext()
  assert.equal(app.api.projectContext.generation - before, 1, 'a reentrant reader must join the new directory load, not reset recursively')
  assert.deepEqual(published, [['', '']])
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(paths.filter(path => path === '/api/projects').length, 1)
  assert.deepEqual(published, [['', ''], ['project-a', 'layer-a']])
  app.requests.splice(0).forEach(request => request.success({ statusCode: 200, data: { handover: null } }))
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(errors, [])
  await app.api.selectProject('project-b', async () => true)
  assert.deepEqual(published.at(-1), ['project-b', ''])
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(app.requests[0].header['X-Fastiot-Project'], 'project-b')
  app.requests.shift().success({ statusCode: 200, data: { handover: null } })
})

test('login publishes the new credential before synchronous context readers resume', async context => {
  const app = await application()
  await app.api.ensureProjectContext()
  const credentials = [], errors = []
  const stop = app.api.watch(() => app.api.projectContext.generation, () => {
    credentials.push(app.storage.get('fastems-token'))
    void app.api.request('/overview/handover?layer=layer-a').catch(error => errors.push(error))
  }, { flush: 'sync' })
  context.after(stop)
  const login = app.api.login('operator', 'valid-fixture')
  app.requests.shift().success({ statusCode: 200, data: { token: 'new-live-session' } })
  assert.equal(await login, 'new-live-session')
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(credentials, ['new-live-session', 'new-live-session'])
  for (const request of app.requests.splice(0)) {
    assert.equal(request.header.Authorization, 'Bearer new-live-session')
    request.success({ statusCode: 200, data: { handover: null } })
  }
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(errors, [])
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

test('rollback API failures retain structured blockers and baseline recovery details', async () => {
  const app = await application()
  const promise = app.api.request('/projects/rollback', 'POST', { site: 'site-a', version: 'pv-old' })
  promise.catch(() => {})
  while (!app.requests.length) await new Promise(resolve => setImmediate(resolve))
  app.requests.shift().success({ statusCode: 409, data: {
    error: 'calculation_rollback_unsupported', retryable: true,
    blockers: [{ code: 'calculation_rollback_unsupported', scope: 'version', node_id: 'calc-23', node_type: 'calculation' }],
  } })
  await assert.rejects(promise, error => {
    assert.equal(error.code, 'calculation_rollback_unsupported')
    assert.equal(error.details.retryable, true)
    assert.equal(error.details.blockers[0].node_id, 'calc-23')
    return true
  })
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
