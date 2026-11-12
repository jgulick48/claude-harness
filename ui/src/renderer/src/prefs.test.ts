import { describe, expect, it } from 'vitest'
import { loadFlag, saveFlag } from './prefs'

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

describe('prefs flags', () => {
  it('round-trips', () => {
    const m = mem()
    saveFlag('k', true, m.get)
    expect(loadFlag('k', false, m.get)).toBe(true)
    saveFlag('k', false, m.get)
    expect(loadFlag('k', true, m.get)).toBe(false)
  })
  it('uses the fallback when unset or garbage', () => {
    const m = mem()
    expect(loadFlag('k', true, m.get)).toBe(true)
    m.store.k = 'banana'
    expect(loadFlag('k', false, m.get)).toBe(false)
  })
  it('survives missing storage', () => {
    expect(loadFlag('k', true, () => null)).toBe(true)
    expect(() => saveFlag('k', true, () => null)).not.toThrow()
  })
  it('survives throwing storage', () => {
    expect(loadFlag('k', true, throwing)).toBe(true)
    expect(() => saveFlag('k', true, throwing)).not.toThrow()
    const bad = (): Pick<Storage, 'getItem' | 'setItem'> => ({ getItem: throwing, setItem: throwing })
    expect(loadFlag('k', false, bad)).toBe(false)
    expect(() => saveFlag('k', true, bad)).not.toThrow()
  })
})

describe('string prefs', () => {
  it('round-trips and survives throwing storage', async () => {
    const { loadString, saveString } = await import('./prefs')
    const m = new Map<string, string>()
    const st = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) }
    expect(loadString('k', '', () => st)).toBe('')
    saveString('k', 'deploy', () => st)
    expect(loadString('k', '', () => st)).toBe('deploy')
    const boom = () => {
      throw new Error('no')
    }
    expect(loadString('k', 'f', () => ({ getItem: boom, setItem: boom }))).toBe('f')
    expect(() => saveString('k', 'x', () => ({ getItem: boom, setItem: boom }))).not.toThrow()
  })
})
