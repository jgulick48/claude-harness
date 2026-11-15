import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { captureState, DEFAULT_STATE, fitToDisplays, loadState, parseState, saveState } from './windowState'

const screen = { x: 0, y: 0, width: 1920, height: 1040 }
const right = { x: 1920, y: 0, width: 2560, height: 1400 }

describe('parseState', () => {
  it('accepts a full state', () => {
    expect(parseState({ x: 10, y: 20, width: 900, height: 700, maximized: true, fullScreen: false })).toEqual({
      x: 10,
      y: 20,
      width: 900,
      height: 700,
      maximized: true,
      fullScreen: false
    })
  })
  it('keeps negative positions (displays left of or above the primary)', () => {
    expect(parseState({ x: -1500, y: -200, width: 900, height: 700 })).toMatchObject({ x: -1500, y: -200 })
  })
  it('drops a partial or non-integer position', () => {
    expect(parseState({ x: 10, width: 900, height: 700 })).not.toHaveProperty('x')
    expect(parseState({ x: 1.5, y: 2, width: 900, height: 700 })).not.toHaveProperty('x')
  })
  it('falls back to defaults for junk or tiny sizes', () => {
    for (const raw of [null, 'x', 42, [], {}, { width: '900', height: 700 }, { width: 100, height: 700 }, { width: 900, height: NaN }]) {
      expect(parseState(raw)).toEqual(DEFAULT_STATE)
    }
  })
  it('only true counts as maximized / full screen', () => {
    expect(parseState({ width: 900, height: 700, maximized: 'yes', fullScreen: 1 })).toMatchObject({ maximized: false, fullScreen: false })
  })
})

describe('fitToDisplays', () => {
  const s = { x: 100, y: 50, width: 1000, height: 700, maximized: false, fullScreen: false }
  it('keeps a window that is on screen', () => {
    expect(fitToDisplays(s, [screen])).toEqual(s)
  })
  it('keeps a window on a secondary display', () => {
    const on2 = { ...s, x: 2500, y: 100 }
    expect(fitToDisplays(on2, [screen, right])).toEqual(on2)
  })
  it('centres a window whose display is gone', () => {
    const out = fitToDisplays({ ...s, x: 2500, y: 100 }, [screen])
    expect(out).not.toHaveProperty('x')
    expect(out).not.toHaveProperty('y')
    expect(out.width).toBe(1000)
  })
  it('centres a window whose title bar is above every display', () => {
    expect(fitToDisplays({ ...s, y: -600 }, [screen])).not.toHaveProperty('x')
  })
  it('keeps a window only slightly off the edge', () => {
    expect(fitToDisplays({ ...s, x: 1500 }, [screen])).toMatchObject({ x: 1500 })
  })
  it('caps the size to the largest display', () => {
    expect(fitToDisplays({ ...s, width: 5000, height: 3000 }, [screen])).toMatchObject({ width: 1920, height: 1040 })
    expect(fitToDisplays({ ...s, width: 5000, height: 3000 }, [screen, right])).toMatchObject({ width: 2560, height: 1400 })
  })
  it('keeps flags and leaves the state alone without displays', () => {
    const m = { ...s, maximized: true }
    expect(fitToDisplays(m, [screen]).maximized).toBe(true)
    expect(fitToDisplays(m, [])).toEqual(m)
  })
})

describe('captureState', () => {
  it('rounds bounds', () => {
    expect(captureState({ x: 10.4, y: 20.6, width: 900.5, height: 700.2 }, true, false)).toEqual({
      x: 10,
      y: 21,
      width: 901,
      height: 700,
      maximized: true,
      fullScreen: false
    })
  })
})

describe('loadState / saveState', () => {
  let dir = ''
  afterEach(() => {
    if (dir) rmSync(dir, { recursive: true, force: true })
  })
  it('round-trips through the file, creating its directory', () => {
    dir = mkdtempSync(join(tmpdir(), 'winstate-'))
    const file = join(dir, 'sub', 'window-state.json')
    const s = { x: 5, y: 6, width: 800, height: 600, maximized: false, fullScreen: true }
    saveState(file, s)
    expect(loadState(file)).toEqual(s)
    expect(JSON.parse(readFileSync(file, 'utf8'))).toEqual(s)
  })
  it('gives defaults for a missing or corrupt file', () => {
    dir = mkdtempSync(join(tmpdir(), 'winstate-'))
    expect(loadState(join(dir, 'nope.json'))).toEqual(DEFAULT_STATE)
    const bad = join(dir, 'bad.json')
    writeFileSync(bad, '{not json')
    expect(loadState(bad)).toEqual(DEFAULT_STATE)
  })
})
