import test from 'node:test'
import assert from 'node:assert/strict'

// New module instance stands for reload; no storage/network dependency is allowed.
test('preview inbox is explicit-read, local, bounded, and reset on reload', async () => {
  globalThis.uni = new Proxy({}, { get() { throw new Error('preview accessed live API/token/storage') } })
  globalThis.fetch = () => { throw new Error('preview used network') }
  const { resolveNotificationPreview: resolve } = await import('./notifications.ts?first')
  const path = '/notifications?limit=1'
  const first = resolve(path, 'GET')
  assert.equal(first.handled, true)
  assert.equal(first.data.items.length, 1)
  assert.equal(first.data.items[0].read_at, null)
  const id = first.data.items[0].id
  assert.equal(resolve(path, 'GET').data.items[0].read_at, null, 'opening is not reading')
  assert.equal(resolve(`/notifications/${id}/read`, 'POST', {}).status, undefined)
  const at = resolve(path, 'GET').data.items[0].read_at
  assert.ok(at)
  resolve(`/notifications/${id}/read`, 'POST', {})
  assert.equal(resolve(path, 'GET').data.items[0].read_at, at)
  assert.equal(resolve('/notifications?user_id=someone', 'GET').status, 400)
  assert.equal(resolve('/notifications/missing/read', 'POST', {}).status, 403)
  assert.equal(resolve('/notifications', 'POST', {}).handled, false)
  const fresh = await import('./notifications.ts?reload')
  assert.equal(fresh.resolveNotificationPreview(path, 'GET').data.items[0].read_at, null)
  delete globalThis.uni
})
