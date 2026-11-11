import { describe, expect, it } from 'vitest'
import { connectCore, type ClientState, type WebSocketLike } from './coreClient'

class FakeWS implements WebSocketLike {
  onopen: ((ev: unknown) => void) | null = null
  onmessage: ((ev: { data: unknown }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
  onclose: ((ev: unknown) => void) | null = null
  sent: string[] = []
  closed = false
  constructor(public url: string) {}
  send(d: string): void {
    this.sent.push(d)
  }
  close(): void {
    this.closed = true
  }
  recv(o: unknown): void {
    this.onmessage?.({ data: JSON.stringify(o) })
  }
}

function setup(now = () => 0) {
  const states: ClientState[] = []
  let ws!: FakeWS
  const client = connectCore({
    url: 'ws://127.0.0.1:1/ws',
    token: 'tok',
    onState: (s) => states.push(s),
    createSocket: (u) => (ws = new FakeWS(u)),
    pingIntervalMs: 1_000_000,
    now
  })
  return { states, ws, client }
}

describe('coreClient', () => {
  it('passes token in the query string', () => {
    expect(setup().ws.url).toBe('ws://127.0.0.1:1/ws?token=tok')
  })

  it('hello -> ready, then pings', () => {
    const { states, ws, client } = setup()
    ws.recv({ type: 'hello', version: '0.1.0-dev', protocol: 1 })
    expect(states.at(-1)).toEqual({ state: 'ready', version: '0.1.0-dev', protocol: 1, latencyMs: null })
    expect(JSON.parse(ws.sent[0])).toEqual({ type: 'ping', id: 1 })
    client.close()
  })

  it('protocol mismatch -> error', () => {
    const { states, ws } = setup()
    ws.recv({ type: 'hello', version: '9', protocol: 2 })
    const last = states.at(-1)
    expect(last?.state).toBe('error')
    expect(last && 'message' in last && last.message).toMatch(/mismatch/i)
    expect(ws.closed).toBe(true)
    expect(ws.sent).toHaveLength(0)
  })

  it('pong -> latency', () => {
    let t = 100
    const { states, ws, client } = setup(() => t)
    ws.recv({ type: 'hello', version: 'v', protocol: 1 })
    t = 142
    ws.recv({ type: 'pong', id: 1 })
    expect(states.at(-1)).toMatchObject({ state: 'ready', latencyMs: 42 })
    client.close()
  })

  it('socket close -> error', () => {
    const { states, ws } = setup()
    ws.onclose?.({})
    expect(states.at(-1)?.state).toBe('error')
  })
})
