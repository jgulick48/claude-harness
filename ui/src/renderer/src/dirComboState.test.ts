import { describe, expect, it } from 'vitest'
import {
  comboReducer,
  completionItems,
  handleComboKey,
  initialCombo,
  isVisible,
  suggestItems,
  wantsSuggestions,
  type ComboState
} from './dirComboState'

const withItems = (paths: string[], extra: Partial<ComboState> = {}): ComboState => ({
  ...initialCombo,
  items: paths.map((path) => ({ path })),
  ...extra
})

describe('comboReducer', () => {
  it('applies results for the latest request', () => {
    let s = comboReducer(initialCombo, { type: 'request', seq: 1 })
    s = comboReducer(s, { type: 'results', seq: 1, items: [{ path: '~/a/' }] })
    expect(s.items).toEqual([{ path: '~/a/' }])
    expect(s.active).toBe(-1)
  })
  it('drops stale responses', () => {
    let s = comboReducer(initialCombo, { type: 'request', seq: 1 })
    s = comboReducer(s, { type: 'request', seq: 2 })
    const before = comboReducer(s, { type: 'results', seq: 2, items: [{ path: 'new' }] })
    const after = comboReducer(before, { type: 'results', seq: 1, items: [{ path: 'old' }] })
    expect(after.items).toEqual([{ path: 'new' }])
    expect(comboReducer(before, { type: 'failed', seq: 1, message: 'x' }).error).toBeNull()
  })
  it('records a quiet error and clears items', () => {
    let s = comboReducer(withItems(['a']), { type: 'request', seq: 3 })
    s = comboReducer(s, { type: 'failed', seq: 3, message: 'boom' })
    expect(s.error).toBe('boom')
    expect(s.items).toEqual([])
  })
})

describe('handleComboKey', () => {
  it('moves down and up, clamped, up from first clears the highlight', () => {
    let s = withItems(['a', 'b'])
    s = handleComboKey(s, 'ArrowDown').state
    expect(s.active).toBe(0)
    s = handleComboKey(s, 'ArrowDown').state
    s = handleComboKey(s, 'ArrowDown').state
    expect(s.active).toBe(1)
    s = handleComboKey(s, 'ArrowUp').state
    s = handleComboKey(s, 'ArrowUp').state
    expect(s.active).toBe(-1)
  })
  it('ArrowDown reopens a closed list without moving', () => {
    const s = handleComboKey(withItems(['a'], { open: false }), 'ArrowDown').state
    expect(s.open).toBe(true)
    expect(s.active).toBe(-1)
  })
  it.each(['Tab', 'ArrowRight', 'Enter'])('%s accepts the highlighted entry', (key) => {
    const r = handleComboKey(withItems(['a/', 'b/'], { active: 1 }), key)
    expect(r.accept).toBe('b/')
    expect(r.handled).toBe(true)
    expect(r.state.active).toBe(-1)
  })
  it.each(['Tab', 'ArrowRight', 'Enter'])('%s without highlight is not handled', (key) => {
    const r = handleComboKey(withItems(['a/']), key)
    expect(r.accept).toBeNull()
    expect(r.handled).toBe(false)
  })
  it('Esc closes the list first, then falls through to the dialog', () => {
    const first = handleComboKey(withItems(['a/'], { active: 0 }), 'Escape')
    expect(first.handled).toBe(true)
    expect(isVisible(first.state)).toBe(false)
    const second = handleComboKey(first.state, 'Escape')
    expect(second.handled).toBe(false)
  })
  it('Esc with an empty list is not handled', () => {
    expect(handleComboKey(initialCombo, 'Escape').handled).toBe(false)
  })
  it('ignores other keys', () => {
    expect(handleComboKey(withItems(['a']), 'x').handled).toBe(false)
  })
})

describe('suggestions', () => {
  it('wants suggestions for empty and ~', () => {
    expect(wantsSuggestions('')).toBe(true)
    expect(wantsSuggestions(' ~ ')).toBe(true)
    expect(wantsSuggestions('~/')).toBe(false)
  })
  it('lists Recent first then Repositories, deduped', () => {
    const items = suggestItems({ recent: ['~/work/api'], repos: ['~/work/api/', '~/src/app/'] })
    expect(items).toEqual([
      { path: '~/work/api', group: 'Recent' },
      { path: '~/src/app/', gitRepo: true, group: 'Repositories' }
    ])
  })
  it('tolerates missing arrays', () => {
    expect(suggestItems({})).toEqual([])
    expect(completionItems(null)).toEqual([])
    expect(completionItems([{ path: 'a/', gitRepo: true }, { path: 'b/' }])).toEqual([
      { path: 'a/', gitRepo: true },
      { path: 'b/', gitRepo: false }
    ])
  })
})
