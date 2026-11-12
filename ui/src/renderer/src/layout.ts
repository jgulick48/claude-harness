// Split layout (M6): the pure model lives in layoutTree.ts; this file keeps
// the small surface other components use.
import { layoutRefs } from './layoutTree'
import type { ActiveRef, AppState } from './store'

/**
 * Sessions currently shown in any pane, focused pane first. The sidebar uses
 * it to mark sessions that are open somewhere (not just the focused one).
 */
export function visibleRefs(state: AppState): ActiveRef[] {
  return layoutRefs(state.layout)
}
