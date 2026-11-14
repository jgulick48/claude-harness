import { describe, expect, it } from 'vitest'
import { clipboardAction, isFindKey, type KeyLike } from './terminalKeys'

const key = (k: string, mods: Partial<KeyLike> = {}): KeyLike => ({
  type: 'keydown',
  key: k,
  ctrlKey: false,
  shiftKey: false,
  altKey: false,
  metaKey: false,
  ...mods
})

describe('clipboardAction (Windows/Linux)', () => {
  it('pastes on Ctrl+V, Ctrl+Shift+V and Shift+Insert', () => {
    expect(clipboardAction(key('v', { ctrlKey: true }), false, false)).toBe('paste')
    expect(clipboardAction(key('V', { ctrlKey: true, shiftKey: true }), false, false)).toBe('paste')
    expect(clipboardAction(key('Insert', { shiftKey: true }), false, false)).toBe('paste')
  })
  it('copies on Ctrl+Shift+C always, and on Ctrl+C / Ctrl+Insert only with a selection', () => {
    expect(clipboardAction(key('C', { ctrlKey: true, shiftKey: true }), false, false)).toBe('copy')
    expect(clipboardAction(key('c', { ctrlKey: true }), false, true)).toBe('copy')
    expect(clipboardAction(key('Insert', { ctrlKey: true }), false, true)).toBe('copy')
  })
  it('leaves Ctrl+C as interrupt when nothing is selected', () => {
    expect(clipboardAction(key('c', { ctrlKey: true }), false, false)).toBeNull()
    expect(clipboardAction(key('Insert', { ctrlKey: true }), false, false)).toBeNull()
  })
  it('ignores other keys, keyup/keypress, and Alt/Meta combinations', () => {
    expect(clipboardAction(key('v'), false, false)).toBeNull()
    expect(clipboardAction(key('a', { ctrlKey: true }), false, false)).toBeNull()
    expect(clipboardAction({ ...key('v', { ctrlKey: true }), type: 'keyup' }, false, false)).toBeNull()
    expect(clipboardAction({ ...key('v', { ctrlKey: true }), type: 'keypress' }, false, false)).toBeNull()
    expect(clipboardAction(key('v', { ctrlKey: true, altKey: true }), false, false)).toBeNull()
  })
})

describe('clipboardAction (macOS)', () => {
  it('handles nothing: Cmd+V / Cmd+C work natively and Ctrl+V stays a control key', () => {
    expect(clipboardAction(key('v', { metaKey: true }), true, false)).toBeNull()
    expect(clipboardAction(key('v', { ctrlKey: true }), true, false)).toBeNull()
    expect(clipboardAction(key('c', { ctrlKey: true }), true, true)).toBeNull()
  })
})

describe('isFindKey', () => {
  it('is Cmd+F on macOS only', () => {
    expect(isFindKey(key('f', { metaKey: true }), true)).toBe(true)
    expect(isFindKey(key('F', { metaKey: true }), true)).toBe(true)
    expect(isFindKey(key('f', { ctrlKey: true }), true)).toBe(false)
    expect(isFindKey(key('f', { metaKey: true, shiftKey: true }), true)).toBe(false)
    expect(isFindKey(key('f', { metaKey: true, altKey: true }), true)).toBe(false)
    expect(isFindKey(key('f', { metaKey: true, ctrlKey: true }), true)).toBe(false)
    expect(isFindKey(key('f', { ctrlKey: true, shiftKey: true }), true)).toBe(false)
  })
  it('is Ctrl+Shift+F elsewhere, never plain Ctrl+F', () => {
    expect(isFindKey(key('F', { ctrlKey: true, shiftKey: true }), false)).toBe(true)
    expect(isFindKey(key('f', { ctrlKey: true, shiftKey: true }), false)).toBe(true)
    expect(isFindKey(key('f', { ctrlKey: true }), false)).toBe(false)
    expect(isFindKey(key('f', { ctrlKey: true, shiftKey: true, altKey: true }), false)).toBe(false)
    expect(isFindKey(key('f', { ctrlKey: true, shiftKey: true, metaKey: true }), false)).toBe(false)
    expect(isFindKey(key('f', { metaKey: true }), false)).toBe(false)
    expect(isFindKey(key('g', { ctrlKey: true, shiftKey: true }), false)).toBe(false)
  })
  it('ignores keyup', () => {
    expect(isFindKey({ ...key('f', { metaKey: true }), type: 'keyup' }, true)).toBe(false)
    expect(isFindKey({ ...key('F', { ctrlKey: true, shiftKey: true }), type: 'keyup' }, false)).toBe(false)
  })
})
