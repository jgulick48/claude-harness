import { describe, expect, it } from 'vitest'
import {
  attachRef, closePane, closeRef, dividers, emptyLayout, focusDirection, layoutRefs, leaves, MAX_PANES, paneCount,
  pruneHosts, reconcile, resizeDivider, splitAt, splitFocused, type Layout, type Split
} from './layoutTree'

const A = { host: 'h', session: '$1' }
const B = { host: 'h', session: '$2' }
const C = { host: 'g', session: '$3' }
const ids = (l: Layout): string[] => leaves(l.root).map((p) => p.id)
const refsOf = (l: Layout): Array<string | null> => leaves(l.root).map((p) => p.ref?.session ?? null)

function two(): Layout {
  return splitFocused(attachRef(emptyLayout(), A), 'right', B).layout
}

describe('attach', () => {
  it('replaces the focused pane ref', () => {
    const l = attachRef(attachRef(emptyLayout(), A), B)
    expect(refsOf(l)).toEqual(['$2'])
  })
  it('focuses the pane already showing the session', () => {
    const l = two() // focus on pane 2 (B)
    const r = attachRef(l, A)
    expect(r.focus).toBe('p1')
    expect(refsOf(r)).toEqual(['$1', '$2'])
  })
  it('null empties the focused pane', () => {
    expect(refsOf(attachRef(two(), null))).toEqual(['$1', null])
  })
})

describe('split', () => {
  it('right is a row, down a column, even sizes, new pane focused', () => {
    const r = splitFocused(emptyLayout(), 'right').layout
    expect(r.root).toMatchObject({ kind: 'split', dir: 'row', sizes: [0.5, 0.5] })
    expect(r.focus).toBe('p2')
    const d = splitFocused(emptyLayout(), 'down').layout
    expect(d.root).toMatchObject({ kind: 'split', dir: 'column' })
  })
  it('same direction adds a sibling and halves the share', () => {
    const l = splitFocused(splitFocused(emptyLayout(), 'right').layout, 'right').layout
    const s = l.root as Split
    expect(s.children).toHaveLength(3)
    expect(s.sizes).toEqual([0.5, 0.25, 0.25])
  })
  it('opening a session shown elsewhere focuses it instead', () => {
    const l = splitFocused(two(), 'down', A).layout
    expect(paneCount(l)).toBe(2)
    expect(l.focus).toBe('p1')
  })
  it('caps at six panes with a message', () => {
    let l = emptyLayout()
    for (let i = 1; i < MAX_PANES; i++) l = splitFocused(l, i % 2 ? 'right' : 'down').layout
    expect(paneCount(l)).toBe(MAX_PANES)
    const r = splitFocused(l, 'right')
    expect(r.layout).toBe(l)
    expect(r.error).toMatch(/At most 6/)
  })
})

describe('close', () => {
  it('sibling takes the space and gets focus', () => {
    const l = closePane(two(), 'p2')
    expect(l.root.kind).toBe('pane')
    expect(l.focus).toBe('p1')
    expect(refsOf(l)).toEqual(['$1'])
  })
  it('the last pane becomes empty', () => {
    const l = closePane(attachRef(emptyLayout(), A), 'p1')
    expect(refsOf(l)).toEqual([null])
    expect(paneCount(l)).toBe(1)
  })
  it('redistributes size among remaining panes', () => {
    let l = splitFocused(splitFocused(emptyLayout(), 'right').layout, 'right').layout
    l = closePane(l, 'p3')
    const s = l.root as Split
    expect(s.sizes.reduce((a, b) => a + b, 0)).toBeCloseTo(1)
    expect(s.children).toHaveLength(2)
  })
  it('collapses a nested split', () => {
    const l = splitFocused(two(), 'down').layout // p3 below p2
    expect(ids(closePane(l, 'p3'))).toEqual(['p1', 'p2'])
    expect((closePane(l, 'p3').root as Split).children).toHaveLength(2)
  })
})

