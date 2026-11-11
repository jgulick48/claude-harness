import { describe, expect, it } from 'vitest'
import {
  deleteNamedLayout, LAYOUT_KEY, loadLayout, loadNamedLayouts, NAMED_LAYOUTS_KEY, parseLayout, saveLayout,
  saveNamedLayout, serializeLayout
} from './layoutStorage'
import { attachRef, emptyLayout, leaves, splitFocused, type Split } from './layoutTree'

function mem(init: Record<string, string> = {}) {
  const m = new Map(Object.entries(init))
  const st = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) }
  return { m, get: () => st }
}
const A = { host: 'h', session: '$1' }
const B = { host: 'h', session: '$2' }
const sample = () => splitFocused(attachRef(emptyLayout(), A), 'right', B).layout

describe('layout persistence', () => {
  it('round-trips with pending refs and focus', () => {
    const { get, m } = mem()
    saveLayout(sample(), get)
    const l = loadLayout(get)!
    expect(l.root.kind).toBe('split')
    const ps = leaves(l.root)
    expect(ps.map((p) => p.ref?.session)).toEqual(['$1', '$2'])
    expect(ps.every((p) => p.pending)).toBe(true)
    expect(l.focus).toBe(ps[1].id)
    expect(m.get(LAYOUT_KEY)).not.toMatch(/pending|"id"/)
  })
  it('drops corrupt data', () => {
    for (const bad of ['not json', '{}', '{"root":{"kind":"x"}}', '{"root":{"kind":"split","dir":"row","sizes":[1],"children":[{"kind":"pane","ref":null}]}}', '{"root":{"kind":"pane","ref":{"host":1,"session":"a"}}}']) {
      const { get, m } = mem({ [LAYOUT_KEY]: bad })
      expect(loadLayout(get)).toBeNull()
      expect(m.has(LAYOUT_KEY)).toBe(false)
    }
  })
  it('rejects more than 6 panes and deep nesting', () => {
    const pane = { kind: 'pane', ref: null }
    const wide = { kind: 'split', dir: 'row', sizes: Array(7).fill(1), children: Array(7).fill(pane) }
    expect(parseLayout({ root: wide, focus: 0 })).toBeNull()
    let deep: unknown = pane
    for (let i = 0; i < 12; i++) deep = { kind: 'split', dir: 'row', sizes: [1, 1], children: [deep, pane] }
    expect(parseLayout({ root: deep, focus: 0 })).toBeNull()
  })
  it('dedupes a session shown twice and clamps focus', () => {
    const raw = { root: { kind: 'split', dir: 'column', sizes: [3, 1], children: [{ kind: 'pane', ref: A }, { kind: 'pane', ref: A }] }, focus: 99 }
    const l = parseLayout(raw)!
    expect(leaves(l.root).map((p) => p.ref?.session ?? null)).toEqual(['$1', null])
    expect(l.focus).toBe(leaves(l.root)[0].id)
    expect((l.root as Split).sizes[0]).toBeCloseTo(0.75)
  })
  it('tolerates throwing storage', () => {
    const boom = () => { throw new Error('no') }
    expect(loadLayout(boom)).toBeNull()
    expect(() => saveLayout(sample(), boom)).not.toThrow()
    expect(loadNamedLayouts(boom)).toEqual([])
  })
  it('serialize keeps the focus index', () => {
    expect(serializeLayout(sample()).focus).toBe(1)
  })
})

describe('named layouts', () => {
  it('saves, overwrites, deletes', () => {
    const { get } = mem()
    saveNamedLayout('  two  ', sample(), get)
    saveNamedLayout('one', attachRef(emptyLayout(), A), get)
    expect(loadNamedLayouts(get).map((e) => e.name)).toEqual(['two', 'one'])
    saveNamedLayout('two', emptyLayout(), get)
    expect(loadNamedLayouts(get).map((e) => e.name)).toEqual(['one', 'two'])
    expect(deleteNamedLayout('one', get).map((e) => e.name)).toEqual(['two'])
    expect(saveNamedLayout('   ', sample(), get)).toHaveLength(1)
  })
  it('drops corrupt entries but keeps good ones', () => {
    const good = serializeLayout(sample())
    const { get } = mem({ [NAMED_LAYOUTS_KEY]: JSON.stringify([{ name: 'ok', layout: good }, { name: 'bad', layout: { root: 5 } }, { layout: good }, 7, { name: 'ok', layout: good }]) })
    expect(loadNamedLayouts(get).map((e) => e.name)).toEqual(['ok'])
    expect(loadNamedLayouts(mem({ [NAMED_LAYOUTS_KEY]: '{"a":1}' }).get)).toEqual([])
  })
})
