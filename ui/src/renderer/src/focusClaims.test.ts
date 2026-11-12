import { describe, expect, it } from 'vitest'
import { claimFocus, focusGeneration } from './focusClaims'

describe('focusClaims', () => {
  it('claimFocus bumps the generation, so a pending attach knows not to steal focus', () => {
    const start = focusGeneration()
    expect(focusGeneration()).toBe(start)
    claimFocus()
    expect(focusGeneration()).not.toBe(start)
  })
})
