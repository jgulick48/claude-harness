// Pure helpers for the usage history view (protocol §5.4, usage.history):
// grouping hourly buckets into local calendar days, totals and axis ticks.
// No React or DOM imports.

export interface HistoryBucket {
  /** Unix seconds, start of the UTC hour. */
  hour: number
  model: string
  project: string
  input: number
  output: number
  cacheCreate: number
  cacheRead: number
  messages: number
}

export interface HistoryResult {
  buckets: HistoryBucket[]
  /** Unix seconds; 0 when the host can't be scanned. */
  scannedAt: number
}

export interface Totals {
  input: number
  output: number
  cacheCreate: number
  cacheRead: number
  messages: number
}

export const OTHER = 'Other'
/** Models drawn as their own series before the rest are folded into "Other". */
export const TOP_MODELS = 4

const zero = (): Totals => ({ input: 0, output: 0, cacheCreate: 0, cacheRead: 0, messages: 0 })

function add(t: Totals, b: HistoryBucket): void {
  t.input += b.input
  t.output += b.output
  t.cacheCreate += b.cacheCreate
  t.messages += b.messages
  t.cacheRead += b.cacheRead
}

/** What the chart and the sort order count: cache reads dwarf everything else, so they are left out. */
export function chartTokens(t: Pick<Totals, 'input' | 'output' | 'cacheCreate'>): number {
  return t.input + t.cacheCreate + t.output
}

/** "950", "1.2k", "34k", "3.4M", "1.2B". */
export function formatTokens(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '0'
  const fmt = (v: number, unit: string): string => `${v < 10 ? (Math.round(v * 10) / 10).toString() : Math.round(v).toString()}${unit}`
  if (n < 1000) return String(Math.round(n))
  if (n < 999_500) return fmt(n / 1e3, 'k')
  if (n < 999_500_000) return fmt(n / 1e6, 'M')
  return fmt(n / 1e9, 'B')
}

export interface DaySeg {
  series: string
  value: number
}

export interface DayRow {
  /** Local midnight, unix ms. */
  start: number
  /** "2026-11-14" (local). */
  key: string
  total: number
  segs: DaySeg[]
}

export interface NamedTotals extends Totals {
  name: string
  chart: number
}

export interface HistorySummary {
  days: DayRow[]
  /** Series in stacking order: the top models, then "Other" when some were folded. */
  series: string[]
  models: NamedTotals[]
  projects: NamedTotals[]
  totals: Totals
  max: number
  empty: boolean
}

const dayKey = (d: Date): string =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

/** The local calendar days ending with the one containing nowMs, oldest first. */
export function localDays(days: number, nowMs: number): Date[] {
  const now = new Date(nowMs)
  const out: Date[] = []
  for (let i = days - 1; i >= 0; i--) out.push(new Date(now.getFullYear(), now.getMonth(), now.getDate() - i))
  return out
}

/** Folds the buckets of the last `days` local days into per-day, per-model and per-project totals. */
export function summarize(buckets: readonly HistoryBucket[], days: number, nowMs: number): HistorySummary {
  const dates = localDays(days, nowMs)
  const first = dates[0]?.getTime() ?? 0
  const index = new Map(dates.map((d, i) => [dayKey(d), i]))
  const inRange: Array<{ b: HistoryBucket; day: number }> = []
  for (const b of buckets) {
    const day = index.get(dayKey(new Date(b.hour * 1000)))
    if (day !== undefined && b.hour * 1000 >= first - 86_400_000) inRange.push({ b, day })
  }

  const byModel = new Map<string, NamedTotals>()
  const byProject = new Map<string, NamedTotals>()
  const totals = zero()
  const named = (m: Map<string, NamedTotals>, name: string): NamedTotals => {
    let t = m.get(name)
    if (!t) m.set(name, (t = { name, chart: 0, ...zero() }))
    return t
  }
  for (const { b } of inRange) {
    add(named(byModel, b.model), b)
    add(named(byProject, b.project), b)
    add(totals, b)
  }
  const rank = (a: NamedTotals, b: NamedTotals): number => chartTokens(b) - chartTokens(a) || a.name.localeCompare(b.name)
  const finish = (m: Map<string, NamedTotals>): NamedTotals[] => {
    const l = [...m.values()]
    for (const t of l) t.chart = chartTokens(t)
    return l.sort(rank)
  }
  const models = finish(byModel)
  const projects = finish(byProject)

  const top = models.slice(0, TOP_MODELS).map((m) => m.name)
  const series = models.length > TOP_MODELS ? [...top, OTHER] : top
  const seriesOf = (model: string): string => (top.includes(model) ? model : OTHER)
  const rows: DayRow[] = dates.map((d) => ({
    start: d.getTime(),
    key: dayKey(d),
    total: 0,
    segs: series.map((s) => ({ series: s, value: 0 }))
  }))
  for (const { b, day } of inRange) {
    const v = chartTokens(b)
    rows[day].total += v
    rows[day].segs[series.indexOf(seriesOf(b.model))].value += v
  }
  return {
    days: rows,
    series,
    models,
    projects,
    totals,
    max: Math.max(0, ...rows.map((r) => r.total)),
    empty: inRange.length === 0
  }
}

/** Gridline values: 0 and up to `count` nice steps covering max; `top` is the last one. */
export function niceTicks(max: number, count = 3): { ticks: number[]; top: number } {
  if (!(max > 0)) return { ticks: [0, 1], top: 1 }
  const raw = max / count
  const pow = 10 ** Math.floor(Math.log10(raw))
  const step = ([1, 2, 5, 10].find((m) => m * pow >= raw) ?? 10) * pow
  const n = Math.ceil(max / step - 1e-9)
  const ticks = Array.from({ length: n + 1 }, (_, i) => i * step)
  return { ticks, top: ticks[ticks.length - 1] }
}

/** Which day columns get an x label: all for a week, every 5th (counting back from today) for longer ranges. */
export function labelledDays(n: number): number[] {
  const out: number[] = []
  const every = n <= 10 ? 1 : 5
  for (let i = 0; i < n; i++) if ((n - 1 - i) % every === 0) out.push(i)
  return out
}

/** "~/src/app" for paths under the host's home; "(unknown)" for an empty project. */
export function projectLabel(project: string, home: string | undefined): string {
  if (!project) return '(unknown)'
  if (home) {
    const h = home.replace(/\/+$/, '')
    if (h && project === h) return '~'
    if (h && project.startsWith(h + '/')) return '~' + project.slice(h.length)
  }
  return project
}

/** "claude-opus-5-5" reads fine as is; only the shared prefix goes. */
export function modelLabel(model: string): string {
  return model === OTHER ? OTHER : model.replace(/^claude-/, '')
}

/** "Nov 14" in the user's locale. */
export function shortDate(startMs: number): string {
  return new Date(startMs).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}
