import assert from 'node:assert/strict'
import test from 'node:test'
import { clearSession, establishSession, loadSession, normalizeSession, saveSession } from '../src/services/session.ts'

function storage() {
    const values = new Map<string, string>([['unrelated-draft', 'keep me']])
    return { values, getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) } }
}
const input = { apiURL: 'http://localhost:5001/', uid: '001 user', token: 'existing-token' }

test('existing-token login never registers or overwrites a server credential', async () => {
    const store = storage()
    const session = await establishSession(store, input, false, async () => { assert.fail('credential registration is forbidden') })
    assert.deepEqual(loadSession(store), session)
    assert.equal(session.uid, input.uid)
    assert.equal(session.token, input.token)
})

test('only explicitly selected demo registration invokes the credential writer', async () => {
    const store = storage(); const registered: unknown[] = []
    await establishSession(store, input, true, async session => { registered.push(session) })
    assert.deepEqual(registered, [normalizeSession(input)])
    const failed = storage()
    await assert.rejects(establishSession(failed, input, true, async () => { throw Error('registration refused') }))
    assert.equal(loadSession(failed), null)
})

test('reload retains credentials without carrying message or cursor state; logout removes only its own key', () => {
    const store = storage()
    saveSession(store, { ...input, messages: ['old'], cursor: 150 } as typeof input)
    const saved = loadSession(store)
    assert.deepEqual(saved, { apiURL: 'http://localhost:5001', uid: input.uid, token: input.token })
    assert.equal(store.values.get('unrelated-draft'), 'keep me')
    clearSession(store)
    assert.equal(loadSession(store), null)
    assert.equal(store.values.get('unrelated-draft'), 'keep me')
})

test('invalid persisted sessions and unsafe API URLs are refused', () => {
    const store = storage()
    assert.equal(loadSession(store), null)
    store.setItem('wukongim.demo.session.v1', '{broken')
    assert.equal(loadSession(store), null)
    for (const apiURL of ['javascript:alert(1)', 'http://name:password@localhost', 'http://localhost/?token=secret', 'http://localhost/#token']) {
        assert.throws(() => normalizeSession({ ...input, apiURL }))
    }
    assert.throws(() => normalizeSession({ ...input, uid: ' ' }))
    assert.throws(() => normalizeSession({ ...input, token: '' }))
})
