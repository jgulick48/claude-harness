import { describe, expect, it } from 'vitest'
import {
  attentionCount,
  attentionRank,
  attentionTitle,
  canRemoveHooks,
  compareAttention,
  createDebounce,
  hooksNotice,
  isViewing,
  newlyWaiting,
  notificationText,
  orderSessions,
  relativeTime,
  stateAge,
  statusText,
  statusInfo,
  statusTooltip,
  UNKNOWN_TOOLTIP
} from './claudeStatus'
import type { ClaudeStatus, Session } from './sessions'

const s = (id: string, claudeStatus?: ClaudeStatus, created = 1): Session => ({
  id, name: id, harnessName: '', managed: true, cwd: '/x', windows: 1, attached: 0, created, claudeStatus
})

describe('statusInfo', () => {
  it('maps each status to icon and label', () => {
    expect(statusInfo({ claudeStatus: 'working' })).toMatchObject({ icon: '◐', label: 'Working' })
    expect(statusInfo({ claudeStatus: 'waiting' })).toMatchObject({ icon: '◉', label: 'Waiting' })
    expect(statusInfo({ claudeStatus: 'idle' })).toMatchObject({ icon: '✓', label: 'Idle' })
    expect(statusInfo({ claudeStatus: 'unknown' })).toMatchObject({ icon: '?', label: 'Unknown' })
  })
  it('is null for empty, absent and unrecognised values', () => {
    expect(statusInfo({ claudeStatus: '' })).toBeNull()
    expect(statusInfo({})).toBeNull()
    expect(statusInfo({ claudeStatus: 'toString' as ClaudeStatus })).toBeNull()
  })
})

describe('relativeTime', () => {
  const now = 1_000_000 * 1000
  it('formats units', () => {
    expect(relativeTime(1_000_000 - 2, now)).toBe('just now')
    expect(relativeTime(1_000_000 - 30, now)).toBe('30s ago')
    expect(relativeTime(1_000_000 - 180, now)).toBe('3m ago')
    expect(relativeTime(1_000_000 - 7300, now)).toBe('2h ago')
    expect(relativeTime(1_000_000 - 3 * 86400, now)).toBe('3d ago')
  })
  it('never goes negative with clock skew', () => {
    expect(relativeTime(1_000_000 + 50, now)).toBe('just now')
  })
})

describe('stateAge', () => {
  const now = 1_000_000 * 1000
  it('is empty under a minute, without a time, or in the future', () => {
    expect(stateAge(undefined, now)).toBe('')
    expect(stateAge(0, now)).toBe('')
    expect(stateAge(1_000_000 - 59, now)).toBe('')
    expect(stateAge(1_000_000 + 600, now)).toBe('')
  })
  it('counts minutes, hours and days', () => {
    expect(stateAge(1_000_000 - 60, now)).toBe('1m')
    expect(stateAge(1_000_000 - 59 * 60 - 59, now)).toBe('59m')
    expect(stateAge(1_000_000 - 3600, now)).toBe('1h')
    expect(stateAge(1_000_000 - 23 * 3600 - 3599, now)).toBe('23h')
    expect(stateAge(1_000_000 - 2 * 86400, now)).toBe('2d')
  })
})

describe('statusText', () => {
  const now = 1_000_000 * 1000
  it('adds the age to the label', () => {
    expect(statusText({ claudeStatus: 'waiting', claudeStatusAt: 1_000_000 - 720 }, now)).toBe('Waiting 12m')
    expect(statusText({ claudeStatus: 'working', claudeStatusAt: 1_000_000 - 10 }, now)).toBe('Working')
    expect(statusText({ claudeStatus: 'unknown', claudeStatusAt: 1_000_000 - 720 }, now)).toBe('Unknown')
    expect(statusText({ claudeStatus: '' }, now)).toBe('')
  })
})

describe('statusTooltip', () => {
  const now = 1_000_000 * 1000
  it('says since when', () => {
    expect(statusTooltip({ claudeStatus: 'waiting', claudeStatusAt: 1_000_000 - 120 }, now)).toBe('since 2m ago')
  })
  it('explains unknown', () => {
    expect(statusTooltip({ claudeStatus: 'unknown' }, now)).toBe(UNKNOWN_TOOLTIP)
    expect(statusTooltip({ claudeStatus: 'unknown', claudeStatusAt: 1_000_000 - 60 }, now)).toBe(
      `${UNKNOWN_TOOLTIP}\nsince 1m ago`
    )
  })
  it('falls back to the label, and is empty with no status', () => {
    expect(statusTooltip({ claudeStatus: 'idle' }, now)).toBe('Idle')
    expect(statusTooltip({ claudeStatus: '' }, now)).toBe('')
  })
})

