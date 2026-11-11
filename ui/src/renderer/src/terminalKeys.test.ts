import { describe, expect, it } from 'vitest'
import { clipboardAction, type KeyLike } from './terminalKeys'

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
