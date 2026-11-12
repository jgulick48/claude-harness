// Pure split-layout model (M6): a tree of splits and panes plus a focused
// pane. No React, no storage; every operation returns a new Layout.
import type { ActiveRef } from './store'

export type Dir = 'row' | 'column'
export type Direction = 'left' | 'right' | 'up' | 'down'

export interface Pane {
  kind: 'pane'
  id: string
  ref: ActiveRef | null
  /** Restored from storage and not yet matched against live hosts/sessions. */
  pending?: boolean
  /** The restored session no longer exists (shown as an empty pane with a note). */
  gone?: boolean
}
export interface Split {
  kind: 'split'
  dir: Dir
  children: Node[]
  /** Fractions of the split, same length as children, sum 1. */
  sizes: number[]
}
export type Node = Pane | Split

export interface Layout {
  root: Node
  focus: string
  nextId: number
}

export const MAX_PANES = 6
export const MIN_FRACTION = 0.15
export const TOO_MANY_PANES = `At most ${MAX_PANES} panes. Close one first.`

export function emptyLayout(): Layout {
  return { root: { kind: 'pane', id: 'p1', ref: null }, focus: 'p1', nextId: 2 }
}

export function leaves(n: Node): Pane[] {
  return n.kind === 'pane' ? [n] : n.children.flatMap(leaves)
}
export const paneCount = (l: Layout): number => leaves(l.root).length
export const findPane = (l: Layout, id: string): Pane | undefined => leaves(l.root).find((p) => p.id === id)
export const focusedPane = (l: Layout): Pane | undefined => findPane(l, l.focus)
export const sameRef = (a: ActiveRef | null, b: ActiveRef | null): boolean =>
  !!a && !!b && a.host === b.host && a.session === b.session

/** Sessions shown in any pane, focused pane first. */
export function layoutRefs(l: Layout): ActiveRef[] {
  const ps = leaves(l.root).filter((p) => p.ref)
  ps.sort((a, b) => Number(b.id === l.focus) - Number(a.id === l.focus))
  return ps.map((p) => p.ref as ActiveRef)
}

function mapPane(n: Node, id: string, f: (p: Pane) => Node): Node {
  if (n.kind === 'pane') return n.id === id ? f(n) : n
  return { ...n, children: n.children.map((c) => mapPane(c, id, f)) }
}

/** Show ref (or nothing) in the focused pane; a ref shown elsewhere focuses that pane instead. */
export function attachRef(l: Layout, ref: ActiveRef | null): Layout {
  if (ref) {
    const other = leaves(l.root).find((p) => sameRef(p.ref, ref))
    if (other) {
      if (other.id === l.focus && !other.pending) return l
      return { ...l, focus: other.id, root: mapPane(l.root, other.id, (p) => ({ ...p, pending: false })) }
    }
  }
  return { ...l, root: mapPane(l.root, l.focus, (p) => ({ kind: 'pane', id: p.id, ref })) }
}

export function clearPane(l: Layout, id: string): Layout {
  return { ...l, root: mapPane(l.root, id, (p) => ({ kind: 'pane', id: p.id, ref: null })) }
}

export function focusPane(l: Layout, id: string): Layout {
  return l.focus === id || !findPane(l, id) ? l : { ...l, focus: id }
}

export type SplitResult = { layout: Layout; error?: string }

function splitNode(n: Node, id: string, dir: Dir, fresh: Pane): Node {
  if (n.kind === 'pane') {
    return n.id === id ? { kind: 'split', dir, children: [n, fresh], sizes: [0.5, 0.5] } : n
  }
  const i = n.children.findIndex((c) => c.kind === 'pane' && c.id === id)
  if (i >= 0 && n.dir === dir) {
    // Same direction as the parent: add a sibling, halving the split pane's share.
    const half = n.sizes[i] / 2
    const children = [...n.children.slice(0, i + 1), fresh, ...n.children.slice(i + 1)]
    const sizes = [...n.sizes.slice(0, i), half, half, ...n.sizes.slice(i + 1)]
    return { ...n, children, sizes }
  }
  return { ...n, children: n.children.map((c) => splitNode(c, id, dir, fresh)) }
}

/**
 * New pane next to the focused one, focused, showing ref (default empty).
 * A ref already shown in some pane focuses that pane instead.
 */
