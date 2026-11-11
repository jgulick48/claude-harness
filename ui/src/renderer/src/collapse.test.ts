import { describe, expect, it } from 'vitest'
import { COLLAPSE_KEY, loadCollapsed, saveCollapsed, toggleCollapsed } from './collapse'

const memory = (): { s: Storage; data: Map<string, string> } => {
  const data = new Map<string, string>()
  const s = { getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => void data.set(k, v) }
  return { s: s as Storage, data }
}
const throwing = (): Storage =>
  ({
    getItem: () => {
      throw new Error('denied')
    },
    setItem: () => {
      throw new Error('denied')
    }
  }) as unknown as Storage

describe('collapse persistence', () => {
  it('round-trips', () => {
    const { s, data } = memory()
    saveCollapsed(new Set(['a', 'b']), () => s)
    expect(JSON.parse(data.get(COLLAPSE_KEY)!)).toEqual(['a', 'b'])
    expect([...loadCollapsed(() => s)]).toEqual(['a', 'b'])
  })
  it('survives a throwing localStorage', () => {
    expect(loadCollapsed(() => throwing()).size).toBe(0)
    expect(() => saveCollapsed(new Set(['a']), () => throwing())).not.toThrow()
  })
  it('survives a throwing accessor and missing storage', () => {
    const boom = (): Storage => {
      throw new Error('SecurityError')
    }
    expect(loadCollapsed(boom).size).toBe(0)
    expect(() => saveCollapsed(new Set(['a']), boom)).not.toThrow()
    expect(loadCollapsed(() => null).size).toBe(0)
    expect(() => saveCollapsed(new Set(['a']), () => null)).not.toThrow()
  })
  it('ignores corrupt data', () => {
    const { s, data } = memory()
    data.set(COLLAPSE_KEY, '{nope')
    expect(loadCollapsed(() => s).size).toBe(0)
    data.set(COLLAPSE_KEY, '{"a":1}')
    expect(loadCollapsed(() => s).size).toBe(0)
    data.set(COLLAPSE_KEY, '["a",3]')
    expect([...loadCollapsed(() => s)]).toEqual(['a'])
  })
  it('toggles without mutating', () => {
    const a = new Set(['x'])
    const b = toggleCollapsed(a, 'y')
    expect([...a]).toEqual(['x'])
    expect([...b]).toEqual(['x', 'y'])
    expect(toggleCollapsed(b, 'x').has('x')).toBe(false)
  })
})
