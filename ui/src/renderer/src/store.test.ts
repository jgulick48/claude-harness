import { describe, expect, it } from 'vitest'
import type { Host, Session } from './sessions'
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
})
