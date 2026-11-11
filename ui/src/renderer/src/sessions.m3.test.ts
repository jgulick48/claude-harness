import { describe, expect, it } from 'vitest'
import { autoReattachAction, isPermanentError, reconnectText, shouldRetryOnResume, type Host } from './sessions'

describe('reconnectText', () => {
  it('counts down, rounding up', () => {
    expect(reconnectText(10_000, 5_000)).toBe('Reconnecting in 5s…')
    expect(reconnectText(10_000, 9_001)).toBe('Reconnecting in 1s…')
  })
  it('is never negative', () => {
    expect(reconnectText(1_000, 5_000)).toBe('Reconnecting in 0s…')
  })
})

describe('resume retry predicate', () => {
  const h = (state: Host['state'], error?: string, kind: Host['kind'] = 'ssh') => ({ kind, state, error })
  it('retries disconnected ssh hosts', () => {
    expect(shouldRetryOnResume(h('disconnected'))).toBe(true)
    expect(shouldRetryOnResume(h('disconnected', 'connection lost'))).toBe(true)
  })
  it('retries transient errors', () => {
    expect(shouldRetryOnResume(h('error', 'dial tcp: i/o timeout'))).toBe(true)
    expect(shouldRetryOnResume(h('error'))).toBe(true)
  })
  it.each([
    'ssh: unable to authenticate',
    'Authentication failed',
    'host key rejected',
    'no SSH authentication methods available',
    'REMOTE HOST IDENTIFICATION HAS CHANGED'
  ])('skips permanent error: %s', (msg) => {
    expect(isPermanentError(msg)).toBe(true)
    expect(shouldRetryOnResume(h('error', msg))).toBe(false)
  })
  it('skips local, connected, connecting, key-unknown hosts', () => {
    expect(shouldRetryOnResume(h('disconnected', undefined, 'local'))).toBe(false)
    expect(shouldRetryOnResume(h('connected'))).toBe(false)
    expect(shouldRetryOnResume(h('connecting'))).toBe(false)
    expect(shouldRetryOnResume(h('host_key_unknown'))).toBe(false)
  })
})

describe('autoReattachAction', () => {
  it('reattaches after connection lost once connected and session exists', () => {
    expect(autoReattachAction({ ended: 'connection lost', hostConnected: true, sessionExists: true })).toBe('reattach')
  })
  it('reports gone when the session vanished', () => {
    expect(autoReattachAction({ ended: 'connection lost', hostConnected: true, sessionExists: false })).toBe('gone')
  })
  it('waits while the host is down and ignores other endings', () => {
    expect(autoReattachAction({ ended: 'connection lost', hostConnected: false, sessionExists: true })).toBe('none')
    expect(autoReattachAction({ ended: 'session ended', hostConnected: true, sessionExists: true })).toBe('none')
    expect(autoReattachAction({ ended: null, hostConnected: true, sessionExists: true })).toBe('none')
  })
})
