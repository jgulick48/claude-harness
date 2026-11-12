// Who gets focus when an attach completes?
//
// A terminal focuses itself once its attach finishes, but the attach is
// asynchronous: by then the user may have moved on (Ctrl/Cmd+K to the filter,
// opened a dialog, ...). The terminal must not steal focus back. Every focus
// change bumps a generation counter, and so does an explicit focus action that
// targets an element that may already be focused (Ctrl/Cmd+K while the filter
// is focused fires no focusin). An attach focuses its terminal only if the
// generation is unchanged since the attach started.

let generation = 0

/** Current focus generation; compare before and after an async step. */
export function focusGeneration(): number {
  return generation
}

/** Call when user input deliberately moves focus somewhere (even if it's already there). */
export function claimFocus(): void {
  generation++
}

if (typeof window !== 'undefined') {
  window.addEventListener('focusin', () => generation++, true)
}
