import { describe, expect, it } from 'vitest'
import { loadNumber, saveNumber } from './prefs'

const mem = (): { store: Record<string, string>; get: () => Pick<Storage, 'getItem' | 'setItem'> } => {
  const store: Record<string, string> = {}
  return {
    store,
    get: () => ({
      getItem: (k) => store[k] ?? null,
      setItem: (k, v) => {
        store[k] = v
      }
    })
  }
}
const throwing = (): never => {
  throw new Error('blocked')
}

describe('prefs numbers (sidebar width)', () => {
  it('round-trips', () => {
    const m = mem()
    saveNumber('w', 312, m.get)
    expect(loadNumber('w', 280, m.get)).toBe(312)
  })
  it('falls back when unset, empty or malformed', () => {
    const m = mem()
    expect(loadNumber('w', 280, m.get)).toBe(280)
    m.store.w = ''
    expect(loadNumber('w', 280, m.get)).toBe(280)
    m.store.w = 'wide'
    expect(loadNumber('w', 280, m.get)).toBe(280)
  })
  it('survives missing and throwing storage', () => {
    expect(loadNumber('w', 280, () => null)).toBe(280)
    expect(() => saveNumber('w', 1, () => null)).not.toThrow()
    expect(loadNumber('w', 280, throwing)).toBe(280)
    expect(() => saveNumber('w', 1, throwing)).not.toThrow()
    const bad = (): Pick<Storage, 'getItem' | 'setItem'> => ({ getItem: throwing, setItem: throwing })
    expect(loadNumber('w', 280, bad)).toBe(280)
    expect(() => saveNumber('w', 1, bad)).not.toThrow()
  })
})
