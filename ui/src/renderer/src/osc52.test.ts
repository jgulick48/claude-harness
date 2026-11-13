import { describe, expect, it } from 'vitest'
import { OSC52_MAX_BYTES, osc52Text } from './osc52'

const b64 = (s: string): string => {
  const bytes = new TextEncoder().encode(s)
  let bin = ''
  for (let i = 0; i < bytes.length; i += 8192) bin += String.fromCharCode(...bytes.subarray(i, i + 8192))
  return btoa(bin)
}

describe('osc52Text', () => {
  it('decodes clipboard writes', () => {
    expect(osc52Text('c;' + b64('hello'))).toBe('hello')
    expect(osc52Text(';' + b64('no target'))).toBe('no target')
    expect(osc52Text('pc;' + b64('sudo apt-get install -y gcc\n'))).toBe('sudo apt-get install -y gcc\n')
    expect(osc52Text('c;' + b64('naïve — ✓ 日本'))).toBe('naïve — ✓ 日本')
    expect(osc52Text('c;')).toBe('')
  })
  it('never answers clipboard queries', () => {
    expect(osc52Text('c;?')).toBeNull()
    expect(osc52Text(';?')).toBeNull()
  })
  it('rejects malformed payloads', () => {
    for (const bad of ['', 'c', 'x;' + b64('a'), 'c;not base64!', 'c;aGVsbG8===', 'c;' + btoa('\xff\xfe')]) {
      expect(osc52Text(bad)).toBeNull()
    }
  })
  it('caps the size', () => {
    expect(osc52Text('c;' + b64('a'.repeat(OSC52_MAX_BYTES)))).toBe('a'.repeat(OSC52_MAX_BYTES))
    expect(osc52Text('c;' + b64('a'.repeat(OSC52_MAX_BYTES + 3)))).toBeNull()
  })
})
