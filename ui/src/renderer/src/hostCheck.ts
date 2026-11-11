import type { CheckItem, CheckStatus, HostReport } from './sessions'

export interface StatusView {
  icon: string
  text: string
}

const VIEWS: Record<CheckStatus, StatusView> = {
  ok: { icon: '✓', text: 'OK' },
  warn: { icon: '⚠', text: 'Warning' },
  error: { icon: '✕', text: 'Problem' }
}

/** Icon and text for an item status; text is always shown, never color alone. */
export function statusView(status: CheckStatus): StatusView {
  return VIEWS[status] ?? VIEWS.warn
}

/** "All good", "1 warning", "2 problems", or "1 problem, 2 warnings". */
export function summarize(items: Pick<CheckItem, 'status'>[]): string {
  const errors = items.filter((i) => i.status === 'error').length
  const warns = items.filter((i) => i.status === 'warn').length
  if (errors === 0 && warns === 0) return 'All good'
  const parts: string[] = []
  if (errors) parts.push(`${errors} ${errors === 1 ? 'problem' : 'problems'}`)
  if (warns) parts.push(`${warns} ${warns === 1 ? 'warning' : 'warnings'}`)
  return parts.join(', ')
}

/** Worst status across the items (for styling the summary). */
export function overallStatus(items: Pick<CheckItem, 'status'>[]): CheckStatus {
  if (items.some((i) => i.status === 'error')) return 'error'
  if (items.some((i) => i.status === 'warn')) return 'warn'
  return 'ok'
}

/** Fix text and command only apply to items that aren't ok. */
export function needsFix(item: Pick<CheckItem, 'status'>): boolean {
  return item.status !== 'ok'
}

/** True when the report has a hooks item that isn't ok (offer Install hooks…). */
export function hooksNeedInstall(report: Pick<HostReport, 'items'> | null): boolean {
  const h = report?.items.find((i) => i.id === 'hooks')
  return !!h && h.status !== 'ok'
}

/** Hosts whose status line should offer "Run setup check". */
export function canRunCheckFromStatus(state: string): boolean {
  return state === 'unavailable' || state === 'error'
}
