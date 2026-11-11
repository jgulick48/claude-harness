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
  /** Number of sessions waiting on the user; main sets the dock/launcher badge. */
  setAttentionCount(n: number): void
  /** Show and focus the app window (e.g. from a notification click). */
  focusWindow(): void
  /** Plain text from the system clipboard. */
  readClipboard(): Promise<string>
  writeClipboard(text: string): Promise<void>
  /** Shows the terminal's right-click menu; resolves to the chosen item. */
  showTerminalMenu(hasSelection: boolean): Promise<'copy' | 'paste' | null>
}

declare global {
  interface Window {
    harness: HarnessApi
  }
}
