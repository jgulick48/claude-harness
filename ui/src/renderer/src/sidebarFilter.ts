// Fuzzy filter for the sidebar. Pure: no React or DOM imports.

export interface FuzzyResult {
  score: number
  /** Indices into the text of the matched characters, ascending. */
  indices: number[]
}

const isWordChar = (c: string): boolean => /[a-z0-9]/i.test(c)

/**
 * Case-insensitive ordered-subsequence match of `query` against `text`.
 * Contiguous substrings score highest; otherwise consecutive runs and
 * word-start hits score higher and gaps cost points. Null when no match.
 */
export function fuzzyMatch(query: string, text: string): FuzzyResult | null {
  const q = query.toLowerCase()
  const t = text.toLowerCase()
  if (!q) return { score: 0, indices: [] }
  if (q.length > t.length) return null

  const at = t.indexOf(q)
  if (at >= 0) {
    const boundary = at === 0 || !isWordChar(t[at - 1])
    return {
      score: 100 + q.length * 4 + (boundary ? 20 : 0) - at,
      indices: Array.from({ length: q.length }, (_, i) => at + i)
    }
  }

  const indices: number[] = []
  let score = 0
  let ti = 0
  for (const ch of q) {
    const found = t.indexOf(ch, ti)
    if (found < 0) return null
    const prev = indices[indices.length - 1]
    score += 10
    if (prev !== undefined && found === prev + 1) score += 8
    else if (prev !== undefined) score -= Math.min(found - prev - 1, 5)
    if (found === 0 || !isWordChar(t[found - 1])) score += 6
    indices.push(found)
    ti = found + 1
  }
  return { score, indices }
}

export interface Candidate {
  label: string
  path: string
  host: string
}

export interface CandidateMatch {
  score: number
  /** Matched character indices within the label, for highlighting. */
  labelIndices: number[]
}

/**
 * Matches a session against a whitespace-separated query. Every token must
 * match the label, the path or the host name (any one of them).
 */
export function matchCandidate(query: string, c: Candidate): CandidateMatch | null {
  const tokens = query.trim().split(/\s+/).filter(Boolean)
  if (tokens.length === 0) return { score: 0, labelIndices: [] }
  let score = 0
  const label = new Set<number>()
  for (const tok of tokens) {
    const l = fuzzyMatch(tok, c.label)
    const p = fuzzyMatch(tok, c.path)
    const h = fuzzyMatch(tok, c.host)
    const best = Math.max(l ? l.score + 10 : -Infinity, p ? p.score : -Infinity, h ? h.score : -Infinity)
    if (best === -Infinity) return null
    score += best
    l?.indices.forEach((i) => label.add(i))
  }
  return { score, labelIndices: [...label].sort((a, b) => a - b) }
}

/** Splits text into runs, flagging the ones at `indices`, for highlight rendering. */
export function highlightParts(text: string, indices: readonly number[]): { text: string; hit: boolean }[] {
  const hit = new Set(indices)
  const parts: { text: string; hit: boolean }[] = []
  for (let i = 0; i < text.length; i++) {
    const h = hit.has(i)
    const last = parts[parts.length - 1]
    if (last && last.hit === h) last.text += text[i]
    else parts.push({ text: text[i], hit: h })
  }
  return parts
}
