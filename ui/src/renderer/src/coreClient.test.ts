import { describe, expect, it } from 'vitest'
import {
  connectCore,
  decodeFrame,
  encodeFrame,
  type ClientState,
  type CoreError,
  type WebSocketLike
} from './coreClient'

class FakeWS implements WebSocketLike {
  binaryType?: string
  onopen: ((ev: unknown) => void) | null = null
  onmessage: ((ev: { data: unknown }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
  onclose: ((ev: unknown) => void) | null = null
  sent: Array<string | Uint8Array> = []
  closed = false
  constructor(public url: string) {}
  send(d: string | Uint8Array): void {
    this.sent.push(d)
  }
  close(): void {
    this.closed = true
  }
  recv(o: unknown): void {
    this.onmessage?.({ data: JSON.stringify(o) })
  }
  recvBin(id: number, payload: number[]): void {
    const b = new Uint8Array(4 + payload.length)
    new DataView(b.buffer).setUint32(0, id, false)
    b.set(payload, 4)
    this.onmessage?.({ data: b.buffer })
  }
  json(i: number): any {
    return JSON.parse(this.sent[i] as string)
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

const hello = { type: 'hello', version: '0.1.0-dev', protocol: 3 }

describe('coreClient', () => {
  it('passes token in the query string and requests arraybuffers', () => {
    const { ws } = setup()
    expect(ws.url).toBe('ws://127.0.0.1:1/ws?token=tok')
    expect(ws.binaryType).toBe('arraybuffer')
  })

  it('hello -> ready, then pings', () => {
    const { states, ws, client } = setup()
    ws.recv(hello)
    expect(states.at(-1)).toEqual({ state: 'ready', version: '0.1.0-dev', protocol: 3, latencyMs: null })
    expect(ws.json(0)).toEqual({ type: 'ping', id: 1 })
    client.close()
  })

  it('protocol mismatch -> error', () => {
    const { states, ws } = setup()
    ws.recv({ type: 'hello', version: '9', protocol: 1 })
    const last = states.at(-1)
    expect(last?.state).toBe('error')
    expect(last && 'message' in last && last.message).toMatch(/mismatch/i)
    expect(ws.closed).toBe(true)
    expect(ws.sent).toHaveLength(0)
  })

  it('pong -> latency', () => {
    let t = 100
    const { states, ws, client } = setup(() => t)
    ws.recv({ ...hello })
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

  it('request resolves on result with incrementing ids', async () => {
    const { ws, client } = setup()
    ws.recv(hello) // ping uses id 1
    const p1 = client.request<string[]>('hosts.list')
    const p2 = client.request('sessions.list', { host: 'local' })
    expect(ws.json(1)).toEqual({ type: 'hosts.list', id: 2 })
    expect(ws.json(2)).toEqual({ type: 'sessions.list', host: 'local', id: 3 })
    ws.recv({ type: 'result', id: 3, result: [] })
    ws.recv({ type: 'result', id: 2, result: ['x'] })
    await expect(p1).resolves.toEqual(['x'])
    await expect(p2).resolves.toEqual([])
    client.close()
  })

  it('request rejects with code on error', async () => {
    const { ws, client } = setup()
    const p = client.request('sessions.create', { host: 'local', dir: '/nope' })
    ws.recv({ type: 'error', id: 1, message: 'directory not found', code: 'not_found' })
    const err = (await p.catch((e) => e)) as CoreError
    expect(err).toBeInstanceOf(Error)
    expect(err.message).toBe('directory not found')
    expect(err.code).toBe('not_found')
  })

  it('pending requests reject when the socket closes; later ones reject too', async () => {
    const { ws, client } = setup()
    const p = client.request('hosts.list')
    ws.onclose?.({})
    await expect(p).rejects.toThrow(/closed/)
    await expect(client.request('hosts.list')).rejects.toThrow()
  })

  it('close() rejects pending requests', async () => {
    const { client } = setup()
    const p = client.request('hosts.list')
    client.close()
    await expect(p).rejects.toThrow()
  })

  it('dispatches events by name and supports unsubscribe', () => {
    const { ws, client } = setup()
    const got: unknown[] = []
    const off = client.onEvent('sessions.changed', (e) => got.push(e.host))
    client.onEvent('other', () => got.push('other'))
    ws.recv({ type: 'event', event: 'sessions.changed', host: 'local', sessions: [] })
    off()
    ws.recv({ type: 'event', event: 'sessions.changed', host: 'again', sessions: [] })
    expect(got).toEqual(['local'])
  })

  it('delivers binary frames to the stream listener', () => {
    const { ws, client } = setup()
    const got: number[][] = []
    client.onStream(7, (d) => got.push([...d]))
    ws.recvBin(7, [1, 2, 3])
    ws.recvBin(8, [9])
    expect(got).toEqual([[1, 2, 3]])
  })

  it('buffers frames that arrive before the listener, in order', () => {
    const { ws, client } = setup()
    ws.recvBin(5, [1])
    ws.recvBin(5, [2])
    const got: number[][] = []
    client.onStream(5, (d) => got.push([...d]))
    ws.recvBin(5, [3])
    expect(got).toEqual([[1], [2], [3]])
  })

  it('sendInput prepends a big-endian stream id and UTF-8 encodes strings', () => {
    const { ws, client } = setup()
    client.sendInput(0x01020304, 'héy')
    expect([...(ws.sent[0] as Uint8Array)]).toEqual([1, 2, 3, 4, 0x68, 0xc3, 0xa9, 0x79])
    client.sendInput(1, new Uint8Array([0xff, 0x00]))
    expect([...(ws.sent[1] as Uint8Array)]).toEqual([0, 0, 0, 1, 0xff, 0])
  })
})

describe('frame encoding', () => {
  it('encodes multi-byte UTF-8', () => {
    const f = encodeFrame(2, '€😀')
    expect([...f.subarray(0, 4)]).toEqual([0, 0, 0, 2])
    expect(new TextDecoder().decode(f.subarray(4))).toBe('€😀')
    expect(f.length).toBe(4 + 3 + 4)
  })
  it('handles ids with the high bit set', () => {
    const f = encodeFrame(0xfffffffe, '')
    expect([...f]).toEqual([0xff, 0xff, 0xff, 0xfe])
    expect(decodeFrame(f)?.id).toBe(0xfffffffe)
  })
  it('round-trips and rejects short frames', () => {
    const d = decodeFrame(encodeFrame(9, 'abc'))
    expect(d?.id).toBe(9)
    expect(new TextDecoder().decode(d!.data)).toBe('abc')
    expect(decodeFrame(new Uint8Array(3))).toBeNull()
  })
})
