import { describe, expect, it } from 'vitest'
import { displayPath, sessionLabel, sortSessions, truncateMiddle, type Session } from './sessions'

const mk = (o: Partial<Session>): Session => ({
  id: '$1', name: 'h-x-1', harnessName: '', managed: true, cwd: '', windows: 1, attached: 0, created: 0, ...o
})

describe('sessionLabel', () => {
  it('prefers harnessName', () => {
    expect(sessionLabel(mk({ harnessName: 'api', cwd: '/a/b' }))).toBe('api')
  })
  it('falls back to basename(cwd)', () => {
    expect(sessionLabel(mk({ cwd: '/home/me/work/api' }))).toBe('api')
    expect(sessionLabel(mk({ cwd: '/home/me/work/api/' }))).toBe('api')
    expect(sessionLabel(mk({ cwd: 'C:\\work\\api' }))).toBe('api')
  })
  it('falls back to name', () => {
    expect(sessionLabel(mk({ cwd: '' }))).toBe('h-x-1')
    expect(sessionLabel(mk({ cwd: '/' }))).toBe('h-x-1')
  })
})

describe('displayPath', () => {
  it('substitutes ~', () => {
    expect(displayPath('/home/me', '/home/me')).toBe('~')
    expect(displayPath('/home/me/work', '/home/me')).toBe('~/work')
    expect(displayPath('/home/me/work', '/home/me/')).toBe('~/work')
  })
  it('does not match sibling prefixes', () => {
    expect(displayPath('/home/mean/x', '/home/me')).toBe('/home/mean/x')
  })
  it('handles empty values and root home', () => {
    expect(displayPath('', '/home/me')).toBe('')
    expect(displayPath('/tmp', '')).toBe('/tmp')
    expect(displayPath('/tmp', '/')).toBe('/tmp')
  })
})

describe('truncateMiddle', () => {
  it('leaves short strings alone', () => {
    expect(truncateMiddle('~/a', 10)).toBe('~/a')
  })
  it('truncates in the middle to max length', () => {
    const r = truncateMiddle('~/go/src/github.com/jgulick48/claude-harness', 20)
    expect(r).toHaveLength(20)
    expect(r).toContain('…')
    expect(r.startsWith('~/go/src')).toBe(true)
    expect(r.endsWith('harness')).toBe(true)
  })
})

describe('sortSessions', () => {
  it('orders by created ascending without mutating', () => {
    const input = [mk({ id: '$2', created: 30 }), mk({ id: '$1', created: 10 }), mk({ id: '$3', created: 20 })]
    expect(sortSessions(input).map((s) => s.id)).toEqual(['$1', '$3', '$2'])
    expect(input[0].id).toBe('$2')
  })
  it('breaks ties by numeric id', () => {
    const r = sortSessions([mk({ id: '$10', created: 1 }), mk({ id: '$9', created: 1 })])
    expect(r.map((s) => s.id)).toEqual(['$9', '$10'])
  })
})
