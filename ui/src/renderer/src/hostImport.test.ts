import { describe, expect, it } from 'vitest'
import {
  applyImportResults,
  describeConfigHost,
  hasImportable,
  importAliases,
  selectedAliases,
  toggleAlias,
  type ConfigHost
} from './hostImport'

const list: ConfigHost[] = [
  { alias: 'dev', hostName: 'dev.lan', user: 'me', port: 2222, added: false },
  { alias: 'old', hostName: 'old.lan', added: true },
  { alias: 'bare', added: false }
]

describe('hostImport', () => {
  it('describes hosts', () => {
    expect(describeConfigHost(list[0])).toBe('me@dev.lan:2222')
    expect(describeConfigHost(list[1])).toBe('old.lan')
    expect(describeConfigHost({ alias: 'x', hostName: 'h', user: 'u', added: false })).toBe('u@h')
  })
  it('detects importable entries', () => {
    expect(hasImportable(list)).toBe(true)
    expect(hasImportable([list[1]])).toBe(false)
    expect(hasImportable([])).toBe(false)
  })
  it('toggles, ignoring added and unknown aliases', () => {
    let s = toggleAlias(new Set(), 'dev', list)
    expect([...s]).toEqual(['dev'])
    s = toggleAlias(s, 'old', list)
    s = toggleAlias(s, 'nope', list)
    expect([...s]).toEqual(['dev'])
    s = toggleAlias(s, 'dev', list)
    expect(s.size).toBe(0)
  })
  it('returns selected aliases in list order', () => {
    expect(selectedAliases(new Set(['bare', 'dev', 'old']), list)).toEqual(['dev', 'bare'])
  })
  it('imports sequentially and collects per-host errors', async () => {
    const calls: string[] = []
    const res = await importAliases(['dev', 'bare'], async (a) => {
      calls.push(a)
      if (a === 'dev') throw new Error('duplicate')
    })
    expect(calls).toEqual(['dev', 'bare'])
    expect(res).toEqual([{ alias: 'dev', error: 'duplicate' }, { alias: 'bare' }])
    const applied = applyImportResults(list, new Set(['dev', 'bare']), res)
    expect(applied.errors).toEqual({ dev: 'duplicate' })
    expect([...applied.selection]).toEqual(['dev'])
    expect(applied.list.find((h) => h.alias === 'bare')?.added).toBe(true)
    expect(applied.list.find((h) => h.alias === 'dev')?.added).toBe(false)
  })
})
