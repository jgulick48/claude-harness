import { describe, expect, it } from 'vitest'
import type { Session } from './sessions'
import { initialState, reducer } from './store'

const s = (id: string, name = 'n'): Session => ({
  id, name, harnessName: '', managed: true, cwd: '/x', windows: 1, attached: 0, created: 1
})

describe('store reducer', () => {
  it('replaces a host session list', () => {
    const a = reducer(initialState, { type: 'sessions', host: 'local', sessions: [s('$1')] })
    const b = reducer(a, { type: 'sessions', host: 'local', sessions: [s('$2')] })
    expect(b.sessions.local.map((x) => x.id)).toEqual(['$2'])
  })
  it('upserts', () => {
    let st = reducer(initialState, { type: 'upsertSession', host: 'local', session: s('$1', 'a') })
    st = reducer(st, { type: 'upsertSession', host: 'local', session: s('$1', 'b') })
    expect(st.sessions.local).toHaveLength(1)
    expect(st.sessions.local[0].name).toBe('b')
  })
  it('attach and dialog', () => {
    let st = reducer(initialState, { type: 'attach', ref: { host: 'local', session: '$1' } })
    expect(st.active).toEqual({ host: 'local', session: '$1' })
    st = reducer(st, { type: 'openDialog', host: 'local' })
    expect(st.dialog).toEqual({ host: 'local' })
    st = reducer(st, { type: 'closeDialog' })
    expect(st.dialog).toBeNull()
  })
})
