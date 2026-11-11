import { execFileSync, spawnSync } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { afterAll, describe, expect, it } from 'vitest'
import { startCore } from './core'

const exe = process.platform === 'win32' ? 'harness-core.exe' : 'harness-core'
const corePath = process.env.HARNESS_CORE_PATH || resolve(__dirname, '../../../bin', exe)
const exists = existsSync(corePath)
const hasTmux = spawnSync('tmux', ['-V']).status === 0

function openWS(url: string): Promise<{ ws: WebSocket; next: () => Promise<any> }> {
  return new Promise((res, rej) => {
    const ws = new WebSocket(url)
    const queue: any[] = []
    const waiters: Array<(m: any) => void> = []
    ws.onmessage = (ev) => {
      const m = JSON.parse(String(ev.data))
      const w = waiters.shift()
      if (w) w(m)
      else queue.push(m)
    }
    ws.onopen = () =>
      res({
        ws,
        next: () =>
          new Promise((r, j) => {
            const t = setTimeout(() => j(new Error('timeout waiting for message')), 5000)
            const done = (m: any): void => {
              clearTimeout(t)
              r(m)
            }
            const q = queue.shift()
            if (q) done(q)
            else waiters.push(done)
          })
      })
    ws.onerror = () => rej(new Error('websocket error'))
  })
}

// Every test core gets a private tmux socket and no tmux config, so tests never
// touch the user's real tmux server (connecting subscribes to session lists).
const isolatedSocket = `harness-uitest-basic-${process.pid}`
// The hosts file lives in a fresh temp dir so tests never read the real one.
const hostsDir = mkdtempSync(join(tmpdir(), 'harness-uitest-hosts-'))
const hostsFile = join(hostsDir, 'hosts.json')
// Same for the state file (recent directories), so the real one is never touched.
const stateFile = join(hostsDir, 'state.json')
afterAll(() => rmSync(hostsDir, { recursive: true, force: true }))
const isolatedEnv = {
  HARNESS_TMUX_SOCKET: isolatedSocket,
  HARNESS_TMUX_CONFIG: '/dev/null',
  HARNESS_HOSTS_FILE: hostsFile,
  HARNESS_STATE_FILE: stateFile
}

describe.skipIf(!exists)('harness-core integration', () => {
  it('starts, speaks protocol v4, and stops cleanly', async () => {
    const logs: string[] = []
    const core = await startCore(corePath, { logger: (l) => logs.push(l), env: isolatedEnv })
    let exitCode: number | null | undefined
    const exited = new Promise<void>((r) =>
      core.onExit((e) => {
        exitCode = e.code
        r()
      })
    )
    try {
      expect(core.info.protocol).toBe(4)
      expect(core.info.version).toBeTruthy()
      expect(core.info.url).toBe(`ws://127.0.0.1:${core.info.port}/ws`)

      const { ws, next } = await openWS(`${core.info.url}?token=${core.info.token}`)
      const hello = await next()
      expect(hello).toMatchObject({ type: 'hello', protocol: 4, version: core.info.version })
      ws.send(JSON.stringify({ type: 'ping', id: 7 }))
      // The pong and the initial events (hosts.changed, sessions.changed) are
      // sent from different goroutines, so they can arrive in either order.
      const events: any[] = []
      let pong: any
      while (!pong || !events.some((e) => e.event === 'hosts.changed')) {
        const m = await next()
        if (m.type === 'event') events.push(m)
        else pong = m
      }
      expect(pong).toEqual({ type: 'pong', id: 7 })
      const hc = events.find((e) => e.event === 'hosts.changed')
      expect(hc?.hosts?.[0]).toMatchObject({ id: 'local', kind: 'local' })
      ws.close()

      // wrong token is rejected before upgrade
      const bad = await new Promise<string>((res) => {
        const w = new WebSocket(`${core.info.url}?token=wrong`)
        w.onmessage = () => res('message')
        w.onerror = () => res('error')
        w.onclose = () => res('close')
        w.onopen = () => res('open')
      })
      expect(['error', 'close']).toContain(bad)
    } finally {
      await core.stop()
    }
    await exited
    expect(exitCode).toBe(0)
  })
})

// --- v2: tmux sessions and terminals ---------------------------------------

const socket = `harness-uitest-${Math.random().toString(36).slice(2, 10)}`

interface Conn {
  ws: WebSocket
  request(type: string, params?: Record<string, unknown>): Promise<any>
  waitEvent(pred: (e: any) => boolean, ms?: number): Promise<any>
  output(id: number): Promise<string>
  waitOutput(id: number, re: RegExp, ms?: number): Promise<string>
  send(id: number, data: string): void
}

