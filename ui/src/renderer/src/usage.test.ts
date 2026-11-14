import { describe, expect, it } from 'vitest'
import type { Session, UsageLimits } from './sessions'
import {
  canRecordUsage,
  canStopUsage,
  formatCost,
  formatPct,
  formatReset,
  hostLimits,
  lastLimits,
  limitLevel,
  limitLines,
  limitsTooltip,
  paneUsageText,
  paneUsageTooltip,
  rememberLimits,
  resolveLimits,
  USAGE_LIMITS_KEY
} from './usage'
import { relativeTime } from './claudeStatus'

const NOW = 1_800_000_000_000
const sec = NOW / 1000

const sess = (id: string, usage?: Session['usage']): Session => ({
  id, name: id, harnessName: '', managed: true, cwd: '/x', windows: 1, attached: 0, created: 1, usage
})

function memStore(initial?: string) {
  const m = new Map<string, string>()
  if (initial !== undefined) m.set(USAGE_LIMITS_KEY, initial)
  return { m, get: () => ({ getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) }) }
}

describe('hostLimits', () => {
  const lim = (pct: number): UsageLimits => ({
    fiveHour: { usedPct: pct, resetsAt: sec + 3600 },
    sevenDay: { usedPct: pct, resetsAt: sec + 86400 }
  })
  it('takes the reading with the largest at', () => {
    const l = hostLimits([sess('a', { at: 10, limits: lim(10) }), sess('b', { at: 30, limits: lim(30) }), sess('c', { at: 20, limits: lim(20) })], NOW)
    expect(l?.fiveHour?.usedPct).toBe(30)
  })
  it('ignores sessions without limits, even when newer', () => {
    const l = hostLimits([sess('a', { at: 10, limits: lim(10) }), sess('b', { at: 99, costUsd: 1 }), sess('c')], NOW)
    expect(l?.fiveHour?.usedPct).toBe(10)
  })
  it('drops windows that have reset', () => {
    const l = hostLimits([sess('a', { at: 1, limits: { fiveHour: { usedPct: 5, resetsAt: sec - 1 }, sevenDay: { usedPct: 6, resetsAt: sec + 10 } } })], NOW)
    expect(l).toEqual({ sevenDay: { usedPct: 6, resetsAt: sec + 10 } })
  })
  it('is null with nothing, or when every window has reset', () => {
    expect(hostLimits([], NOW)).toBeNull()
    expect(hostLimits(undefined, NOW)).toBeNull()
    expect(hostLimits([sess('a')], NOW)).toBeNull()
    expect(hostLimits([sess('a', { at: 1, limits: { fiveHour: { usedPct: 5, resetsAt: sec } } })], NOW)).toBeNull()
  })
})

describe('formatReset', () => {
  it.each([
    [2 * 3600 + 14 * 60 + 5, 'resets in 2h 14m'],
    [3 * 86400 + 4 * 3600 + 59, 'resets in 3d 4h'],
    [12 * 60 + 1, 'resets in 12m'],
    [3600, 'resets in 1h 0m'],
    [30, 'resets in <1m'],
    [0, 'resets now'],
    [-50, 'resets now']
  ])('%i s ahead', (ahead, want) => {
    expect(formatReset(sec + ahead, NOW)).toBe(want)
  })
})

describe('formatCost / formatPct', () => {
  it('formats costs', () => {
    expect(formatCost(0.0123)).toBe('$0.01')
    expect(formatCost(0)).toBe('<$0.01')
    expect(formatCost(0.004)).toBe('<$0.01')
    expect(formatCost(0.005)).toBe('$0.01')
    expect(formatCost(9.994)).toBe('$9.99')
    expect(formatCost(12.34)).toBe('$12.3')
    expect(formatCost(314.12)).toBe('$314.1')
  })
  it('rounds percentages', () => {
    expect(formatPct(23.5)).toBe('23%')
    expect(formatPct(99.9)).toBe('99%')
    expect(formatPct(42.4)).toBe('42%')
    expect(formatPct(0)).toBe('0%')
  })
})

describe('limitLevel', () => {
  it('has thresholds at 70 and 90', () => {
    expect(limitLevel(0)).toBe('ok')
    expect(limitLevel(69.9)).toBe('ok')
    expect(limitLevel(70)).toBe('warn')
    expect(limitLevel(89.9)).toBe('warn')
    expect(limitLevel(90)).toBe('high')
    expect(limitLevel(100)).toBe('high')
  })
})

