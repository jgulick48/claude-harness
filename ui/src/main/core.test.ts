import { describe, expect, it } from 'vitest'
import { generateToken, parseReadyLine, resolveCorePath } from './core'

const base = {
  env: {},
  isPackaged: false,
  resourcesPath: '/res',
  appPath: '/repo/ui',
  platform: 'linux'
}

describe('resolveCorePath', () => {
  it('prefers HARNESS_CORE_PATH', () => {
    expect(resolveCorePath({ ...base, env: { HARNESS_CORE_PATH: '/x/core' }, isPackaged: true })).toBe('/x/core')
  })
  it('uses resources/bin when packaged', () => {
    expect(resolveCorePath({ ...base, isPackaged: true })).toBe('/res/bin/harness-core')
  })
  it('uses repo-root bin in dev', () => {
    expect(resolveCorePath(base)).toBe('/repo/bin/harness-core')
  })
  it('adds .exe on win32', () => {
    expect(resolveCorePath({ ...base, platform: 'win32', isPackaged: true })).toMatch(/harness-core\.exe$/)
    expect(resolveCorePath({ ...base, platform: 'win32' })).toMatch(/harness-core\.exe$/)
  })
})

describe('generateToken', () => {
  it('is 64 hex chars and unique', () => {
    const a = generateToken()
    expect(a).toMatch(/^[0-9a-f]{64}$/)
    expect(generateToken()).not.toBe(a)
  })
})

describe('parseReadyLine', () => {
  const good = { type: 'ready', addr: '127.0.0.1:54321', port: 54321, version: '0.1.0-dev', protocol: 1 }
  it('parses a valid line', () => {
    expect(parseReadyLine(JSON.stringify(good))).toEqual({
      addr: '127.0.0.1:54321',
      port: 54321,
      version: '0.1.0-dev',
      protocol: 1
    })
  })
  it('rejects bad JSON', () => {
    expect(() => parseReadyLine('not json')).toThrow(/valid JSON/)
  })
  it('rejects wrong type', () => {
    expect(() => parseReadyLine(JSON.stringify({ ...good, type: 'hello' }))).toThrow(/type/)
  })
  it('rejects non-objects', () => {
    expect(() => parseReadyLine('42')).toThrow()
  })
  it.each(['addr', 'port', 'version', 'protocol'])('rejects missing %s', (k) => {
    const o: Record<string, unknown> = { ...good }
    delete o[k]
    expect(() => parseReadyLine(JSON.stringify(o))).toThrow(new RegExp(k))
  })
})
