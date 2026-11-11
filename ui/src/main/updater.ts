// Update state machine. Deliberately free of `electron` / `electron-updater`
// imports so it can be unit-tested; index.ts injects the real autoUpdater.

export type UpdateState =
  | { state: 'unsupported'; manual?: boolean }
  | { state: 'idle' }
  | { state: 'checking' }
  | { state: 'available'; version: string; manualDownload?: boolean }
  | { state: 'downloading'; version: string; percent: number }
  | { state: 'ready'; version: string }
  | { state: 'up-to-date' }
  | { state: 'error'; message: string }

export interface AutoUpdaterLike {
  autoDownload: boolean
  autoInstallOnAppQuit: boolean
  on(event: string, listener: (...args: any[]) => void): unknown
  checkForUpdates(): Promise<unknown>
  downloadUpdate(): Promise<unknown>
  quitAndInstall(isSilent?: boolean, isForceRunAfter?: boolean): void
}

export interface UpdateControllerOptions {
  updater: AutoUpdaterLike
  platform: NodeJS.Platform
  supported: boolean
  openExternal: (url: string) => void | Promise<void>
  log?: (msg: string) => void
  /** Awaited before quitAndInstall (e.g. gracefully stop the Go core). */
  beforeInstall?: () => Promise<void>
  /** Called if the install fails after beforeInstall ran (e.g. restart the Go core). */
  onInstallFailed?: () => void
  owner?: string
  repo?: string
  startDelayMs?: number
  intervalMs?: number
}

const SAFE_VERSION = /^[0-9A-Za-z][0-9A-Za-z.+-]*$/

export class UpdateController {
  private readonly updater: AutoUpdaterLike
  private readonly platform: NodeJS.Platform
  private readonly supported: boolean
  private readonly openExternal: (url: string) => void | Promise<void>
  private readonly log: (msg: string) => void
  private readonly beforeInstall: () => Promise<void>
  private readonly onInstallFailed: () => void
  private readonly owner: string
  private readonly repo: string
  private readonly startDelayMs: number
  private readonly intervalMs: number

  private current: UpdateState
  private listeners = new Set<(s: UpdateState) => void>()
  private manualPending = false
  private startTimer: ReturnType<typeof setTimeout> | null = null
  private intervalTimer: ReturnType<typeof setInterval> | null = null
  private installing = false

  constructor(opts: UpdateControllerOptions) {
    this.updater = opts.updater
    this.platform = opts.platform
    this.supported = opts.supported
    this.openExternal = opts.openExternal
    this.log = opts.log ?? (() => {})
    this.beforeInstall = opts.beforeInstall ?? (() => Promise.resolve())
    this.onInstallFailed = opts.onInstallFailed ?? (() => {})
    this.owner = opts.owner ?? 'jgulick48'
    this.repo = opts.repo ?? 'claude-harness'
    this.startDelayMs = opts.startDelayMs ?? 10_000
    this.intervalMs = opts.intervalMs ?? 4 * 60 * 60 * 1000
    this.current = this.supported ? { state: 'idle' } : { state: 'unsupported' }

    // macOS builds are unsigned: Squirrel.Mac cannot auto-install, so only notify.
    const isMac = this.platform === 'darwin'
    this.updater.autoDownload = !isMac
    this.updater.autoInstallOnAppQuit = !isMac

    this.updater.on('checking-for-update', () => {
      if (this.current.state !== 'checking') this.set({ state: 'checking' })
    })
    this.updater.on('update-available', (info: { version: string }) => {
      this.manualPending = false
      this.set({ state: 'available', version: info.version, manualDownload: this.platform === 'darwin' })
    })
    this.updater.on('update-not-available', () => {
      this.set(this.manualPending ? { state: 'up-to-date' } : { state: 'idle' })
      this.manualPending = false
    })
    this.updater.on('download-progress', (p: { percent: number }) => {
      const version = this.versionOf(this.current)
      if (version === null) return
      this.set({ state: 'downloading', version, percent: Math.max(0, Math.min(100, Math.round(p.percent))) })
    })
    this.updater.on('update-downloaded', (info: { version: string }) => {
      this.manualPending = false
      this.set({ state: 'ready', version: info.version })
    })
    this.updater.on('error', (err: unknown) => this.handleError(err))
  }

