import { describe, expect, it } from 'vitest'
import {
  clampWidth,
  moveNav,
  navRefs,
  nthRef,
  SIDEBAR_DEFAULT,
  SIDEBAR_MAX,
  SIDEBAR_MIN,
  shortcutFor,
  type NavGroup
} from './sidebarNav'

const groups: NavGroup[] = [
  { host: 'a', folded: false, sessions: ['1', '2'] },
  { host: 'b', folded: true, sessions: ['3'] },
  { host: 'c', folded: false, sessions: [] },
  { host: 'd', folded: false, sessions: ['4'] }
]
const list = navRefs(groups)

describe('navRefs', () => {
  it('flattens in order and skips folded groups', () => {
    expect(list).toEqual([
      { host: 'a', session: '1' },
      { host: 'a', session: '2' },
      { host: 'd', session: '4' }
    ])
  })
})

describe('moveNav', () => {
  it('crosses host groups and clamps at the ends', () => {
    expect(moveNav(list, list[1], 1)).toEqual(list[2])
    expect(moveNav(list, list[2], 1)).toEqual(list[2])
    expect(moveNav(list, list[0], -1)).toEqual(list[0])
    expect(moveNav(list, list[2], -1)).toEqual(list[1])
  })
  it('starts at an end when nothing is current', () => {
    expect(moveNav(list, null, 1)).toEqual(list[0])
    expect(moveNav(list, null, -1)).toEqual(list[2])
    expect(moveNav(list, { host: 'b', session: '3' }, 1)).toEqual(list[0])
  })
  it('returns null for an empty list', () => {
    expect(moveNav([], null, 1)).toBeNull()
  })
})

describe('nthRef', () => {
  it('is 1-based and bounded', () => {
    expect(nthRef(list, 1)).toEqual(list[0])
    expect(nthRef(list, 3)).toEqual(list[2])
    expect(nthRef(list, 4)).toBeNull()
    expect(nthRef(list, 0)).toBeNull()
  })
})

describe('shortcutFor', () => {
  const k = (key: string, o: Partial<Record<'ctrlKey' | 'metaKey' | 'shiftKey' | 'altKey', boolean>> = {}) => ({
    key,
    ctrlKey: false,
    metaKey: false,
    shiftKey: false,
    altKey: false,
    ...o
  })
  it('maps Ctrl/Cmd combos', () => {
    expect(shortcutFor(k('k', { ctrlKey: true }))).toEqual({ kind: 'filter' })
    expect(shortcutFor(k('K', { metaKey: true }))).toEqual({ kind: 'filter' })
    expect(shortcutFor(k('b', { ctrlKey: true }))).toEqual({ kind: 'rail' })
    expect(shortcutFor(k('2', { ctrlKey: true }))).toEqual({ kind: 'nth', n: 2 })
  })
  it('ignores plain, shifted, alt and 0', () => {
    expect(shortcutFor(k('k'))).toBeNull()
    expect(shortcutFor(k('k', { ctrlKey: true, shiftKey: true }))).toBeNull()
    expect(shortcutFor(k('1', { ctrlKey: true, altKey: true }))).toBeNull()
    expect(shortcutFor(k('0', { ctrlKey: true }))).toBeNull()
  })
})

describe('clampWidth', () => {
  it('clamps and rounds', () => {
    expect(clampWidth(100)).toBe(SIDEBAR_MIN)
    expect(clampWidth(900)).toBe(SIDEBAR_MAX)
    expect(clampWidth(300.4)).toBe(300)
    expect(clampWidth(NaN)).toBe(SIDEBAR_DEFAULT)
  })
})
