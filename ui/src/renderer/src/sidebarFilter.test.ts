import { describe, expect, it } from 'vitest'
import { fuzzyMatch, highlightParts, matchCandidate } from './sidebarFilter'

describe('fuzzyMatch', () => {
  it('matches empty query with no indices', () => {
    expect(fuzzyMatch('', 'abc')).toEqual({ score: 0, indices: [] })
  })
  it('is case-insensitive and ordered', () => {
    expect(fuzzyMatch('API', 'api-refactor')?.indices).toEqual([0, 1, 2])
    expect(fuzzyMatch('arf', 'api-refactor')?.indices).toEqual([0, 4, 6])
    expect(fuzzyMatch('fra', 'api-refactor')).toBeNull()
  })
  it('rejects missing characters and over-long queries', () => {
    expect(fuzzyMatch('xyz', 'api')).toBeNull()
    expect(fuzzyMatch('apiapi', 'api')).toBeNull()
  })
  it('ranks contiguous and word-start matches higher', () => {
    const sub = fuzzyMatch('ref', 'api-refactor')!
    const scattered = fuzzyMatch('ref', 'rxexf')!
    expect(sub.score).toBeGreaterThan(scattered.score)
    expect(fuzzyMatch('a', 'a-b')!.score).toBeGreaterThan(fuzzyMatch('a', 'zzza')!.score)
  })
})

describe('matchCandidate', () => {
  const c = { label: 'api-refactor', path: '~/work/api', host: 'devbox' }
  it('matches everything for an empty or blank query', () => {
    expect(matchCandidate('', c)).toEqual({ score: 0, labelIndices: [] })
    expect(matchCandidate('   ', c)).toEqual({ score: 0, labelIndices: [] })
  })
  it('matches label, path or host', () => {
    expect(matchCandidate('refac', c)?.labelIndices).toEqual([4, 5, 6, 7, 8])
    expect(matchCandidate('work', c)).not.toBeNull()
    expect(matchCandidate('devb', c)).not.toBeNull()
    expect(matchCandidate('zzz', c)).toBeNull()
  })
  it('requires every token to match something', () => {
    expect(matchCandidate('api devbox', c)).not.toBeNull()
    expect(matchCandidate('api nope', c)).toBeNull()
  })
})

describe('highlightParts', () => {
  it('groups runs', () => {
    expect(highlightParts('abcd', [1, 2])).toEqual([
      { text: 'a', hit: false },
      { text: 'bc', hit: true },
      { text: 'd', hit: false }
    ])
    expect(highlightParts('', [])).toEqual([])
  })
})
