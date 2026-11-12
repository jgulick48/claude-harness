import { describe, expect, it } from 'vitest'
import type { Host, Session } from './sessions'
import { initialState, initialStateWith, reducer } from './store'
import { visibleRefs } from './layout'
import { leaves } from './layoutTree'
import { parseLayout } from './layoutStorage'

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
  describe('hosts.changed', () => {
    const h = (id: string, state: Host['state'] = 'connected'): Host => ({
      id, name: id, kind: id === 'local' ? 'local' : 'ssh', state
    })
    it('adds hosts and updates state', () => {
      let st = reducer(initialState, { type: 'hosts', hosts: [h('local')] })
      st = reducer(st, { type: 'hosts', hosts: [h('local'), h('dev', 'connecting')] })
      expect(st.hosts.map((x) => x.id)).toEqual(['local', 'dev'])
      st = reducer(st, { type: 'hosts', hosts: [h('local'), h('dev', 'connected')] })
      expect(st.hosts[1].state).toBe('connected')
    })
    it('drops sessions of removed hosts and keeps the active view of others', () => {
      let st = reducer(initialState, { type: 'hosts', hosts: [h('local'), h('dev')] })
      st = reducer(st, { type: 'sessions', host: 'dev', sessions: [s('$1')] })
      st = reducer(st, { type: 'sessions', host: 'local', sessions: [s('$2')] })
      st = reducer(st, { type: 'attach', ref: { host: 'local', session: '$2' } })
      st = reducer(st, { type: 'hosts', hosts: [h('local')] })
      expect(Object.keys(st.sessions)).toEqual(['local'])
      expect(st.active).toEqual({ host: 'local', session: '$2' })
    })
    it('clears the active view when its host is removed', () => {
      let st = reducer(initialState, { type: 'hosts', hosts: [h('local'), h('dev')] })
      st = reducer(st, { type: 'attach', ref: { host: 'dev', session: '$1' } })
      st = reducer(st, { type: 'hosts', hosts: [h('local')] })
      expect(st.active).toBeNull()
    })
    it('keeps the active view when its host merely disconnects', () => {
      let st = reducer(initialState, { type: 'hosts', hosts: [h('dev')] })
      st = reducer(st, { type: 'attach', ref: { host: 'dev', session: '$1' } })
      st = reducer(st, { type: 'hosts', hosts: [h('dev', 'disconnected')] })
      expect(st.active).toEqual({ host: 'dev', session: '$1' })
    })
  })
  describe('retryAt', () => {
    it('is carried by hosts events and cleared when the host reconnects', () => {
      const dev = (state: Host['state'], retryAt?: number): Host => ({ id: 'dev', name: 'dev', kind: 'ssh', state, retryAt })
      let st = reducer(initialState, { type: 'hosts', hosts: [dev('disconnected', 12345)] })
      expect(st.hosts[0].retryAt).toBe(12345)
      st = reducer(st, { type: 'hosts', hosts: [dev('connected')] })
      expect(st.hosts[0].retryAt).toBeUndefined()
    })
    it('keeps the active view and its session list while a host retries', () => {
      const dev = (state: Host['state'], retryAt?: number): Host => ({ id: 'dev', name: 'dev', kind: 'ssh', state, retryAt })
      let st = reducer(initialState, { type: 'hosts', hosts: [dev('connected')] })
      st = reducer(st, { type: 'sessions', host: 'dev', sessions: [s('$1')] })
      st = reducer(st, { type: 'attach', ref: { host: 'dev', session: '$1' } })
      st = reducer(st, { type: 'hosts', hosts: [dev('disconnected', 99)] })
      expect(st.active).toEqual({ host: 'dev', session: '$1' })
      expect(st.sessions.dev).toHaveLength(1)
    })
  })
  it('add-host dialog toggles', () => {
    let st = reducer(initialState, { type: 'openAddHost' })
    expect(st.addHostOpen).toBe(true)
    st = reducer(st, { type: 'closeAddHost' })
    expect(st.addHostOpen).toBe(false)
  })
  describe('claude status fields', () => {
    it('keeps claudeStatus through replace and upsert', () => {
      const w = { ...s('$1'), claudeStatus: 'waiting' as const, claudeStatusAt: 5 }
      let st = reducer(initialState, { type: 'sessions', host: 'local', sessions: [w] })
      expect(st.sessions.local[0]).toMatchObject({ claudeStatus: 'waiting', claudeStatusAt: 5 })
      st = reducer(st, { type: 'upsertSession', host: 'local', session: { ...w, claudeStatus: 'idle' } })
      expect(st.sessions.local[0].claudeStatus).toBe('idle')
    })
    it('keeps claudeHooks on hosts and drops sessions of removed hosts', () => {
      const h: Host = { id: 'a', name: 'a', kind: 'ssh', state: 'connected', claudeHooks: 'missing' }
      let st = reducer(initialState, { type: 'hosts', hosts: [h] })
      expect(st.hosts[0].claudeHooks).toBe('missing')
      st = reducer(st, { type: 'sessions', host: 'a', sessions: [s('$1')] })
      st = reducer(st, { type: 'hosts', hosts: [] })
      expect(st.sessions).toEqual({})
    })
  })
})

describe('split layout in the reducer', () => {
  const ref = (n: number) => ({ host: 'local', session: `$${n}` })
  it('derives active from the focused pane', () => {
    let st = reducer(initialState, { type: 'attach', ref: ref(1) })
    st = reducer(st, { type: 'openInSplit', ref: ref(2), direction: 'right' })
    expect(st.active).toEqual(ref(2))
    expect(visibleRefs(st)).toEqual([ref(2), ref(1)])
    st = reducer(st, { type: 'attach', ref: ref(1) }) // focuses pane 1, no duplicate
    expect(st.active).toEqual(ref(1))
    expect(visibleRefs(st)).toEqual([ref(1), ref(2)])
    st = reducer(st, { type: 'closePane' })
    expect(st.active).toEqual(ref(2))
  })
  it('notices past the pane cap', () => {
    let st = initialState
    for (let i = 0; i < 6; i++) st = reducer(st, { type: 'split', direction: 'right' })
    expect(st.notice).toMatch(/At most 6/)
    expect(reducer(st, { type: 'dismissNotice' }).notice).toBeNull()
  })
  it('removing a host removes its panes and reconcile resolves restored refs', () => {
    const host = (id: string): Host => ({ id, name: id, kind: 'ssh', state: 'connected' }) as Host
    let st = initialStateWith(
      parseLayout({ root: { kind: 'split', dir: 'row', sizes: [1, 1], children: [{ kind: 'pane', ref: ref(1) }, { kind: 'pane', ref: { host: 'x', session: '$1' } }] }, focus: 0 })
    )
    st = reducer(st, { type: 'hosts', hosts: [host('local')] }) // x is gone
    expect(visibleRefs(st)).toEqual([ref(1)])
    expect(leaves(st.layout.root)[0].pending).toBe(true)
    st = reducer(st, { type: 'sessions', host: 'local', sessions: [] })
    expect(leaves(st.layout.root)[0]).toMatchObject({ ref: null, gone: true })
    expect(st.active).toBeNull()
  })
})
