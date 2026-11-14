import { describe, expect, it } from 'vitest'
import { bestMatch, fuzzyMatch, highlightParts, matchCandidate } from './sidebarFilter'

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

describe('matchCandidate colour', () => {
  const c = { label: 'api', path: '~/work/api', host: 'devbox', color: 'green' }
  it('matches the colour name by prefix', () => {
    expect(matchCandidate('green', c)).not.toBeNull()
    expect(matchCandidate('gre', c)).not.toBeNull()
    expect(matchCandidate('red', c)).toBeNull()
  })
  it('does not fuzzy-match colour letters or match untagged sessions', () => {
    expect(matchCandidate('gn', c)).toBeNull()
    expect(matchCandidate('green', { ...c, color: undefined })).toBeNull()
  })
  it('combines with other tokens and leaves label highlights alone', () => {
    expect(matchCandidate('green api', c)?.labelIndices).toEqual([0, 1, 2])
    expect(matchCandidate('green nope', c)).toBeNull()
    expect(matchCandidate('green', c)?.labelIndices).toEqual([])
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

describe('bestMatch', () => {
  it('picks the highest score, the first on a tie', () => {
    expect(bestMatch([])).toBeUndefined()
    expect(bestMatch([{ ref: 'a', score: 1 }, { ref: 'b', score: 3 }, { ref: 'c', score: 3 }])).toBe('b')
  })

  // Seen on macOS CI: "gamma" fuzzy-matched alpha-work's long temp path, and
  // Enter attached alpha-work because it came first.
  it('prefers a name match over a path that happens to fuzzy-match', () => {
    const home = '/private/var/folders/h5/t85nd63d71x27_8gl5gw3l_w0000gn/T/harness-e2e-jx859M/home'
    const sessions = [
      { ref: 'alpha', label: 'alpha-work', path: home + '/proj/alpha' },
      { ref: 'beta', label: 'beta-build', path: home + '/proj/beta' },
      { ref: 'gamma', label: 'gamma-docs', path: home + '/src/gamma' }
    ]
    const scored = sessions.flatMap((s) => {
      const m = matchCandidate('gamma', { label: s.label, path: s.path, host: 'This computer' })
      return m ? [{ ref: s.ref, score: m.score }] : []
    })
    expect(scored[0]?.ref).toBe('alpha') // the path really does match
    expect(bestMatch(scored)).toBe('gamma')
  })
})