function connect(url: string): Promise<Conn> {
  return new Promise((res, rej) => {
    const ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'
    let nextId = 1
    const pending = new Map<number, { ok: (v: any) => void; fail: (e: Error) => void }>()
    const events: any[] = []
    const eventWaiters: Array<{ pred: (e: any) => boolean; ok: (e: any) => void }> = []
    const out = new Map<number, string>()
    const decoders = new Map<number, TextDecoder>()
    const outWaiters: Array<() => void> = []
    ws.onmessage = (ev) => {
      if (typeof ev.data !== 'string') {
        const buf = new Uint8Array(ev.data as ArrayBuffer)
        const id = new DataView(buf.buffer).getUint32(0, false)
        let d = decoders.get(id)
        if (!d) decoders.set(id, (d = new TextDecoder()))
        out.set(id, (out.get(id) ?? '') + d.decode(buf.subarray(4), { stream: true }))
        outWaiters.slice().forEach((w) => w())
        return
      }
      const m = JSON.parse(ev.data)
      if (m.type === 'result' || m.type === 'error') {
        const p = pending.get(m.id)
        pending.delete(m.id)
        if (m.type === 'result') p?.ok(m.result)
        else p?.fail(Object.assign(new Error(m.message), { code: m.code }))
      } else if (m.type === 'event') {
        const i = eventWaiters.findIndex((w) => w.pred(m))
        if (i >= 0) eventWaiters.splice(i, 1)[0].ok(m)
        else events.push(m)
      }
    }
    ws.onerror = () => rej(new Error('websocket error'))
    ws.onopen = () =>
      res({
        ws,
        request: (type, params = {}) =>
          new Promise((ok, fail) => {
            const id = nextId++
            const t = setTimeout(() => fail(new Error(`timeout: ${type}`)), 8000)
            pending.set(id, {
              ok: (v) => (clearTimeout(t), ok(v)),
              fail: (e) => (clearTimeout(t), fail(e))
            })
            ws.send(JSON.stringify({ ...params, type, id }))
          }),
        waitEvent: (pred, ms = 8000) =>
          new Promise((ok, fail) => {
            const i = events.findIndex(pred)
            if (i >= 0) return ok(events.splice(i, 1)[0])
            const t = setTimeout(() => fail(new Error('timeout waiting for event')), ms)
            eventWaiters.push({ pred, ok: (e) => (clearTimeout(t), ok(e)) })
          }),
        output: async (id) => out.get(id) ?? '',
        waitOutput: (id, re, ms = 8000) =>
          new Promise((ok, fail) => {
            const check = (): boolean => {
              if (re.test(out.get(id) ?? '')) {
                clearTimeout(t)
                const k = outWaiters.indexOf(check)
                if (k >= 0) outWaiters.splice(k, 1)
                ok(out.get(id) ?? '')
                return true
              }
              return false
            }
            const t = setTimeout(
              () => fail(new Error(`timeout waiting for ${re}; got ${JSON.stringify(out.get(id))}`)),
              ms
            )
            if (!check()) outWaiters.push(check)
          }),
        send: (id, data) => {
          const payload = new TextEncoder().encode(data)
          const b = new Uint8Array(4 + payload.length)
          new DataView(b.buffer).setUint32(0, id, false)
          b.set(payload, 4)
          ws.send(b)
        }
      })
  })
}

describe.skipIf(!exists || !hasTmux)('harness-core tmux scenario', () => {
  afterAll(() => {
    // Only ever our private socket; never the default tmux server.
    try {
      execFileSync('tmux', ['-L', socket, 'kill-server'], { stdio: 'ignore' })
    } catch {
      /* server already exited (tmux exits when its last session is killed) */
    }
    // tmux leaves the socket file behind; it lives at $TMUX_TMPDIR (or /tmp)/tmux-<uid>/<name>.
    if (process.getuid) {
      rmSync(join(process.env.TMUX_TMPDIR || '/tmp', `tmux-${process.getuid()}`, socket), { force: true })
    }
  })

  it('creates a session, attaches, runs a command, detaches and kills', async () => {
    const core = await startCore(corePath, {
      env: { HARNESS_TMUX_SOCKET: socket, HARNESS_TMUX_CONFIG: '/dev/null', HARNESS_HOSTS_FILE: hostsFile, HARNESS_STATE_FILE: stateFile, SHELL: '/bin/sh' }
    })
    try {
      const c = await connect(`${core.info.url}?token=${core.info.token}`)
      try {
        const hosts = await c.request('hosts.list')
        expect(hosts.find((h: any) => h.id === 'local')).toMatchObject({ state: 'connected' })

        // v4: directory suggestions. A fresh state file has no recent entries.
        const before = await c.request('remote.suggestDirs', { host: 'local' })
        expect(Array.isArray(before.recent)).toBe(true)
        expect(Array.isArray(before.repos)).toBe(true)
        expect(before.recent).toEqual([])

        const created = await c.request('sessions.create', {
          host: 'local',
          dir: tmpdir(),
          name: 'it-test',
          command: 'shell'
        })
        expect(created.id).toBeTruthy()
        await c.waitEvent(
          (e) => e.event === 'sessions.changed' && e.host === 'local' && e.sessions.some((s: any) => s.id === created.id)
        )

        const { stream } = await c.request('term.attach', {
          host: 'local',
          session: created.id,
          cols: 80,
          rows: 24
        })
        expect(typeof stream).toBe('number')
        c.send(stream, 'echo harness-$((6*7))\r')
        const text = await c.waitOutput(stream, /harness-42/)
        expect(text).toContain('harness-42')

        // A successful create records its dir in the host's recent list.
        const after = await c.request('remote.suggestDirs', { host: 'local' })
        expect(after.recent).toContain(tmpdir())

        await c.request('term.detach', { stream })
        await c.request('sessions.kill', { host: 'local', session: created.id })
      } finally {
        c.ws.close()
      }
    } finally {
      await core.stop()
    }
  })
})
