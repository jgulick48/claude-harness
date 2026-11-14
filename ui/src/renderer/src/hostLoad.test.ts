import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { relativeTime } from './claudeStatus'
import {
  createLoadPoller,
  formatBytes,
  formatLoadPct,
  formatMem,
  LOAD_HOSTS_KEY,
  loadLevel,
  loadLoadHosts,
  loadTooltip,
  parseLoadHosts,
  saveLoadHosts,
  serializeLoadHosts,
  type HostLoad
} from './hostLoad'

const L: HostLoad = { at: 1_000_000, cpuPct: 23, cores: 8, load1: 0.5, load5: 0.61, load15: 0.7, memTotal: 1, memUsed: 1 }

describe('enabled host set', () => {
  it('round-trips', () => {
    const s = new Set(['local', 'ssh:b'])
    expect(parseLoadHosts(serializeLoadHosts(s))).toEqual(s)
    expect(serializeLoadHosts(new Set(['b', 'a']))).toBe('["a","b"]')
  })
  it('tolerates bad data', () => {
    for (const bad of [null, undefined, '', 'nope', '{"a":1}', '42', 'null']) expect(parseLoadHosts(bad).size).toBe(0)
    expect([...parseLoadHosts('["a",1,null,"","b"]')]).toEqual(['a', 'b'])
  })
  it('loads and saves through storage, surviving a throwing one', () => {
    const store: Record<string, string> = {}
    const st = { getItem: (k: string) => store[k] ?? null, setItem: (k: string, v: string) => void (store[k] = v) }
    saveLoadHosts(new Set(['x']), () => st)
    expect(store[LOAD_HOSTS_KEY]).toBe('["x"]')
    expect([...loadLoadHosts(() => st)]).toEqual(['x'])
    const boom = (): never => {
      throw new Error('blocked')
    }
    expect(loadLoadHosts(boom).size).toBe(0)
    expect(() => saveLoadHosts(new Set(['x']), boom)).not.toThrow()
  })
})

describe('formatting', () => {
  it('formats bytes', () => {
    expect(formatBytes(6.1 * 1024 ** 3)).toBe('6.1 GB')
    expect(formatBytes(1024 ** 3)).toBe('1.0 GB')
    expect(formatBytes(512 * 1024 ** 2)).toBe('512 MB')
    expect(formatBytes(0)).toBe('0 MB')
  })
  it('formats memory pairs', () => {
    expect(formatMem(6.1 * 1024 ** 3, 15.6 * 1024 ** 3)).toBe('6.1 / 15.6 GB')
    expect(formatMem(512 * 1024 ** 2, 1024 ** 3)).toBe('512 MB / 1.0 GB')
  })
  it('formats percent', () => {
    expect(formatLoadPct(23.9)).toBe('23%')
    expect(formatLoadPct(-1)).toBe('0%')
    expect(formatLoadPct(150)).toBe('100%')
  })
  it('levels', () => {
    expect(loadLevel(79.9)).toBe('ok')
    expect(loadLevel(80)).toBe('warn')
    expect(loadLevel(94.9)).toBe('warn')
    expect(loadLevel(95)).toBe('high')
  })
  it('tooltip', () => {
    expect(loadTooltip(L, 1_030_000, relativeTime)).toBe(
      'Load average: 0.50 0.61 0.70 (1/5/15 min)\n8 cores\nUpdated 30s ago'
    )
    expect(loadTooltip({ ...L, cores: 1 }, 1_000_000, relativeTime)).toContain('1 core\n')
  })
})

describe('createLoadPoller', () => {
  let visible = true
  let vis: (() => void) | null = null
  beforeEach(() => {
    vi.useFakeTimers()
    visible = true
    vis = null
  })
  afterEach(() => vi.useRealTimers())

  const make = (request: () => Promise<HostLoad>) => {
    const got = { loads: 0, errors: [] as string[], unsupported: 0 }
    const stop = createLoadPoller({
      request,
      onLoad: () => void got.loads++,
      onError: (m) => void got.errors.push(m),
      onUnsupported: () => void got.unsupported++,
      isVisible: () => visible,
      onVisibility: (cb) => {
        vis = cb
        return () => {
          vis = null
        }
      },
      intervalMs: 5000
    })
    return { got, stop }
  }

  it('polls immediately and every interval, and stops', async () => {
    const request = vi.fn(() => Promise.resolve(L))
    const { got, stop } = make(request)
    expect(request).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(request).toHaveBeenCalledTimes(3)
    expect(got.loads).toBe(3)
    stop()
    await vi.advanceTimersByTimeAsync(20_000)
    expect(request).toHaveBeenCalledTimes(3)
    expect(vis).toBeNull()
  })

  it('pauses while hidden and resumes with an immediate poll', async () => {
    const request = vi.fn(() => Promise.resolve(L))
    const { stop } = make(request)
    visible = false
    vis!()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(request).toHaveBeenCalledTimes(1)
    visible = true
    vis!()
    expect(request).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(5000)
    expect(request).toHaveBeenCalledTimes(3)
    stop()
  })

  it('does not start while hidden', async () => {
    visible = false
    const request = vi.fn(() => Promise.resolve(L))
    const { stop } = make(request)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(request).not.toHaveBeenCalled()
    visible = true
    vis!()
    expect(request).toHaveBeenCalledTimes(1)
    stop()
  })

  it('stops on unsupported', async () => {
    const request = vi.fn(() => Promise.reject(Object.assign(new Error('no'), { code: 'unsupported' })))
    const { got } = make(request)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(request).toHaveBeenCalledTimes(1)
    expect(got.unsupported).toBe(1)
    expect(vis).toBeNull()
  })

  it('reports other errors and keeps polling', async () => {
    const request = vi.fn(() => Promise.reject(new Error('boom')))
    const { got, stop } = make(request)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(request).toHaveBeenCalledTimes(3)
    expect(got.errors).toEqual(['boom', 'boom', 'boom'])
    stop()
  })

  it('never overlaps requests', async () => {
    const request = vi.fn(() => new Promise<HostLoad>(() => {}))
    const { stop } = make(request)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(request).toHaveBeenCalledTimes(1)
    stop()
  })
})
