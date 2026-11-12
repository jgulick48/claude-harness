import { describe, expect, it } from 'vitest'
import { layoutCommand, shortcutHints } from './shortcuts'

const key = (k: string, m: Partial<{ ctrl: boolean; shift: boolean; alt: boolean; meta: boolean }> = {}) => ({
  type: 'keydown', key: k, ctrlKey: !!m.ctrl, shiftKey: !!m.shift, altKey: !!m.alt, metaKey: !!m.meta
})

describe('layout shortcuts on macOS', () => {
  it('maps Cmd bindings', () => {
    expect(layoutCommand(key('d', { meta: true }), true)).toEqual({ type: 'splitRight' })
    expect(layoutCommand(key('D', { meta: true, shift: true }), true)).toEqual({ type: 'splitDown' })
    expect(layoutCommand(key('w', { meta: true }), true)).toEqual({ type: 'closePane' })
    expect(layoutCommand(key('ArrowLeft', { meta: true, alt: true }), true)).toEqual({ type: 'focus', dir: 'left' })
  })
  it('leaves Ctrl+D alone', () => {
    expect(layoutCommand(key('d', { ctrl: true }), true)).toBeNull()
  })
})

describe('layout shortcuts on Windows/Linux', () => {
  it('uses Ctrl+Shift', () => {
    expect(layoutCommand(key('D', { ctrl: true, shift: true }), false)).toEqual({ type: 'splitRight' })
    expect(layoutCommand(key('E', { ctrl: true, shift: true }), false)).toEqual({ type: 'splitDown' })
    expect(layoutCommand(key('W', { ctrl: true, shift: true }), false)).toEqual({ type: 'closePane' })
    expect(layoutCommand(key('ArrowDown', { ctrl: true, alt: true }), false)).toEqual({ type: 'focus', dir: 'down' })
  })
  it('lets plain Ctrl+D (EOF) and Ctrl+W through to the terminal', () => {
    expect(layoutCommand(key('d', { ctrl: true }), false)).toBeNull()
    expect(layoutCommand(key('w', { ctrl: true }), false)).toBeNull()
    expect(layoutCommand(key('e', { ctrl: true }), false)).toBeNull()
  })
  it('ignores keyups, Cmd, and extra modifiers', () => {
    expect(layoutCommand({ ...key('D', { ctrl: true, shift: true }), type: 'keyup' }, false)).toBeNull()
    expect(layoutCommand(key('D', { meta: true, shift: true }), false)).toBeNull()
    expect(layoutCommand(key('D', { ctrl: true, shift: true, alt: true }), false)).toBeNull()
  })
})

it('hints mirror the platform', () => {
  expect(shortcutHints(false).map((h) => h.keys)).toContain('Ctrl+Shift+D')
  expect(shortcutHints(true).map((h) => h.keys)).toContain('⌘D')
})
