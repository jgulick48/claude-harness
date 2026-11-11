import { describe, expect, it } from 'vitest'
import { canRunCheckFromStatus, hooksNeedInstall, needsFix, overallStatus, statusView, summarize } from './hostCheck'

const it_ = (status: 'ok' | 'warn' | 'error', id = 'x') => ({ id, label: id, status, detail: '' })

describe('summarize', () => {
  it('reports all good', () => expect(summarize([it_('ok'), it_('ok')])).toBe('All good'))
  it('empty is all good', () => expect(summarize([])).toBe('All good'))
  it('pluralizes warnings', () => {
    expect(summarize([it_('warn')])).toBe('1 warning')
    expect(summarize([it_('warn'), it_('warn'), it_('ok')])).toBe('2 warnings')
  })
  it('pluralizes problems', () => {
    expect(summarize([it_('error')])).toBe('1 problem')
    expect(summarize([it_('error'), it_('error')])).toBe('2 problems')
  })
  it('combines', () => expect(summarize([it_('error'), it_('warn'), it_('warn')])).toBe('1 problem, 2 warnings'))
})

describe('statusView', () => {
  it('maps icons and text', () => {
    expect(statusView('ok')).toEqual({ icon: '✓', text: 'OK' })
    expect(statusView('warn')).toEqual({ icon: '⚠', text: 'Warning' })
    expect(statusView('error')).toEqual({ icon: '✕', text: 'Problem' })
  })
})

describe('helpers', () => {
  it('overallStatus is the worst', () => {
    expect(overallStatus([it_('ok')])).toBe('ok')
    expect(overallStatus([it_('ok'), it_('warn')])).toBe('warn')
    expect(overallStatus([it_('warn'), it_('error')])).toBe('error')
  })
  it('needsFix', () => {
    expect(needsFix(it_('ok'))).toBe(false)
    expect(needsFix(it_('warn'))).toBe(true)
  })
  it('hooksNeedInstall', () => {
    expect(hooksNeedInstall(null)).toBe(false)
    expect(hooksNeedInstall({ items: [it_('ok', 'hooks')] })).toBe(false)
    expect(hooksNeedInstall({ items: [it_('warn', 'tmux')] })).toBe(false)
    expect(hooksNeedInstall({ items: [it_('warn', 'hooks')] })).toBe(true)
  })
  it('canRunCheckFromStatus', () => {
    expect(canRunCheckFromStatus('unavailable')).toBe(true)
    expect(canRunCheckFromStatus('error')).toBe(true)
    expect(canRunCheckFromStatus('connected')).toBe(false)
    expect(canRunCheckFromStatus('disconnected')).toBe(false)
  })
})
