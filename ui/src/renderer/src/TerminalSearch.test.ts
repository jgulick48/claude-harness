import { describe, expect, it } from 'vitest'
import { isDarkColor } from './TerminalSearch'

describe('isDarkColor', () => {
  it('tells dark from light backgrounds', () => {
    expect(isDarkColor('#0d1117')).toBe(true)
    expect(isDarkColor('#000')).toBe(true)
    expect(isDarkColor('#ffffff')).toBe(false)
    expect(isDarkColor('#FDF6E3')).toBe(false)
  })
  it('treats anything unparseable as light', () => {
    expect(isDarkColor(undefined)).toBe(false)
    expect(isDarkColor('rgb(0,0,0)')).toBe(false)
    expect(isDarkColor('')).toBe(false)
  })
})
