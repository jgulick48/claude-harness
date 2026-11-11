// Split layout (M6). CONTRACT STUB: the layout implementation replaces this
// file's bodies and adds whatever else it needs; these exports are fixed.
import type { ActiveRef, AppState } from './store'

/**
 * Sessions currently shown in any pane, focused pane first. The sidebar uses
 * it to mark sessions that are open somewhere (not just the focused one).
 */
export function visibleRefs(state: AppState): ActiveRef[] {
  return state.active ? [state.active] : []
}
