// NOTE: must not import `electron` so it can run under plain Node (vitest).
import { spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { join } from 'node:path'

export interface ResolveOpts {
  env: Record<string, string | undefined>
  isPackaged: boolean
  resourcesPath: string
  appPath: string
  platform: string
}

export function resolveCorePath(opts: ResolveOpts): string {
  const override = opts.env.HARNESS_CORE_PATH
  if (override) return override
  const exe = opts.platform === 'win32' ? 'harness-core.exe' : 'harness-core'
  if (opts.isPackaged) return join(opts.resourcesPath, 'bin', exe)
  return join(opts.appPath, '..', 'bin', exe)
}

export function generateToken(): string {
  return randomBytes(32).toString('hex')
}

export interface ReadyInfo {
  addr: string
  port: number
  version: string
  protocol: number
}

export function parseReadyLine(line: string): ReadyInfo {
  let v: unknown
  try {
    v = JSON.parse(line)
  } catch {
    throw new Error(`ready line is not valid JSON: ${line.slice(0, 200)}`)
  }
  if (typeof v !== 'object' || v === null) throw new Error('ready line is not an object')
  const o = v as Record<string, unknown>
  if (o.type !== 'ready') throw new Error(`unexpected message type: ${String(o.type)}`)
  if (typeof o.addr !== 'string') throw new Error('ready line missing addr')
  if (typeof o.port !== 'number' || !Number.isInteger(o.port)) throw new Error('ready line missing port')
  if (typeof o.version !== 'string') throw new Error('ready line missing version')
  if (typeof o.protocol !== 'number') throw new Error('ready line missing protocol')
  return { addr: o.addr, port: o.port, version: o.version, protocol: o.protocol }
}

export interface CoreInfo {
  url: string
  port: number
  token: string
  version: string
  protocol: number
}

export interface CoreExit {
  code: number | null
  signal: string | null
}

export interface RunningCore {
  info: CoreInfo
  stop(): Promise<void>
  onExit(cb: (e: CoreExit) => void): void
}

export interface StartOpts {
  readyTimeoutMs?: number
  stopTimeoutMs?: number
  logger?: (line: string) => void
  token?: string
  env?: Record<string, string | undefined>
}

export function startCore(corePath: string, opts: StartOpts = {}): Promise<RunningCore> {
  const readyTimeoutMs = opts.readyTimeoutMs ?? 10_000
  const stopTimeoutMs = opts.stopTimeoutMs ?? 3_000
  const log = opts.logger ?? (() => {})
  const token = opts.token ?? generateToken()

  return new Promise<RunningCore>((resolve, reject) => {
    const child = spawn(corePath, ['--listen', '127.0.0.1:0'], {
      env: { ...process.env, ...opts.env, HARNESS_TOKEN: token },
      stdio: ['pipe', 'pipe', 'pipe']
    })

    let settled = false
    let exited = false
    let exitInfo: CoreExit | null = null
    const exitCbs: Array<(e: CoreExit) => void> = []
    const exitWaiters: Array<() => void> = []

    const fail = (err: Error): void => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      if (!exited) child.kill()
      reject(err)
    }

    const timer = setTimeout(
      () => fail(new Error(`harness-core did not report ready within ${readyTimeoutMs}ms`)),
      readyTimeoutMs
    )

    child.on('error', (err) => {
      exited = true
      fail(new Error(`failed to start harness-core: ${err.message}`))
    })

    child.on('exit', (code, signal) => {
      exited = true
      exitInfo = { code, signal }
      exitWaiters.splice(0).forEach((w) => w())
      if (!settled) {
        fail(new Error(`harness-core exited before ready (code=${code}, signal=${signal})`))
      } else {
        exitCbs.forEach((cb) => cb(exitInfo as CoreExit))
      }
    })

    // stdin errors (EPIPE after exit) are expected
    child.stdin.on('error', () => {})

    let errBuf = ''
    child.stderr.setEncoding('utf8')
    child.stderr.on('data', (chunk: string) => {
      errBuf += chunk
      let i: number
      while ((i = errBuf.indexOf('\n')) >= 0) {
        const l = errBuf.slice(0, i).replace(/\r$/, '')
        errBuf = errBuf.slice(i + 1)
        if (l) log(l)
      }
    })

    let outBuf = ''
    child.stdout.setEncoding('utf8')
    child.stdout.on('data', (chunk: string) => {
      if (settled) return
      outBuf += chunk
      const i = outBuf.indexOf('\n')
      if (i < 0) return
      const line = outBuf.slice(0, i).trim()
      try {
        const ready = parseReadyLine(line)
        settled = true
        clearTimeout(timer)
        resolve({
          info: {
            url: `ws://127.0.0.1:${ready.port}/ws`,
            port: ready.port,
            token,
            version: ready.version,
            protocol: ready.protocol
          },
          stop: () =>
            new Promise<void>((res) => {
              if (exited) return res()
              exitWaiters.push(res)
              const killTimer = setTimeout(() => child.kill(), stopTimeoutMs)
              exitWaiters.push(() => clearTimeout(killTimer))
              child.stdin.end()
            }),
          onExit: (cb) => {
            if (exited && exitInfo) cb(exitInfo)
            else exitCbs.push(cb)
          }
        })
      } catch (e) {
        fail(e as Error)
      }
    })
  })
}
