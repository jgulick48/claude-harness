import { describe, expect, it } from 'vitest'
import {
  chartTokens,
  formatTokens,
  labelledDays,
  localDays,
  modelLabel,
  niceTicks,
  OTHER,
  projectLabel,
  summarize,
  type HistoryBucket
} from './usageHistory'

const at = (y: number, m: number, d: number, h = 12): number => Math.floor(new Date(y, m - 1, d, h).getTime() / 1000)
const NOW = new Date(2026, 10, 14, 15).getTime()

function b(over: Partial<HistoryBucket> & { hour: number }): HistoryBucket {
  return { model: 'claude-opus-5-5', project: '/home/u/a', input: 0, output: 0, cacheCreate: 0, cacheRead: 0, messages: 1, ...over }
}

describe('formatTokens', () => {
  it('compacts', () => {
    expect(formatTokens(0)).toBe('0')
    expect(formatTokens(950)).toBe('950')
    expect(formatTokens(1234)).toBe('1.2k')
    expect(formatTokens(34_567)).toBe('35k')
    expect(formatTokens(999_600)).toBe('1M')
    expect(formatTokens(3_400_000)).toBe('3.4M')
    expect(formatTokens(1_250_000_000)).toBe('1.3B')
    expect(formatTokens(-1)).toBe('0')
  })
})

describe('summarize', () => {
  it('lists every local day, including empty ones', () => {
    const s = summarize([], 7, NOW)
    expect(s.days).toHaveLength(7)
    expect(s.days[6].key).toBe('2026-11-14')
    expect(s.days[0].key).toBe('2026-11-08')
    expect(s.empty).toBe(true)
    expect(s.max).toBe(0)
    expect(localDays(30, NOW)).toHaveLength(30)
  })

  it('groups by local day, model and project; the chart excludes cache reads', () => {
    const s = summarize(
      [
        b({ hour: at(2026, 11, 14, 9), input: 10, output: 5, cacheCreate: 100, cacheRead: 100_000 }),
        b({ hour: at(2026, 11, 14, 10), input: 1, output: 1, cacheCreate: 1, cacheRead: 5, project: '/home/u/b' }),
        b({ hour: at(2026, 11, 13, 12), model: 'claude-haiku-5', input: 7, project: '/home/u/a' }),
        b({ hour: at(2026, 11, 1, 12), input: 999 }) // outside 7 days
      ],
      7,
      NOW
    )
    const today = s.days[6]
    expect(today.total).toBe(10 + 5 + 100 + 3)
    expect(s.days[5].total).toBe(7)
    expect(s.max).toBe(118)
    expect(s.series).toEqual(['claude-opus-5-5', 'claude-haiku-5'])
    expect(today.segs.map((x) => x.value)).toEqual([118, 0])
    expect(s.totals).toEqual({ input: 18, output: 6, cacheCreate: 101, cacheRead: 100_005, messages: 3 })
    expect(s.projects.map((p) => [p.name, p.chart, p.messages])).toEqual([
      ['/home/u/a', 122, 2],
      ['/home/u/b', 3, 1]
    ])
    expect(s.models.map((m) => m.name)).toEqual(['claude-opus-5-5', 'claude-haiku-5'])
    expect(s.models[0].cacheRead).toBe(100_005)
    // 30 days reaches the old bucket.
    expect(summarize([b({ hour: at(2026, 11, 1, 12), input: 999 })], 30, NOW).totals.input).toBe(999)
  })

  it('folds models beyond the top four into Other', () => {
    const bs = ['a', 'b', 'c', 'd', 'e', 'f'].map((m, i) => b({ hour: at(2026, 11, 14), model: m, input: 100 - i }))
    const s = summarize(bs, 7, NOW)
    expect(s.series).toEqual(['a', 'b', 'c', 'd', OTHER])
    expect(s.days[6].segs.map((x) => x.value)).toEqual([100, 99, 98, 97, 96 + 95])
    expect(s.models).toHaveLength(6)
    expect(chartTokens(s.models[0])).toBe(100)
  })
})

describe('niceTicks', () => {
  it('picks round steps covering the max', () => {
    expect(niceTicks(0)).toEqual({ ticks: [0, 1], top: 1 })
    expect(niceTicks(118)).toEqual({ ticks: [0, 50, 100, 150], top: 150 })
    expect(niceTicks(3_000_000)).toEqual({ ticks: [0, 1_000_000, 2_000_000, 3_000_000], top: 3_000_000 })
    expect(niceTicks(7)).toEqual({ ticks: [0, 5, 10], top: 10 })
  })
})

describe('labels', () => {
  it('thins out x labels', () => {
    expect(labelledDays(7)).toEqual([0, 1, 2, 3, 4, 5, 6])
    expect(labelledDays(30)).toEqual([4, 9, 14, 19, 24, 29])
  })
  it('shortens paths and models', () => {
    expect(projectLabel('/home/u/src/app', '/home/u')).toBe('~/src/app')
    expect(projectLabel('/home/u', '/home/u/')).toBe('~')
    expect(projectLabel('/home/user2/x', '/home/u')).toBe('/home/user2/x')
    expect(projectLabel('', '/home/u')).toBe('(unknown)')
    expect(projectLabel('/srv/x', undefined)).toBe('/srv/x')
    expect(modelLabel('claude-opus-5-5')).toBe('opus-5-5')
    expect(modelLabel(OTHER)).toBe('Other')
  })
})
