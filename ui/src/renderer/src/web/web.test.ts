import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { bridgeKeys } from '../../../../e2e/support/bridge'
import { attentionTitle } from '../claudeStatus'
import { createWebBridge } from './webBridge'
import { authorizedKeysCommand, isHttpLink, shellQuote, watchResume, wsUrlFor } from './webPure'

describe('web bridge', () => {
  it('implements exactly the HarnessApi keys', () => {
    expect(Object.keys(createWebBridge()).sort()).toEqual(Object.keys(bridgeKeys).sort())
  })
})

describe('wsUrlFor', () => {
  it('maps http to ws and https to wss, keeping the port', () => {
    expect(wsUrlFor({ protocol: 'http:', host: 'localhost:8080' })).toBe('ws://localhost:8080/ws')
    expect(wsUrlFor({ protocol: 'https:', host: 'harness.example.com' })).toBe('wss://harness.example.com/ws')
  })
})

describe('attention title', () => {
  it('prefixes the count', () => {
    expect(attentionTitle(2)).toBe('(2) Claude Harness')
    expect(attentionTitle(0)).toBe('Claude Harness')
  })
})

describe('isHttpLink', () => {
  it('accepts only http(s)', () => {
    expect(isHttpLink('https://a.b/c')).toBe(true)
    expect(isHttpLink('http://a.b')).toBe(true)
    expect(isHttpLink('javascript:alert(1)')).toBe(false)
    expect(isHttpLink('file:///etc/passwd')).toBe(false)
    expect(isHttpLink('not a url')).toBe(false)
    expect(isHttpLink(null)).toBe(false)
  })
})

describe('authorized_keys command', () => {
  it('single-quotes the key', () => {
    expect(authorizedKeysCommand('ssh-ed25519 AAAA harness\n')).toBe(
      "echo 'ssh-ed25519 AAAA harness' >> ~/.ssh/authorized_keys"
    )
  })
  it("escapes embedded single quotes", () => {
    expect(shellQuote("a'b")).toBe(`'a'\\''b'`)
  })
})

describe('watchResume', () => {
  type L = () => void
  const mk = (): { doc: any; win: any; fire: (t: 'doc' | 'win', ev: string) => void } => {
    const ls: Record<string, L[]> = {}
    const target = (p: string): any => ({
      addEventListener: (e: string, f: L) => (ls[p + e] ??= []).push(f),
      removeEventListener: (e: string, f: L) => (ls[p + e] = (ls[p + e] ?? []).filter((x) => x !== f))
    })
    const doc = { ...target('doc'), visibilityState: 'visible' }
    return { doc, win: target('win'), fire: (t, ev) => (ls[t + ev] ?? []).forEach((f) => f()) }
  }
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('fires only after being hidden longer than 30s', () => {
    const { doc, win, fire } = mk()
    const cb = vi.fn()
    watchResume(doc, win, cb)
    doc.visibilityState = 'hidden'
    fire('doc', 'visibilitychange')
    vi.advanceTimersByTime(10_000)
    doc.visibilityState = 'visible'
    fire('doc', 'visibilitychange')
    expect(cb).not.toHaveBeenCalled()
    doc.visibilityState = 'hidden'
    fire('doc', 'visibilitychange')
    vi.advanceTimersByTime(31_000)
    doc.visibilityState = 'visible'
    fire('doc', 'visibilitychange')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('fires on online and stops after unsubscribe', () => {
    const { doc, win, fire } = mk()
    const cb = vi.fn()
    const off = watchResume(doc, win, cb)
    fire('win', 'online')
    expect(cb).toHaveBeenCalledTimes(1)
    off()
    fire('win', 'online')
    expect(cb).toHaveBeenCalledTimes(1)
  })
})