  get state(): UpdateState {
    return this.current
  }

  onState(cb: (s: UpdateState) => void): () => void {
    this.listeners.add(cb)
    return () => this.listeners.delete(cb)
  }

  start(): void {
    if (!this.supported || this.startTimer || this.intervalTimer) return
    this.startTimer = setTimeout(() => {
      this.startTimer = null
      void this.check({ manual: false })
    }, this.startDelayMs)
    this.intervalTimer = setInterval(() => void this.check({ manual: false }), this.intervalMs)
  }

  stop(): void {
    if (this.startTimer) clearTimeout(this.startTimer)
    if (this.intervalTimer) clearInterval(this.intervalTimer)
    this.startTimer = null
    this.intervalTimer = null
  }

  async check(opts: { manual: boolean }): Promise<void> {
    if (!this.supported) {
      if (opts.manual) this.set({ state: 'unsupported', manual: true })
      return
    }
    const s = this.current.state
    if (s === 'checking' || s === 'downloading' || s === 'ready' || s === 'available') {
      // Already busy or already know about an update; just let the UI re-show it.
      if (opts.manual) this.emit()
      return
    }
    this.manualPending = opts.manual
    this.set({ state: 'checking' })
    try {
      await this.updater.checkForUpdates()
    } catch (e) {
      // The 'error' event normally fires first; this covers a bare rejection.
      if ((this.current as UpdateState).state === 'checking') this.handleError(e)
    }
  }

  /** Quit and install a downloaded update. Only valid in `ready`. */
  async install(): Promise<void> {
    if (this.current.state !== 'ready' || this.installing) return
    this.installing = true
    try {
      // quitAndInstall launches the installer synchronously, before before-quit
      // runs, so anything holding files (the Go core) must be stopped first.
      await this.beforeInstall()
    } catch (e) {
      this.log(`beforeInstall failed: ${(e as Error).message}`)
    }
    try {
      // On failure electron-updater emits 'error' (handled in handleError) and
      // does not quit; a throw is covered here.
      this.updater.quitAndInstall()
    } catch (e) {
      this.failInstall(e instanceof Error ? e.message : String(e))
    }
  }

  private failInstall(message: string): void {
    this.installing = false
    this.log(`install failed: ${message}`)
    this.set({ state: 'error', message: `Update install failed: ${message}` })
    this.onInstallFailed()
  }

  /** macOS: open the GitHub release page for the available version. */
  openDownload(): void {
    const s = this.current
    if (this.platform !== 'darwin' || s.state !== 'available') return
    if (!SAFE_VERSION.test(s.version)) {
      this.log(`refusing to open release page for odd version ${JSON.stringify(s.version)}`)
      return
    }
    void this.openExternal(`https://github.com/${this.owner}/${this.repo}/releases/tag/v${s.version}`)
  }

  private versionOf(s: UpdateState): string | null {
    return s.state === 'available' || s.state === 'downloading' || s.state === 'ready' ? s.version : null
  }

  private handleError(err: unknown): void {
    const message = err instanceof Error ? err.message : String(err)
    if (this.installing) {
      this.failInstall(message)
      return
    }
    const manual = this.manualPending
    this.manualPending = false
    this.log(`update error${manual ? ' (manual)' : ''}: ${message}`)
    if (manual) {
      this.set({ state: 'error', message })
    } else if (this.current.state === 'checking' || this.current.state === 'downloading') {
      this.set({ state: 'idle' })
    }
  }

  private set(s: UpdateState): void {
    this.current = s
    this.emit()
  }

  private emit(): void {
    for (const cb of this.listeners) cb(this.current)
  }
}
