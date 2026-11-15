// Remembers the main window's size, position and maximized/full-screen state
// across restarts. Pure helpers plus a small file store; index.ts wires them
// to the BrowserWindow and Electron's screen module.

import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

export interface Rect {
  x: number
  y: number
  width: number
  height: number
}

export interface WindowState {
  /** Normal (not maximized) bounds. x/y are absent when the window should be centred. */
  x?: number
  y?: number
  width: number
  height: number
  maximized: boolean
  fullScreen: boolean
}

export const DEFAULT_STATE: WindowState = { width: 1200, height: 800, maximized: false, fullScreen: false }
export const MIN_WIDTH = 400
export const MIN_HEIGHT = 300
/** How much of the title bar area must be on a display for a saved position to be kept. */
const MIN_VISIBLE = 100

const isInt = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && Number.isInteger(v)

/** Validates parsed JSON into a WindowState; anything unusable gives the defaults. */
export function parseState(raw: unknown): WindowState {
  if (!raw || typeof raw !== 'object') return { ...DEFAULT_STATE }
  const r = raw as Record<string, unknown>
  if (!isInt(r.width) || !isInt(r.height) || r.width < MIN_WIDTH || r.height < MIN_HEIGHT) return { ...DEFAULT_STATE }
  const s: WindowState = {
    width: r.width,
    height: r.height,
    maximized: r.maximized === true,
    fullScreen: r.fullScreen === true
  }
  if (isInt(r.x) && isInt(r.y)) {
    s.x = r.x
    s.y = r.y
  }
  return s
}

function overlap(a: Rect, b: Rect): { w: number; h: number } {
  return {
    w: Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x),
    h: Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y)
  }
}

/**
 * Fits a saved state to the current displays (their work areas). The size is
 * capped to the largest display; a position whose top strip isn't at least
 * partly on some display (e.g. that monitor was unplugged) is dropped so the
 * window is centred instead.
 */
export function fitToDisplays(s: WindowState, workAreas: Rect[]): WindowState {
  if (workAreas.length === 0) return s
  const maxW = Math.max(...workAreas.map((a) => a.width))
  const maxH = Math.max(...workAreas.map((a) => a.height))
  const out: WindowState = {
    ...s,
    width: Math.max(MIN_WIDTH, Math.min(s.width, maxW)),
    height: Math.max(MIN_HEIGHT, Math.min(s.height, maxH))
  }
  if (out.x === undefined || out.y === undefined) return out
  // The top strip of the window is where it can be grabbed and moved.
  const strip: Rect = { x: out.x, y: out.y, width: out.width, height: Math.min(out.height, 40) }
  const visible = workAreas.some((a) => {
    const o = overlap(strip, a)
    return o.w >= Math.min(MIN_VISIBLE, out.width) && o.h > 0
  })
  if (!visible) {
    delete out.x
    delete out.y
  }
  return out
}

/** Reads the state file; a missing or corrupt file gives the defaults. */
export function loadState(file: string): WindowState {
  try {
    return parseState(JSON.parse(readFileSync(file, 'utf8')))
  } catch {
    return { ...DEFAULT_STATE }
  }
}

/** Writes the state file atomically. Errors are swallowed: losing it only costs the default size. */
export function saveState(file: string, s: WindowState): void {
  try {
    mkdirSync(dirname(file), { recursive: true })
    const tmp = `${file}.${process.pid}.tmp`
    writeFileSync(tmp, JSON.stringify(s), { mode: 0o600 })
    renameSync(tmp, file)
  } catch {
    // ignore
  }
}

/** The state to save for a window, from its normal bounds and flags. */
export function captureState(normal: Rect, maximized: boolean, fullScreen: boolean): WindowState {
  return {
    x: Math.round(normal.x),
    y: Math.round(normal.y),
    width: Math.round(normal.width),
    height: Math.round(normal.height),
    maximized,
    fullScreen
  }
}
