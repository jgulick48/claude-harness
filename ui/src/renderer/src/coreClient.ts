// Framework-free WebSocket client for harness-core (protocol v1).
export const EXPECTED_PROTOCOL = 1

export type ClientState =
  | { state: 'connecting' }
  | { state: 'ready'; version: string; protocol: number; latencyMs: number | null }
  | { state: 'error'; message: string }

export interface WebSocketLike {
  onopen: ((ev: unknown) => void) | null
  onmessage: ((ev: { data: unknown }) => void) | null
  onerror: ((ev: unknown) => void) | null
  onclose: ((ev: unknown) => void) | null
  send(data: string): void
  close(): void
}

export interface CoreClientOptions {
  url: string
  token: string
  onState: (s: ClientState) => void
  createSocket?: (url: string) => WebSocketLike
  pingIntervalMs?: number
  now?: () => number
}

export interface CoreClient {
  close(): void
}

export function connectCore(opts: CoreClientOptions): CoreClient {
  const create = opts.createSocket ?? ((u: string) => new WebSocket(u) as unknown as WebSocketLike)
  const now = opts.now ?? (() => performance.now())
  const interval = opts.pingIntervalMs ?? 10_000
  const ws = create(`${opts.url}?token=${encodeURIComponent(opts.token)}`)

  let closed = false
  let timer: ReturnType<typeof setInterval> | null = null
  let nextId = 1
  const pending = new Map<number, number>()
  let current: ClientState = { state: 'connecting' }

  const emit = (s: ClientState): void => {
    current = s
    if (!closed) opts.onState(s)
  }
  const stopTimer = (): void => {
    if (timer) clearInterval(timer)
    timer = null
  }
  const fail = (message: string): void => {
    stopTimer()
    if (current.state !== 'error') emit({ state: 'error', message })
  }
  const ping = (): void => {
    const id = nextId++
    pending.set(id, now())
    try {
      ws.send(JSON.stringify({ type: 'ping', id }))
    } catch (e) {
      fail(`ping failed: ${(e as Error).message}`)
    }
  }

  ws.onmessage = (ev) => {
    if (typeof ev.data !== 'string') return
    let msg: { type?: string; id?: number; version?: string; protocol?: number; message?: string }
    try {
      msg = JSON.parse(ev.data)
    } catch {
      return
    }
    if (msg.type === 'hello') {
      if (msg.protocol !== EXPECTED_PROTOCOL) {
        fail(
          `Version mismatch: core speaks protocol ${String(msg.protocol)}, app expects ${EXPECTED_PROTOCOL}`
        )
        ws.close()
        return
      }
      emit({ state: 'ready', version: String(msg.version), protocol: msg.protocol, latencyMs: null })
      ping()
      timer = setInterval(ping, interval)
    } else if (msg.type === 'pong' && typeof msg.id === 'number') {
      const sent = pending.get(msg.id)
      if (sent === undefined || current.state !== 'ready') return
      pending.delete(msg.id)
      emit({ ...current, latencyMs: Math.max(0, Math.round(now() - sent)) })
    } else if (msg.type === 'error') {
      console.warn('core error:', msg.message)
    }
  }
  ws.onerror = () => fail('Connection to core failed')
  ws.onclose = () => fail('Connection to core closed')

  return {
    close() {
      closed = true
      stopTimer()
      ws.close()
    }
  }
}
