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

export interface HarnessApi {
  getCoreInfo(): Promise<CoreInfo | { error: string }>
  onCoreStatus(cb: (s: CoreStatus) => void): () => void
}

declare global {
  interface Window {
    harness: HarnessApi
  }
}