describe('last known limits store', () => {
  const limits: UsageLimits = { fiveHour: { usedPct: 20, resetsAt: sec + 600 }, sevenDay: { usedPct: 40, resetsAt: sec - 5 } }
  it('round-trips and drops expired windows on read', () => {
    const st = memStore()
    rememberLimits('h1', limits, 100, st.get)
    const r = lastLimits('h1', NOW, st.get)
    expect(r?.at).toBe(100)
    expect(r?.limits).toEqual({ fiveHour: { usedPct: 20, resetsAt: sec + 600 } })
    expect(lastLimits('other', NOW, st.get)).toBeNull()
    expect(lastLimits('h1', NOW + 3_600_000, st.get)).toBeNull()
  })
  it('keeps hosts apart and does not let an older reading win', () => {
    const st = memStore()
    rememberLimits('a', limits, 100, st.get)
    rememberLimits('b', { fiveHour: { usedPct: 1, resetsAt: sec + 60 } }, 50, st.get)
    rememberLimits('a', { fiveHour: { usedPct: 99, resetsAt: sec + 60 } }, 90, st.get)
    expect(lastLimits('a', NOW, st.get)?.limits.fiveHour?.usedPct).toBe(20)
    expect(lastLimits('b', NOW, st.get)?.at).toBe(50)
  })
  it('tolerates corrupt and missing storage', () => {
    for (const raw of ['{not json', '[]', '"x"', '{"h":{"at":"no"}}', '{"h":null}']) {
      const st = memStore(raw)
      expect(lastLimits('h', NOW, st.get)).toBeNull()
      rememberLimits('h', limits, 1, st.get)
      expect(lastLimits('h', NOW, st.get)?.at).toBe(1)
    }
    expect(lastLimits('h', NOW, () => null)).toBeNull()
    expect(() => rememberLimits('h', limits, 1, () => null)).not.toThrow()
    const throwing = () => { throw new Error('blocked') }
    expect(lastLimits('h', NOW, throwing)).toBeNull()
    expect(() => rememberLimits('h', limits, 1, throwing)).not.toThrow()
  })
})

describe('resolveLimits and tooltip', () => {
  const limits: UsageLimits = {
    fiveHour: { usedPct: 23.5, resetsAt: sec + 2 * 3600 + 14 * 60 + 10 },
    sevenDay: { usedPct: 41.2, resetsAt: sec + 3 * 86400 + 4 * 3600 + 10 },
    spend: { usedPct: 62.8, resetsAt: sec + 86400, usedUsd: 314.12, limitUsd: 500, period: 'monthly' }
  }
  it('prefers live, else falls back to remembered', () => {
    const st = memStore()
    expect(resolveLimits('h', [], NOW, st.get)).toBeNull()
    const live = resolveLimits('h', [sess('a', { at: sec - 120, limits })], NOW, st.get)
    expect(live?.live).toBe(true)
    rememberLimits('h', limits, sec - 120, st.get)
    const old = resolveLimits('h', [sess('a')], NOW, st.get)
    expect(old).toMatchObject({ live: false, at: sec - 120 })
  })
  it('builds the tooltip', () => {
    expect(limitsTooltip({ at: sec - 120, limits, live: true }, NOW, relativeTime)).toBe(
      [
        '5-hour limit: 23% used, resets in 2h 14m',
        'Weekly limit: 41% used, resets in 3d 4h',
        'Spend: $314.12 of $500 (monthly), resets in 1d 0h',
        'As of 2m ago'
      ].join('\n')
    )
    expect(limitsTooltip({ at: sec - 120, limits: { fiveHour: limits.fiveHour }, live: false }, NOW, relativeTime)).toMatch(
      /As of 2m ago \(Claude not running\)$/
    )
  })
  it('lists windows in order with short keys', () => {
    expect(limitLines(limits, NOW).map((l) => l.key)).toEqual(['5h', '7d', '$'])
    expect(limitLines({ sevenDay: limits.sevenDay }, NOW).map((l) => l.key)).toEqual(['7d'])
    expect(limitLines({ spend: { usedPct: 10, resetsAt: sec + 60 } }, NOW)[0].text).toBe('Spend: 10% used, resets in 1m')
  })
})

describe('host predicates', () => {
  it('canRecordUsage needs hooks installed and usage missing/outdated', () => {
    expect(canRecordUsage({ claudeHooks: 'installed', claudeUsage: 'missing' })).toBe(true)
    expect(canRecordUsage({ claudeHooks: 'installed', claudeUsage: 'outdated' })).toBe(true)
    expect(canRecordUsage({ claudeHooks: 'installed', claudeUsage: 'installed' })).toBe(false)
    expect(canRecordUsage({ claudeHooks: 'missing', claudeUsage: 'missing' })).toBe(false)
    expect(canRecordUsage({ claudeHooks: 'installed' })).toBe(false)
  })
  it('canStopUsage', () => {
    expect(canStopUsage({ claudeUsage: 'installed' })).toBe(true)
    expect(canStopUsage({ claudeUsage: 'outdated' })).toBe(true)
    expect(canStopUsage({ claudeUsage: 'missing' })).toBe(false)
    expect(canStopUsage({})).toBe(false)
  })
})

describe('pane usage text', () => {
  it('shows what is present', () => {
    expect(paneUsageText({ at: 1, contextPct: 42.5, costUsd: 0.0123 })).toBe('ctx 42% · $0.01')
    expect(paneUsageText({ at: 1, contextPct: 42 })).toBe('ctx 42%')
    expect(paneUsageText({ at: 1, costUsd: 12.34 })).toBe('$12.3')
    expect(paneUsageText({ at: 1, model: 'Opus' })).toBe('')
    expect(paneUsageText(undefined)).toBe('')
  })
  it('builds the tooltip', () => {
    expect(paneUsageTooltip({ at: 1, contextPct: 42, costUsd: 0.0123, model: 'Opus' })).toBe(
      'Context window 42% used · estimated cost $0.0123 (Opus)'
    )
    expect(paneUsageTooltip({ at: 1, contextPct: 42 })).toBe('Context window 42% used')
    expect(paneUsageTooltip({ at: 1, model: 'Opus' })).toBe('(Opus)')
  })
})
