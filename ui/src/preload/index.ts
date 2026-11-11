import { contextBridge, ipcRenderer } from 'electron'
import type { CoreInfo, CoreStatus } from './index.d'

contextBridge.exposeInMainWorld('harness', {
  getCoreInfo: (): Promise<CoreInfo | { error: string }> => ipcRenderer.invoke('core:info'),
  onCoreStatus: (cb: (s: CoreStatus) => void): (() => void) => {
    const listener = (_e: Electron.IpcRendererEvent, s: CoreStatus): void => cb(s)
    ipcRenderer.on('core:status', listener)
    return () => ipcRenderer.removeListener('core:status', listener)
  }
})
