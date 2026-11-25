import test from 'node:test'
import assert from 'node:assert/strict'

let sequence = 0
async function mode(href) {
  if (href) globalThis.location = new URL(href)
  else delete globalThis.location
  return (await import(`./source.ts?case=${sequence++}`)).PREVIEW_MODE
}

test('real backend is the default; preview requires an explicit document-level choice', async () => {
  assert.equal(await mode(), false)
  assert.equal(await mode('https://example.test/uniapp/'), false)
  assert.equal(await mode('https://example.test/uniapp/?live=1#/pages/login/index'), false)
  assert.equal(await mode('https://example.test/uniapp/?preview=1#/pages/design/index'), true)
  assert.equal(await mode('https://example.test/uniapp/?preview=0'), false)
  assert.equal(await mode('https://example.test/uniapp/#/pages/design/index?preview=1'), false)
})
