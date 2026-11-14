import { describe, expect, it } from 'vitest'
import {
  groupByHost,
  keysWithStatus,
  promptBytes,
  promptTargets,
  pruneSelection,
  selKey,
  skipReason,
  type PromptCandidate
} from './sendPrompt'
import type { Session } from './sessions'

const cand = (host: string, id: string, claudeStatus: string): PromptCandidate => ({
  host,
  hostName: host === 'local' ? 'This computer' : host,
  session: { id, name: id, harnessName: '', claudeStatus } as unknown as Session
})

const all = [
  cand('local', '$1', 'idle'),
  cand('local', '$2', 'waiting'),
  cand('local', '$3', 'working'),
  cand('local', '$4', ''),
  cand('box', '$1', 'idle'),
  cand('box', '$2', 'unknown')
]
const sel = (...k: string[]): Set<string> => new Set(k)

describe('promptTargets', () => {
  const everything = sel(...all.map((c) => selKey(c.host, c.session.id)))

  it('sends to idle only by default and says why the rest are skipped', () => {
    const t = promptTargets(all, everything, false)
    expect(t.send.map((c) => selKey(c.host, c.session.id))).toEqual(['local/$1', 'box/$1'])
    expect(t.skip.map((s) => [selKey(s.target.host, s.target.session.id), s.reason])).toEqual([
      ['local/$2', 'waiting for input, not included'],
      ['local/$3', 'Claude is working'],
      ['local/$4', "Claude isn't running"],
      ['box/$2', "Claude's state is unknown"]
    ])
  })

  it('includes waiting sessions when allowed, but never working or plain shells', () => {
    const t = promptTargets(all, everything, true)
    expect(t.send.map((c) => selKey(c.host, c.session.id))).toEqual(['local/$1', 'local/$2', 'box/$1'])
    expect(t.skip).toHaveLength(3)
  })

  it('only considers selected, existing sessions', () => {
    const t = promptTargets(all, sel('local/$2', 'box/$1', 'gone/$9'), false)
    expect(t.send.map((c) => c.session.id)).toEqual(['$1'])
    expect(t.skip.map((s) => s.target.session.id)).toEqual(['$2'])
    expect(promptTargets(all, sel(), true)).toEqual({ send: [], skip: [] })
  })
})

describe('skipReason', () => {
  it('matches the core rules', () => {
    expect(skipReason('idle', false)).toBeNull()
    expect(skipReason('waiting', true)).toBeNull()
    expect(skipReason('waiting', false)).not.toBeNull()
    for (const s of ['working', 'unknown', '', 'x']) expect(skipReason(s, true)).not.toBeNull()
  })
})

describe('selection helpers', () => {
  it('picks sessions by status', () => {
    expect([...keysWithStatus(all, 'idle')]).toEqual(['local/$1', 'box/$1'])
    expect([...keysWithStatus(all, 'waiting')]).toEqual(['local/$2'])
  })
  it('drops sessions that disappeared and keeps identity when nothing changed', () => {
    const s = sel('local/$1', 'local/$9')
    expect([...pruneSelection(s, all)]).toEqual(['local/$1'])
    const keep = sel('local/$1')
    expect(pruneSelection(keep, all)).toBe(keep)
  })
  it('groups by host in first-seen order', () => {
    const g = groupByHost(all)
    expect(g.map((x) => [x.host, x.items.length])).toEqual([['local', 4], ['box', 2]])
  })
  it('counts UTF-8 bytes', () => {
    expect(promptBytes('aé日')).toBe(6)
  })
})
