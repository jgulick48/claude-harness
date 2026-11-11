// Framework-free WebSocket client for harness-core (protocol v2).
export const EXPECTED_PROTOCOL = 2

export type ClientState =
  | { state: 'connecting' }
  | { state: 'ready'; version: string; protocol: number; latencyMs: number | null }
  | { state: 'error'; message: string }

export interface WebSocketLike {
  binaryType?: string
  onopen: ((ev: unknown) => void) | null
  onmessage: ((ev: { data: unknown }) => void) | null
  onerror: ((ev: unknown) => void) | null
  onclose: ((ev: unknown) => void) | null
  send(data: string | Uint8Array): void
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

/** Error returned by the core for a failed request. */
export class CoreError extends Error {
  code: string
  constructor(message: string, code: string) {
    super(message)
    this.name = 'CoreError'
    this.code = code
  }
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type CoreEvent = { type: 'event'; event: string; [k: string]: any }

export interface CoreClient {
  close(): void
  request<T = unknown>(type: string, params?: Record<string, unknown>): Promise<T>
  /** Subscribe to a named event. Returns an unsubscribe function. */
  onEvent(name: string, cb: (ev: CoreEvent) => void): () => void
  /**
   * Subscribe to terminal output for a stream id. Frames that arrived before
   * the listener existed are delivered first, in order. Returns unsubscribe.
   */
  onStream(id: number, cb: (data: Uint8Array) => void): () => void
  sendInput(id: number, data: string | Uint8Array): void
}

const textEncoder = new TextEncoder()

/** Build a binary frame: 4-byte big-endian stream id + payload. Strings are UTF-8 encoded. */
export function encodeFrame(id: number, data: string | Uint8Array): Uint8Array {
  const payload = typeof data === 'string' ? textEncoder.encode(data) : data
  const out = new Uint8Array(4 + payload.length)
  new DataView(out.buffer).setUint32(0, id >>> 0, false)
  out.set(payload, 4)
  return out
}

/** Split a binary frame into stream id and payload; null if too short. */
export function decodeFrame(buf: ArrayBuffer | ArrayBufferView): { id: number; data: Uint8Array } | null {
  const bytes =
    buf instanceof ArrayBuffer
      ? new Uint8Array(buf)
      : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength)
  if (bytes.length < 4) return null
  const id = new DataView(bytes.buffer, bytes.byteOffset, 4).getUint32(0, false)
  return { id, data: bytes.subarray(4) }
}

const MAX_BUFFERED_STREAMS = 16
const MAX_BUFFERED_FRAMES = 512

export function connectCore(opts: CoreClientOptions): CoreClient {
  const create = opts.createSocket ?? ((u: string) => new WebSocket(u) as unknown as WebSocketLike)
  const now = opts.now ?? (() => performance.now())
  const interval = opts.pingIntervalMs ?? 10_000
  const ws = create(`${opts.url}?token=${encodeURIComponent(opts.token)}`)
  ws.binaryType = 'arraybuffer'

  let closed = false
  let dead = false
  let timer: ReturnType<typeof setInterval> | null = null
  let nextId = 1
  const pings = new Map<number, number>()
  const requests = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>()
  const eventCbs = new Map<string, Set<(ev: CoreEvent) => void>>()
  const streamCbs = new Map<number, (d: Uint8Array) => void>()
  const streamBuf = new Map<number, Uint8Array[]>()
  let current: ClientState = { state: 'connecting' }

  const emit = (s: ClientState): void => {
    current = s
    if (!closed) opts.onState(s)
  }
  const stopTimer = (): void => {
    if (timer) clearInterval(timer)
    timer = null
  }
  const rejectAll = (message: string): void => {
    for (const r of requests.values()) r.reject(new CoreError(message, 'closed'))
    requests.clear()
  }
  const fail = (message: string): void => {
    stopTimer()
    dead = true
    rejectAll(message)
    if (current.state !== 'error') emit({ state: 'error', message })
  }
  const ping = (): void => {
    const id = nextId++
    pings.set(id, now())
    try {
      ws.send(JSON.stringify({ type: 'ping', id }))
    } catch (e) {
      fail(`ping failed: ${(e as Error).message}`)
    }
  }

  ws.onmessage = (ev) => {
    if (typeof ev.data !== 'string') {
      if (ev.data instanceof ArrayBuffer || ArrayBuffer.isView(ev.data)) {
        const f = decodeFrame(ev.data as ArrayBuffer | ArrayBufferView)
        if (!f) return
        const cb = streamCbs.get(f.id)
        if (cb) {
          cb(f.data)
        } else {
          // Output can race ahead of the attach result handler; hold it.
          let q = streamBuf.get(f.id)
          if (!q) {
            if (streamBuf.size >= MAX_BUFFERED_STREAMS) {
              const oldest = streamBuf.keys().next().value
              if (oldest !== undefined) streamBuf.delete(oldest)
            }
            q = []
            streamBuf.set(f.id, q)
          }
          if (q.length < MAX_BUFFERED_FRAMES) q.push(f.data)
        }
      }
      return
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let msg: any
    try {
      msg = JSON.parse(ev.data)
    } catch {
      return
    }
    if (typeof msg !== 'object' || msg === null) return
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
      const sent = pings.get(msg.id)
      if (sent === undefined || current.state !== 'ready') return
      pings.delete(msg.id)
      emit({ ...current, latencyMs: Math.max(0, Math.round(now() - sent)) })
    } else if (msg.type === 'result' && typeof msg.id === 'number') {
      const r = requests.get(msg.id)
      if (r) {
        requests.delete(msg.id)
        r.resolve(msg.result)
      }
    } else if (msg.type === 'error') {
      const r = typeof msg.id === 'number' ? requests.get(msg.id) : undefined
      if (r) {
        requests.delete(msg.id)
        r.reject(new CoreError(String(msg.message ?? 'error'), String(msg.code ?? 'internal')))
      } else {
        console.warn('core error:', msg.message)
      }
    } else if (msg.type === 'event' && typeof msg.event === 'string') {
      eventCbs.get(msg.event)?.forEach((cb) => cb(msg as CoreEvent))
    }
  }
  ws.onerror = () => fail('Connection to core failed')
  ws.onclose = () => fail('Connection to core closed')

  return {
    close() {
      closed = true
      dead = true
      stopTimer()
      rejectAll('connection closed')
      ws.close()
    },
    request<T>(type: string, params: Record<string, unknown> = {}): Promise<T> {
      if (dead) return Promise.reject(new CoreError('not connected', 'closed'))
      const id = nextId++
      return new Promise<T>((resolve, reject) => {
        requests.set(id, { resolve: resolve as (v: unknown) => void, reject })
        try {
          ws.send(JSON.stringify({ ...params, type, id }))
        } catch (e) {
          requests.delete(id)
          reject(new CoreError((e as Error).message, 'closed'))
        }
      })
    },
    onEvent(name, cb) {
      let set = eventCbs.get(name)
      if (!set) eventCbs.set(name, (set = new Set()))
      set.add(cb)
      return () => {
        set.delete(cb)
      }
    },
    onStream(id, cb) {
      streamCbs.set(id, cb)
      const q = streamBuf.get(id)
      if (q) {
        streamBuf.delete(id)
        for (const d of q) cb(d)
      }
      return () => {
        if (streamCbs.get(id) === cb) streamCbs.delete(id)
        streamBuf.delete(id)
      }
    },
    sendInput(id, data) {
      if (dead) return
      try {
        ws.send(encodeFrame(id, data))
      } catch {
        /* socket closing; state handlers report it */
      }
    }
  }
}
