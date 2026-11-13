import { describe, expect, it } from 'vitest'
import { externalLink } from './links'

describe('externalLink', () => {
  it('accepts http and https URLs', () => {
    expect(externalLink('https://example.com/a?b=c#d')).toBe('https://example.com/a?b=c#d')
    expect(externalLink('http://localhost:8080')).toBe('http://localhost:8080/')
    expect(externalLink('HTTPS://Example.com')).toBe('https://example.com/')
  })
  it('rejects other schemes and junk', () => {
    for (const bad of [
      'file:///etc/passwd',
      'javascript:alert(1)',
      'mailto:a@b.c',
      'vscode://x',
      'about:blank',
      'example.com',
      '',
      'https://',
      'https://' + 'a'.repeat(9000)
    ]) {
      expect(externalLink(bad)).toBeNull()
    }
    expect(externalLink(undefined)).toBeNull()
    expect(externalLink(42)).toBeNull()
  })
})