describe('closeRef', () => {
  it('closes the pane showing the session and leaves the others', () => {
    const l = closeRef(two(), B)
    expect(refsOf(l)).toEqual(['$1'])
    expect(l.focus).toBe('p1')
  })
  it('empties the last pane', () => {
    expect(refsOf(closeRef(attachRef(emptyLayout(), A), A))).toEqual([null])
  })
  it('is a no-op for a session not shown', () => {
    const l = two()
    expect(closeRef(l, C)).toBe(l)
  })
})

describe('focus by direction', () => {
  // p1 | (p2 over p3)
  const l0 = splitFocused(two(), 'down').layout
  it('moves between neighbours', () => {
    expect(focusDirection(l0, 'up').focus).toBe('p2')
    expect(focusDirection(l0, 'left').focus).toBe('p1')
    const atP1 = { ...l0, focus: 'p1' }
    expect(focusDirection(atP1, 'right').focus).toMatch(/p2|p3/)
    expect(focusDirection(atP1, 'left')).toBe(atP1)
    expect(focusDirection(atP1, 'down')).toBe(atP1)
  })
})

describe('resize', () => {
  it('moves the divider keeping fractions', () => {
    const l = resizeDivider(two(), [], 0, 0.1)
    expect((l.root as Split).sizes[0]).toBeCloseTo(0.6)
    expect((l.root as Split).sizes[1]).toBeCloseTo(0.4)
  })
  it('keeps a minimum of 15%', () => {
    const l = resizeDivider(two(), [], 0, 0.9)
    expect((l.root as Split).sizes[1]).toBeCloseTo(0.15)
    expect((resizeDivider(two(), [], 0, -0.9).root as Split).sizes[0]).toBeCloseTo(0.15)
  })
  it('uses the drag base sizes', () => {
    const l0 = resizeDivider(two(), [], 0, 0.2)
    const l = resizeDivider(l0, [], 0, 0.1, [0.5, 0.5])
    expect((l.root as Split).sizes[0]).toBeCloseTo(0.6)
  })
  it('ignores bad paths', () => {
    const l = two()
    expect(resizeDivider(l, [3], 0, 0.1)).toBe(l)
    expect(splitAt(l, [])).not.toBeNull()
  })
  it('describes dividers geometrically', () => {
    const d = dividers(two().root)
    expect(d).toHaveLength(1)
    expect(d[0].at).toBeCloseTo(0.5)
  })
})

describe('hosts and refs', () => {
  it('prunes panes of removed hosts', () => {
    const l = splitFocused(two(), 'down', C).layout
    const r = pruneHosts(l, new Set(['h']))
    expect(refsOf(r)).toEqual(['$1', '$2'])
    expect(refsOf(pruneHosts(attachRef(emptyLayout(), C), new Set(['h'])))).toEqual([null])
  })
  it('lists refs, focused first', () => {
    expect(layoutRefs(two())).toEqual([B, A])
  })
  it('reconcile waits for host, then resolves or marks gone', () => {
    const l: Layout = {
      root: {
        kind: 'split', dir: 'row', sizes: [0.5, 0.5],
        children: [
          { kind: 'pane', id: 'p1', ref: A, pending: true },
          { kind: 'pane', id: 'p2', ref: { host: 'h', session: '$9' }, pending: true }
        ]
      },
      focus: 'p1', nextId: 3
    }
    expect(reconcile(l, [{ id: 'h', state: 'connecting' }], {})).toBe(l)
    expect(reconcile(l, [{ id: 'h', state: 'connected' }], {})).toBe(l)
    const r = reconcile(l, [{ id: 'h', state: 'connected' }], { h: [{ id: '$1' }] })
    const [p1, p2] = leaves(r.root)
    expect(p1).toEqual({ kind: 'pane', id: 'p1', ref: A })
    expect(p2).toEqual({ kind: 'pane', id: 'p2', ref: null, gone: true })
  })
})
