import { existsSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { startCore } from './core'

const exe = process.platform === 'win32' ? 'harness-core.exe' : 'harness-core'
const corePath = process.env.HARNESS_CORE_PATH || resolve(__dirname, '../../../bin', exe)
const exists = existsSync(corePath)

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

describe.skipIf(!exists)('harness-core integration', () => {
  it('starts, speaks protocol v1, and stops cleanly', async () => {
    const logs: string[] = []
    const core = await startCore(corePath, { logger: (l) => logs.push(l) })
    let exitCode: number | null | undefined
    const exited = new Promise<void>((r) =>
      core.onExit((e) => {
        exitCode = e.code
        r()
      })
    )
    try {
      expect(core.info.protocol).toBe(1)
      expect(core.info.version).toBeTruthy()
      expect(core.info.url).toBe(`ws://127.0.0.1:${core.info.port}/ws`)

      const { ws, next } = await openWS(`${core.info.url}?token=${core.info.token}`)
      const hello = await next()
      expect(hello).toMatchObject({ type: 'hello', protocol: 1, version: core.info.version })
      ws.send(JSON.stringify({ type: 'ping', id: 7 }))
      expect(await next()).toEqual({ type: 'pong', id: 7 })
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
