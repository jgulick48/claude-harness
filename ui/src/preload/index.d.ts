export interface CoreInfo {
  url: string
  port: number
  token: string
  version: string
  protocol: number
}

export type CoreStatus =
  | { state: 'starting' }
  | { state: 'ready' }
  | { state: 'error'; message: string }
  | { state: 'exited'; code: number | null }

export type UpdateStatus =
  | { state: 'unsupported'; manual?: boolean }
  | { state: 'idle' }
  | { state: 'checking' }
  | { state: 'available'; version: string; manualDownload?: boolean }
  | { state: 'downloading'; version: string; percent: number }
  | { state: 'ready'; version: string }
  | { state: 'up-to-date' }
  | { state: 'error'; message: string }

export interface HarnessApi {
  getCoreInfo(): Promise<CoreInfo | { error: string }>
  onCoreStatus(cb: (s: CoreStatus) => void): () => void
  onUpdateStatus(cb: (s: UpdateStatus) => void): () => void
  /** Fires when the machine wakes from sleep. Returns unsubscribe. */
  onSystemResume(cb: () => void): () => void
  checkForUpdates(): Promise<void>
  installUpdate(): Promise<void>
  downloadUpdate(): Promise<void>
}

declare global {
  interface Window {
    harness: HarnessApi
  }
}
