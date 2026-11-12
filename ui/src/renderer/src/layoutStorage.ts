// Saving and restoring layouts. Everything read from storage is untrusted:
// parseLayout validates it and returns null for anything malformed.
import { leaves, MAX_PANES, MIN_FRACTION, type Layout, type Node } from './layoutTree'
import type { StorageLike } from './prefs'

export const LAYOUT_KEY = 'harness.layout'
export const NAMED_LAYOUTS_KEY = 'harness.layouts'
const MAX_DEPTH = 8
const MAX_NAMED = 50

export interface NamedLayout {
  name: string
  layout: unknown
}

type JsonNode =
  | { kind: 'pane'; ref: { host: string; session: string } | null }
  | { kind: 'split'; dir: 'row' | 'column'; sizes: number[]; children: JsonNode[] }
export interface SavedLayout {
  root: JsonNode
  /** Index of the focused pane in depth-first order. */
  focus: number
}

/** What gets stored: no ids and no transient flags. */
export function serializeLayout(l: Layout): SavedLayout {
  const order = leaves(l.root).map((p) => p.id)
  const ser = (n: Node): JsonNode =>
    n.kind === 'pane'
      ? { kind: 'pane', ref: n.ref ? { host: n.ref.host, session: n.ref.session } : null }
      : { kind: 'split', dir: n.dir, sizes: n.sizes, children: n.children.map(ser) }
  return { root: ser(l.root), focus: Math.max(0, order.indexOf(l.focus)) }
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const isStr = (v: unknown): v is string => typeof v === 'string' && v.length > 0 && v.length < 512

/**
 * Validates a stored layout and rebuilds it with fresh pane ids. Refs are
 * marked pending until hosts and sessions are known; a session shown twice
 * keeps only its first pane. Returns null when the shape is wrong.
 */
export function parseLayout(raw: unknown): Layout | null {
  if (!isObj(raw) || !isObj(raw.root)) return null
  let next = 1
  let count = 0
  const seen = new Set<string>()
  const build = (v: unknown, depth: number): Node | null => {
    if (!isObj(v) || depth > MAX_DEPTH) return null
    if (v.kind === 'pane') {
      if (++count > MAX_PANES) return null
      const id = `p${next++}`
      const r = v.ref
      if (r === null || r === undefined) return { kind: 'pane', id, ref: null }
      if (!isObj(r) || !isStr(r.host) || !isStr(r.session)) return null
      const key = `${r.host}\u0000${r.session}`
      if (seen.has(key)) return { kind: 'pane', id, ref: null }
      seen.add(key)
      return { kind: 'pane', id, ref: { host: r.host, session: r.session }, pending: true }
    }
    if (v.kind !== 'split') return null
    if (v.dir !== 'row' && v.dir !== 'column') return null
    if (!Array.isArray(v.children) || v.children.length < 2 || v.children.length > MAX_PANES) return null
    if (!Array.isArray(v.sizes) || v.sizes.length !== v.children.length) return null
    if (!v.sizes.every((s) => typeof s === 'number' && Number.isFinite(s) && s > 0)) return null
    const children: Node[] = []
    for (const c of v.children) {
      const n = build(c, depth + 1)
      if (!n) return null
      children.push(n)
    }
    const total = (v.sizes as number[]).reduce((a, b) => a + b, 0)
    const sizes = (v.sizes as number[]).map((s) => Math.max(s / total, MIN_FRACTION / 2))
    const t2 = sizes.reduce((a, b) => a + b, 0)
    return { kind: 'split', dir: v.dir, children, sizes: sizes.map((s) => s / t2) }
  }
  const root = build(raw.root, 0)
  if (!root) return null
  const ps = leaves(root)
  const fi = typeof raw.focus === 'number' && Number.isInteger(raw.focus) ? raw.focus : 0
  const focus = (ps[fi] ?? ps[0]).id
  return { root, focus, nextId: next }
}

function defaultStorage(): StorageLike | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}
type GetStorage = () => StorageLike | null
type Removable = StorageLike & Partial<Pick<Storage, 'removeItem'>>

const CORRUPT = Symbol('corrupt')

function readJson(key: string, get: GetStorage): unknown {
  try {
    const raw = get()?.getItem(key)
    return raw ? JSON.parse(raw) : null
  } catch {
    return CORRUPT
  }
}
function writeJson(key: string, v: unknown, get: GetStorage): void {
  try {
    get()?.setItem(key, JSON.stringify(v))
  } catch {
    /* the app works without persistence */
  }
}

export function loadLayout(get: GetStorage = defaultStorage): Layout | null {
  const raw = readJson(LAYOUT_KEY, get)
  const l = parseLayout(raw)
  if (raw !== null && !l) {
    try {
      ;(get() as Removable | null)?.removeItem?.(LAYOUT_KEY) // corrupt: drop it
    } catch {
      /* ignore */
    }
  }
  return l
}
export function saveLayout(l: Layout, get: GetStorage = defaultStorage): void {
  writeJson(LAYOUT_KEY, serializeLayout(l), get)
}

/** Named layouts; corrupt entries are dropped. */
export function loadNamedLayouts(get: GetStorage = defaultStorage): Array<{ name: string; layout: Layout }> {
  const raw = readJson(NAMED_LAYOUTS_KEY, get)
  if (!Array.isArray(raw)) return []
  const out: Array<{ name: string; layout: Layout }> = []
  const names = new Set<string>()
  for (const e of raw) {
    if (!isObj(e) || typeof e.name !== 'string' || !e.name.trim() || e.name.length > 80 || names.has(e.name)) continue
    const layout = parseLayout(e.layout)
    if (!layout) continue
    names.add(e.name)
    out.push({ name: e.name, layout })
  }
  return out.slice(0, MAX_NAMED)
}

function writeNamed(list: Array<{ name: string; layout: Layout }>, get: GetStorage): void {
  writeJson(NAMED_LAYOUTS_KEY, list.map((e) => ({ name: e.name, layout: serializeLayout(e.layout) })), get)
}

/** Saves (or overwrites) a named layout; returns the new list. */
export function saveNamedLayout(name: string, l: Layout, get: GetStorage = defaultStorage) {
  const n = name.trim()
  if (!n) return loadNamedLayouts(get)
  const list = loadNamedLayouts(get).filter((e) => e.name !== n)
  list.push({ name: n, layout: l })
  const capped = list.slice(-MAX_NAMED)
  writeNamed(capped, get)
  return loadNamedLayouts(get)
}

export function deleteNamedLayout(name: string, get: GetStorage = defaultStorage) {
  writeNamed(loadNamedLayouts(get).filter((e) => e.name !== name), get)
  return loadNamedLayouts(get)
}