export function splitFocused(l: Layout, direction: 'right' | 'down', ref: ActiveRef | null = null): SplitResult {
  if (ref) {
    const other = leaves(l.root).find((p) => sameRef(p.ref, ref))
    if (other) return { layout: attachRef(l, ref) }
  }
  if (paneCount(l) >= MAX_PANES) return { layout: l, error: TOO_MANY_PANES }
  const id = `p${l.nextId}`
  const fresh: Pane = { kind: 'pane', id, ref }
  const root = splitNode(l.root, l.focus, direction === 'right' ? 'row' : 'column', fresh)
  return { layout: { root, focus: id, nextId: l.nextId + 1 } }
}

function normalize(sizes: number[]): number[] {
  const t = sizes.reduce((a, b) => a + b, 0)
  return t > 0 ? sizes.map((s) => s / t) : sizes.map(() => 1 / sizes.length)
}

/** Removes pane `id`; returns the node that replaces `n` (null when n itself is the pane) and the pane to focus. */
function removeFrom(n: Node, id: string): { node: Node | null; focus?: string } {
  if (n.kind === 'pane') return { node: n.id === id ? null : n }
  const i = n.children.findIndex((c) => leaves(c).some((p) => p.id === id))
  if (i < 0) return { node: n }
  const child = n.children[i]
  const r = removeFrom(child, id)
  if (r.node) {
    const children = n.children.map((c, j) => (j === i ? (r.node as Node) : c))
    return { node: { ...n, children }, focus: r.focus }
  }
  // The child itself vanished: its share goes to the neighbour.
  const children = n.children.filter((_, j) => j !== i)
  const sizes = n.sizes.filter((_, j) => j !== i)
  const nb = i > 0 ? i - 1 : 0
  sizes[nb] += n.sizes[i]
  const focus = leaves(children[nb])[0].id
  if (children.length === 1) return { node: children[0], focus }
  return { node: { ...n, children, sizes: normalize(sizes) }, focus }
}

/** Closes a pane; the sibling takes its space, and the last pane just becomes empty. */
export function closePane(l: Layout, id: string): Layout {
  if (!findPane(l, id)) return l
  if (paneCount(l) === 1) return clearPane(l, id)
  const r = removeFrom(l.root, id)
  const root = r.node as Node
  let focus = l.focus
  if (l.focus === id) focus = r.focus ?? leaves(root)[0].id
  return { ...l, root, focus }
}

/** Closes every pane showing ref (the tmux session itself is untouched). */
export function closeRef(l: Layout, ref: ActiveRef): Layout {
  let out = l
  for (const p of leaves(l.root)) if (sameRef(p.ref, ref)) out = closePane(out, p.id)
  return out
}

export interface Rect {
  x: number
  y: number
  w: number
  h: number
}
export function rects(n: Node, r: Rect, out: Map<string, Rect>): void {
  if (n.kind === 'pane') {
    out.set(n.id, r)
    return
  }
  let off = 0
  n.children.forEach((c, i) => {
    const f = n.sizes[i]
    rects(
      c,
      n.dir === 'row' ? { x: r.x + off * r.w, y: r.y, w: f * r.w, h: r.h } : { x: r.x, y: r.y + off * r.h, w: r.w, h: f * r.h },
      out
    )
    off += f
  })
}

/** The pane next to the focused one in a direction, by geometry; null when there is none. */
export function neighbour(l: Layout, dir: Direction): string | null {
  const m = new Map<string, Rect>()
  rects(l.root, { x: 0, y: 0, w: 1, h: 1 }, m)
  const cur = m.get(l.focus)
  if (!cur) return null
  const eps = 1e-6
  let best: { id: string; gap: number; dist: number } | null = null
  for (const [id, r] of m) {
    if (id === l.focus) continue
    let gap: number
    let overlap: number
    let dist: number
    if (dir === 'left' || dir === 'right') {
      gap = dir === 'right' ? r.x - (cur.x + cur.w) : cur.x - (r.x + r.w)
      overlap = Math.min(cur.y + cur.h, r.y + r.h) - Math.max(cur.y, r.y)
      dist = Math.abs(r.y + r.h / 2 - (cur.y + cur.h / 2))
    } else {
      gap = dir === 'down' ? r.y - (cur.y + cur.h) : cur.y - (r.y + r.h)
      overlap = Math.min(cur.x + cur.w, r.x + r.w) - Math.max(cur.x, r.x)
      dist = Math.abs(r.x + r.w / 2 - (cur.x + cur.w / 2))
    }
    if (gap < -eps || overlap <= eps) continue
    if (!best || gap < best.gap - eps || (Math.abs(gap - best.gap) <= eps && dist < best.dist)) best = { id, gap, dist }
  }
  return best ? best.id : null
}

