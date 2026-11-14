import { useEffect, useRef, useState } from 'react'
import { relativeTime } from './claudeStatus'
import type { CoreClient } from './coreClient'
import {
  createLoadPoller,
  formatLoadPct,
  formatMem,
  loadLevel,
  loadTooltip,
  memPct,
  type HostLoad
} from './hostLoad'
import { useNow } from './useNow'
import { barPct } from './usage'

/**
 * CPU and memory bars for one host. Polls hosts.load while mounted (the
 * parent mounts it only for connected hosts with the toggle on).
 */
export function HostLoadIndicator({
  hostId,
  hostName,
  client,
  onUnsupported
}: {
  hostId: string
  hostName: string
  client: CoreClient | null
  onUnsupported: () => void
}): React.JSX.Element | null {
  const [load, setLoad] = useState<HostLoad | null>(null)
  const [error, setError] = useState<string | null>(null)
  const now = useNow()
  const unsupportedRef = useRef(onUnsupported)
  unsupportedRef.current = onUnsupported

  useEffect(() => {
    if (!client) return
    return createLoadPoller({
      request: () => client.request<HostLoad>('hosts.load', { host: hostId }),
      onLoad: (l) => {
        setLoad(l)
        setError(null)
      },
      onError: setError,
      onUnsupported: () => unsupportedRef.current(),
      isVisible: () => document.visibilityState === 'visible',
      onVisibility: (cb) => {
        document.addEventListener('visibilitychange', cb)
        return () => document.removeEventListener('visibilitychange', cb)
      }
    })
  }, [client, hostId])

  if (!load) {
    return error ? (
      <div className="load-ind muted" data-testid="host-load" title={error} role="status">
        Load unavailable
      </div>
    ) : null
  }
  const cpu = load.cpuPct
  const mem = memPct(load)
  const stale = error !== null
  return (
    <div
      className={`load-ind${stale ? ' stale' : ''}`}
      data-testid="host-load"
      title={stale ? `${error}\n${loadTooltip(load, now, relativeTime)}` : loadTooltip(load, now, relativeTime)}
      aria-label={`${hostName} load`}
    >
      <div className="load-row">
        <span className="load-key">CPU</span>
        <span className="usage-bar" aria-hidden="true">
          <span
            className={`usage-fill ${cpu === null ? 'ok' : loadLevel(cpu)}`}
            style={{ width: `${cpu === null ? 0 : barPct(cpu)}%` }}
          />
        </span>
        <span className="load-text">{cpu === null ? '—' : formatLoadPct(cpu)}</span>
      </div>
      <div className="load-row">
        <span className="load-key">Mem</span>
        <span className="usage-bar" aria-hidden="true">
          <span className={`usage-fill ${loadLevel(mem)}`} style={{ width: `${barPct(mem)}%` }} />
        </span>
        <span className="load-text">{formatMem(load.memUsed, load.memTotal)}</span>
      </div>
    </div>
  )
}
