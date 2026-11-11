import { contextBridge, ipcRenderer } from 'electron'
import type { CoreInfo, CoreStatus, UpdateStatus } from './index.d'

contextBridge.exposeInMainWorld('harness', {
  getCoreInfo: (): Promise<CoreInfo | { error: string }> => ipcRenderer.invoke('core:info'),
  onCoreStatus: (cb: (s: CoreStatus) => void): (() => void) => {
    const listener = (_e: Electron.IpcRendererEvent, s: CoreStatus): void => cb(s)
    ipcRenderer.on('core:status', listener)
    return () => ipcRenderer.removeListener('core:status', listener)
  },
  onUpdateStatus: (cb: (s: UpdateStatus) => void): (() => void) => {
    const listener = (_e: Electron.IpcRendererEvent, s: UpdateStatus): void => cb(s)
    ipcRenderer.on('update:status', listener)
    return () => ipcRenderer.removeListener('update:status', listener)
  },
  onSystemResume: (cb: () => void): (() => void) => {
    const listener = (): void => cb()
    ipcRenderer.on('system:resume', listener)
    return () => ipcRenderer.removeListener('system:resume', listener)
  },
  checkForUpdates: (): Promise<void> => ipcRenderer.invoke('update:check'),
  installUpdate: (): Promise<void> => ipcRenderer.invoke('update:install'),
  downloadUpdate: (): Promise<void> => ipcRenderer.invoke('update:download'),
  setAttentionCount: (n: number): void => ipcRenderer.send('attention:count', n),
  focusWindow: (): void => ipcRenderer.send('window:focus'),
  // The sandboxed preload can't use Electron's clipboard directly; main does.
  readClipboard: (): Promise<string> => ipcRenderer.invoke('clipboard:read'),
  writeClipboard: (text: string): Promise<void> => ipcRenderer.invoke('clipboard:write', text),
  showTerminalMenu: (hasSelection: boolean): Promise<'copy' | 'paste' | null> =>
    ipcRenderer.invoke('terminal:menu', hasSelection)
})