export function focusDirection(l: Layout, dir: Direction): Layout {
  const id = neighbour(l, dir)
  return id ? focusPane(l, id) : l
}

/** The split at a path of child indices from the root. */
export function splitAt(l: Layout, path: number[]): Split | null {
  let n: Node = l.root
  for (const i of path) {
    if (n.kind !== 'split' || !n.children[i]) return null
    n = n.children[i]
  }
  return n.kind === 'split' ? n : null
}

function mapSplit(n: Node, path: number[], f: (s: Split) => Split): Node {
  if (n.kind !== 'split') return n
  if (path.length === 0) return f(n)
  const [h, ...rest] = path
  return { ...n, children: n.children.map((c, i) => (i === h ? mapSplit(c, rest, f) : c)) }
}

/**
 * Moves the divider between children `index` and `index+1` of the split at
 * `path` by `delta` (a fraction of the split), from `base` sizes (the sizes
 * when a drag started) or the current ones. Both neighbours keep MIN_FRACTION.
 */
export function resizeDivider(l: Layout, path: number[], index: number, delta: number, base?: number[]): Layout {
  const s = splitAt(l, path)
  if (!s || index < 0 || index >= s.children.length - 1) return l
  const from = base && base.length === s.sizes.length ? base : s.sizes
  const total = from[index] + from[index + 1]
  const min = Math.min(MIN_FRACTION, total / 2)
  const a = Math.min(Math.max(from[index] + delta, min), total - min)
  const sizes = from.slice()
  sizes[index] = a
  sizes[index + 1] = total - a
  return { ...l, root: mapSplit(l.root, path, (x) => ({ ...x, sizes })) }
}

/** Drops panes whose host is not in `hostIds` (the last pane becomes empty). */
export function pruneHosts(l: Layout, hostIds: Set<string>): Layout {
  let out = l
  for (const p of leaves(l.root)) if (p.ref && !hostIds.has(p.ref.host)) out = closePane(out, p.id)
  return out
}

/**
 * Resolves restored panes against live data. A pane waits while its host is not
 * connected or its sessions are unknown; then it either becomes a live pane or
 * an empty one noting the session is gone.
 */
export function reconcile(
  l: Layout,
  hosts: ReadonlyArray<{ id: string; state: string }>,
  sessions: Record<string, ReadonlyArray<{ id: string }> | undefined>
): Layout {
  let changed = false
  const walk = (n: Node): Node => {
    if (n.kind === 'split') {
      const children = n.children.map(walk)
      return children.some((c, i) => c !== n.children[i]) ? { ...n, children } : n
    }
    if (!n.pending || !n.ref) return n
    const ref = n.ref
    const host = hosts.find((h) => h.id === ref.host)
    const list = sessions[ref.host]
    if (!host || host.state !== 'connected' || !list) return n
    changed = true
    return list.some((s) => s.id === ref.session)
      ? { kind: 'pane', id: n.id, ref }
      : { kind: 'pane', id: n.id, ref: null, gone: true }
  }
  const root = walk(l.root)
  return changed ? { ...l, root } : l
}

export interface DividerGeom {
  path: number[]
  index: number
  dir: Dir
  /** The split's own rectangle (fractions of the whole layout). */
  rect: Rect
  /** Divider position in layout fractions along the split's axis. */
  at: number
  sizes: number[]
}

/** Every divider with its geometry, for rendering all panes flat (so a pane never remounts when the tree changes). */
export function dividers(n: Node, r: Rect = { x: 0, y: 0, w: 1, h: 1 }, path: number[] = []): DividerGeom[] {
  if (n.kind === 'pane') return []
  const out: DividerGeom[] = []
  let off = 0
  n.children.forEach((c, i) => {
    const f = n.sizes[i]
    const cr =
      n.dir === 'row' ? { x: r.x + off * r.w, y: r.y, w: f * r.w, h: r.h } : { x: r.x, y: r.y + off * r.h, w: r.w, h: f * r.h }
    off += f
    if (i < n.children.length - 1) {
      out.push({ path, index: i, dir: n.dir, rect: r, at: n.dir === 'row' ? r.x + off * r.w : r.y + off * r.h, sizes: n.sizes })
    }
    out.push(...dividers(c, cr, [...path, i]))
  })
  return out
}