describe('attention sort', () => {
  it('ranks Waiting, Working, Idle, Unknown, none', () => {
    const order: (ClaudeStatus | undefined)[] = ['waiting', 'working', 'idle', 'unknown', '', undefined]
    const ranks = order.map((c) => attentionRank({ claudeStatus: c }))
    expect(ranks.slice(0, 5)).toEqual([0, 1, 2, 3, 4])
    expect(ranks[5]).toBe(4)
  })
  it('compares by rank, 0 when equal', () => {
    expect(compareAttention(s('a', 'waiting'), s('b', 'idle'))).toBeLessThan(0)
    expect(compareAttention(s('a', 'working'), s('b', 'working'))).toBe(0)
  })
  it('orders with manual order as tiebreak', () => {
    const list = [s('$1', 'idle', 1), s('$2', '', 2), s('$3', 'waiting', 3), s('$4', 'working', 4), s('$5', 'waiting', 2), s('$6', 'unknown', 5)]
    expect(orderSessions(list, true).map((x) => x.id)).toEqual(['$5', '$3', '$4', '$1', '$6', '$2'])
    expect(orderSessions(list, false).map((x) => x.id)).toEqual(['$1', '$2', '$5', '$3', '$4', '$6'])
  })
  it('does not mutate', () => {
    const list = [s('$2', 'waiting', 2), s('$1', '', 1)]
    orderSessions(list, true)
    expect(list.map((x) => x.id)).toEqual(['$2', '$1'])
  })
})

describe('newlyWaiting', () => {
  it('ignores first sight of a host', () => {
    expect(newlyWaiting(undefined, [s('$1', 'waiting')])).toEqual([])
  })
  it('ignores a session seen for the first time already waiting', () => {
    expect(newlyWaiting([s('$1', 'idle')], [s('$1', 'idle'), s('$2', 'waiting')])).toEqual([])
  })
  it('detects a real transition', () => {
    expect(newlyWaiting([s('$1', 'working')], [s('$1', 'waiting')]).map((x) => x.id)).toEqual(['$1'])
    expect(newlyWaiting([s('$1', '')], [s('$1', 'waiting')]).map((x) => x.id)).toEqual(['$1'])
    expect(newlyWaiting([s('$1')], [s('$1', 'waiting')]).map((x) => x.id)).toEqual(['$1'])
  })
  it('ignores a repeat waiting', () => {
    expect(newlyWaiting([s('$1', 'waiting')], [s('$1', 'waiting')])).toEqual([])
  })
  it('detects waiting -> working -> waiting', () => {
    const w = [s('$1', 'waiting')]
    const k = [s('$1', 'working')]
    expect(newlyWaiting(w, k)).toEqual([])
    expect(newlyWaiting(k, w)).toHaveLength(1)
  })
  it('only reports the sessions that changed', () => {
    const prev = [s('$1', 'working'), s('$2', 'waiting'), s('$3', 'idle')]
    const next = [s('$1', 'waiting'), s('$2', 'waiting'), s('$3', 'idle')]
    expect(newlyWaiting(prev, next).map((x) => x.id)).toEqual(['$1'])
  })
})

describe('createDebounce', () => {
  it('suppresses repeats of a key inside the window only', () => {
    const allow = createDebounce(10_000)
    expect(allow('h/$1', 0)).toBe(true)
    expect(allow('h/$1', 9_999)).toBe(false)
    expect(allow('h/$2', 5_000)).toBe(true)
    expect(allow('h/$1', 10_000)).toBe(true)
  })
  it('suppressed attempts do not extend the window', () => {
    const allow = createDebounce(10_000)
    allow('k', 0)
    allow('k', 9_000)
    expect(allow('k', 10_000)).toBe(true)
  })
})

describe('attention count and title', () => {
  it('sums waiting across hosts', () => {
    expect(attentionCount({ a: [s('$1', 'waiting'), s('$2', 'idle')], b: [s('$1', 'waiting')], c: [] })).toBe(2)
    expect(attentionCount({})).toBe(0)
  })
  it('formats the title', () => {
    expect(attentionTitle(2)).toBe('(2) Claude Harness')
    expect(attentionTitle(0)).toBe('Claude Harness')
  })
})

describe('notifications', () => {
  it('builds text', () => {
    expect(notificationText('api', 'devbox', '~/work/api')).toEqual({
      title: 'api needs you',
      body: 'devbox · ~/work/api'
    })
  })
  it('suppresses only when active and focused', () => {
    const active = { host: 'h', session: '$1' }
    expect(isViewing(active, 'h', '$1', true)).toBe(true)
    expect(isViewing(active, 'h', '$1', false)).toBe(false)
    expect(isViewing(active, 'h', '$2', true)).toBe(false)
    expect(isViewing(null, 'h', '$1', true)).toBe(false)
  })
})

describe('hooks notice', () => {
  it('maps hook states', () => {
    expect(hooksNotice({ claudeHooks: 'missing' })).toBe('install')
    expect(hooksNotice({ claudeHooks: 'outdated' })).toBe('update')
    expect(hooksNotice({ claudeHooks: 'installed' })).toBeNull()
    expect(hooksNotice({ claudeHooks: 'unknown' })).toBeNull()
    expect(hooksNotice({})).toBeNull()
  })
  it('allows removal when installed or outdated', () => {
    expect(canRemoveHooks({ claudeHooks: 'installed' })).toBe(true)
    expect(canRemoveHooks({ claudeHooks: 'outdated' })).toBe(true)
    expect(canRemoveHooks({ claudeHooks: 'missing' })).toBe(false)
    expect(canRemoveHooks({ claudeHooks: 'unknown' })).toBe(false)
    expect(canRemoveHooks({})).toBe(false)
  })
})
