import { useCallback, useEffect, useRef, useState } from 'react'
import { relativeTime } from './claudeStatus'
import { useCore } from './CoreContext'
import type { Host } from './sessions'
import { useNow } from './useNow'
import {
  formatTokens,
  labelledDays,
  modelLabel,
  niceTicks,
  projectLabel,
  shortDate,
  summarize,
  type HistoryResult,
  type HistorySummary,
  type NamedTotals
} from './usageHistory'

const RANGES = [7, 30] as const
const SERIES_VARS = ['--series-1', '--series-2', '--series-3', '--series-4']
const seriesColor = (series: string, i: number): string => (series === 'Other' ? 'var(--series-other)' : `var(${SERIES_VARS[i] ?? '--series-other'})`)

/** Token usage per day, model and project, read from the transcripts on one host. */
export function UsageHistoryView({ hosts }: { hosts: readonly Host[] }): React.JSX.Element {
  const { client } = useCore()
  const now = useNow()
  const choices = hosts.filter((h) => h.state === 'connected')
  const [hostId, setHostId] = useState<string>(choices[0]?.id ?? '')
  const [days, setDays] = useState<(typeof RANGES)[number]>(7)
  const [state, setState] = useState<{ loading: boolean; error?: string; result?: HistoryResult }>({ loading: true })
  const seq = useRef(0)
  const host = choices.find((h) => h.id === hostId) ?? choices[0]

  const load = useCallback(
    (id: string) => {
      if (!client) return
      const mine = ++seq.current
      setState((s) => ({ ...s, loading: true, error: undefined }))
      client
        .request<HistoryResult>('usage.history', { host: id })
        .then((r) => mine === seq.current && setState({ loading: false, result: { buckets: r?.buckets ?? [], scannedAt: r?.scannedAt ?? 0 } }))
        .catch((e: Error) => mine === seq.current && setState({ loading: false, error: e.message }))
    },
    [client]
  )
  useEffect(() => {
    if (host) load(host.id)
    return () => {
      seq.current++
    }
  }, [host?.id, load]) // eslint-disable-line react-hooks/exhaustive-deps

  if (!host) return <p className="help usage-empty">No connected host to read Claude transcripts from.</p>
  const result = state.result
  const summary = result ? summarize(result.buckets, days, now) : null
  return (
    <div className="usage-history" data-testid="usage-history">
      <div className="usage-history-controls">
        <label>
          Host
          <select value={host.id} onChange={(e) => setHostId(e.target.value)} aria-label="Host">
            {choices.map((h) => (
              <option key={h.id} value={h.id}>
                {h.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Range
          <select value={days} onChange={(e) => setDays(Number(e.target.value) as (typeof RANGES)[number])} aria-label="Range">
            {RANGES.map((d) => (
              <option key={d} value={d}>
                Last {d} days
              </option>
            ))}
          </select>
        </label>
        <button type="button" className="btn small" disabled={state.loading} onClick={() => load(host.id)}>
          Rescan
        </button>
      </div>
      {state.loading && <p className="help" role="status">Scanning transcripts…</p>}
      {state.error && <p className="form-error">{state.error}</p>}
      {!state.loading && !state.error && result && summary && (
        summary.empty ? (
          <p className="help usage-empty">
            No Claude transcripts found on {host.name} in the last {days} days.
          </p>
        ) : (
          <>
            <HistoryChart summary={summary} />
            <p className="usage-history-total" data-testid="usage-history-total">
              {formatTokens(summary.totals.input + summary.totals.cacheCreate + summary.totals.output)} tokens in {summary.totals.messages.toLocaleString()} messages
              {' '}(input, cache write and output)
            </p>
            <BreakdownTable title="Project" rows={summary.projects} label={(n) => projectLabel(n, host.home)} testId="usage-projects" />
            <BreakdownTable title="Model" rows={summary.models} label={modelLabel} testId="usage-models" />
          </>
        )
      )}
      <p className="help">
        Tokens only; cost isn&apos;t estimated here.
        {result && result.scannedAt > 0 && <> As of {relativeTime(result.scannedAt, now)}.</>}
      </p>
    </div>
  )
}

const W = 600
const H = 220
const M = { l: 44, r: 8, t: 10, b: 24 }

function HistoryChart({ summary }: { summary: HistorySummary }): React.JSX.Element {
  const { ticks, top } = niceTicks(summary.max)
  const pw = W - M.l - M.r
  const ph = H - M.t - M.b
  const n = summary.days.length
  const slot = pw / n
  const bar = slot * 0.7
  const y = (v: number): number => M.t + ph - (v / top) * ph
  const labelled = new Set(labelledDays(n))
  const total = summary.days.reduce((a, d) => a + d.total, 0)
  const aria = `Tokens per day for the last ${n} days, input, cache write and output (cache reads excluded): ${formatTokens(total)} in total, peak ${formatTokens(summary.max)} in a day.`
  return (
    <figure className="usage-chart" data-testid="usage-chart">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={aria}>
        {ticks.map((t) => (
          <g key={t}>
            <line x1={M.l} x2={W - M.r} y1={y(t)} y2={y(t)} className="chart-grid" />
            <text x={M.l - 6} y={y(t)} className="chart-axis" textAnchor="end" dominantBaseline="middle">
              {formatTokens(t)}
            </text>
          </g>
        ))}
        {summary.days.map((d, i) => {
          const x = M.l + i * slot + (slot - bar) / 2
          let acc = 0
          const tip =
            `${shortDate(d.start)}: ${d.total.toLocaleString()} tokens` +
            d.segs.filter((s) => s.value > 0).map((s) => `\n${modelLabel(s.series)}: ${s.value.toLocaleString()}`).join('')
          return (
            <g key={d.key} data-testid="usage-bar" data-total={d.total}>
              <title>{tip}</title>
              <rect x={M.l + i * slot} y={M.t} width={slot} height={ph} fill="transparent" />
              {d.segs.map((s, si) => {
                if (s.value <= 0) return null
                const y1 = y(acc + s.value)
                const h = y(acc) - y1
                acc += s.value
                return <rect key={s.series} x={x} y={y1} width={bar} height={Math.max(h, 1)} fill={seriesColor(s.series, si)} />
              })}
              {labelled.has(i) && (
                <text x={x + bar / 2} y={H - 8} className="chart-axis" textAnchor="middle">
                  {shortDate(d.start)}
                </text>
              )}
            </g>
          )
        })}
      </svg>
      <figcaption className="usage-legend">
        {summary.series.map((s, i) => (
          <span key={s} className="usage-legend-item">
            <span className="usage-swatch" style={{ background: seriesColor(s, i) }} />
            {modelLabel(s)}
          </span>
        ))}
        <span className="help">Input + cache write + output tokens; cache reads are not shown.</span>
      </figcaption>
    </figure>
  )
}

function BreakdownTable({
  title,
  rows,
  label,
  testId
}: {
  title: string
  rows: readonly NamedTotals[]
  label: (name: string) => string
  testId: string
}): React.JSX.Element {
  const num = (n: number): React.JSX.Element => <td className="usage-num" title={n.toLocaleString()}>{formatTokens(n)}</td>
  return (
    <table className="usage-table usage-history-table" data-testid={testId}>
      <thead>
        <tr>
          <th>{title}</th>
          <th className="usage-num">Messages</th>
          <th className="usage-num">Input</th>
          <th className="usage-num">Cache write</th>
          <th className="usage-num">Cache read</th>
          <th className="usage-num">Output</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.name}>
            <td className="usage-session" title={r.name}>{label(r.name)}</td>
            <td className="usage-num">{r.messages.toLocaleString()}</td>
            {num(r.input)}
            {num(r.cacheCreate)}
            {num(r.cacheRead)}
            {num(r.output)}
          </tr>
        ))}
      </tbody>
    </table>
  )
}
