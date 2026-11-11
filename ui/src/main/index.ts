import { app, BrowserWindow, ipcMain, Menu, powerMonitor, shell, type MenuItemConstructorOptions } from 'electron'
import { autoUpdater } from 'electron-updater'
import { join } from 'node:path'
import { resolveCorePath, startCore, type CoreInfo, type RunningCore } from './core'
import { UpdateController } from './updater'

type CoreStatus =
  | { state: 'starting' }
  | { state: 'ready' }
  | { state: 'error'; message: string }
  | { state: 'exited'; code: number | null }

let mainWindow: BrowserWindow | null = null
let core: RunningCore | null = null
let coreInfo: CoreInfo | { error: string } | null = null
let status: CoreStatus = { state: 'starting' }
let quitting = false
let updates: UpdateController | null = null

/** Gracefully stop the Go core (same path as before-quit) so an installer can replace its files. */
async function stopCoreForQuit(): Promise<void> {
  quitting = true
  const c = core
  core = null
  if (c) await c.stop().catch(() => {})
}

function updatesSupported(): boolean {
  if (!app.isPackaged) return false
  if (process.env.HARNESS_DISABLE_UPDATES === '1') return false
  if (process.platform === 'linux' && !process.env.APPIMAGE) return false
  return true
}

function setupUpdates(): UpdateController {
  const log = (m: string): void => console.log(`[updater] ${m}`)
  autoUpdater.logger = { info: log, warn: log, error: log, debug: () => {} }
  const ctl = new UpdateController({
    updater: autoUpdater,
    platform: process.platform,
    supported: updatesSupported(),
    openExternal: (url) => shell.openExternal(url),
    beforeInstall: stopCoreForQuit,
    onInstallFailed: () => {
      // The core was stopped for the installer; bring it back since we're staying open.
      quitting = false
      void launchCore()
    },
    log
  })
  ctl.onState((s) => mainWindow?.webContents.send('update:status', s))
  ipcMain.handle('update:check', () => ctl.check({ manual: true }))
  ipcMain.handle('update:install', () => ctl.install())
  ipcMain.handle('update:download', () => ctl.openDownload())
  return ctl
}

function buildMenu(): void {
  const isMac = process.platform === 'darwin'
  const checkItem: MenuItemConstructorOptions = {
    label: 'Check for Updates…',
    click: () => void updates?.check({ manual: true })
  }
  const template: MenuItemConstructorOptions[] = [
    ...(isMac
      ? [
          {
            label: app.name,
            submenu: [
              { role: 'about' },
              checkItem,
              { type: 'separator' },
              { role: 'services' },
              { type: 'separator' },
              { role: 'hide' },
              { role: 'hideOthers' },
              { role: 'unhide' },
              { type: 'separator' },
              { role: 'quit' }
            ]
          } as MenuItemConstructorOptions
        ]
      : []),
    { role: 'fileMenu' },
    { role: 'editMenu' },
    { role: 'viewMenu' },
    { role: 'windowMenu' },
    { role: 'help', submenu: isMac ? [] : [checkItem] }
  ]
  Menu.setApplicationMenu(Menu.buildFromTemplate(template))
}

function setStatus(s: CoreStatus): void {
  status = s
  mainWindow?.webContents.send('core:status', s)
}

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1200,
    height: 800,
    show: false,
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false
    }
  })
  mainWindow.on('ready-to-show', () => mainWindow?.show())
  mainWindow.on('closed', () => {
    mainWindow = null
  })
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:/.test(url)) void shell.openExternal(url)
    return { action: 'deny' }
  })
  mainWindow.webContents.on('did-finish-load', () => {
    mainWindow?.webContents.send('core:status', status)
    if (updates) mainWindow?.webContents.send('update:status', updates.state)
  })

  if (process.env.ELECTRON_RENDERER_URL) {
    void mainWindow.loadURL(process.env.ELECTRON_RENDERER_URL)
  } else {
    void mainWindow.loadFile(join(__dirname, '../renderer/index.html'))
  }
}

async function launchCore(): Promise<void> {
  setStatus({ state: 'starting' })
  const corePath = resolveCorePath({
    env: process.env,
    isPackaged: app.isPackaged,
    resourcesPath: process.resourcesPath,
    appPath: app.getAppPath(),
    platform: process.platform
  })
  try {
    core = await startCore(corePath, { logger: (l) => console.log(`[core] ${l}`) })
    coreInfo = core.info
    core.onExit((e) => {
      if (quitting) return
      setStatus({ state: 'exited', code: e.code })
    })
    setStatus({ state: 'ready' })
  } catch (e) {
    const message = `${(e as Error).message} (${corePath})`
    coreInfo = { error: message }
    setStatus({ state: 'error', message })
  }
}

// Badge count (macOS dock, Linux Unity); a no-op elsewhere.
ipcMain.on('attention:count', (_e, n: unknown) => {
  const count = typeof n === 'number' && Number.isFinite(n) ? Math.max(0, Math.floor(n)) : 0
  app.setBadgeCount(count)
})

ipcMain.on('window:focus', () => {
  if (!mainWindow) return
  if (mainWindow.isMinimized()) mainWindow.restore()
  mainWindow.show()
  mainWindow.focus()
})

ipcMain.handle('core:info', () => coreInfo ?? { error: 'core is not started yet' })

app.whenReady().then(() => {
  updates = setupUpdates()
  powerMonitor.on('resume', () => mainWindow?.webContents.send('system:resume'))
  buildMenu()
  createWindow()
  void launchCore()
  updates.start()
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})

app.on('before-quit', (event) => {
  updates?.stop()
  if (quitting || !core) return
  event.preventDefault()
  void stopCoreForQuit().finally(() => app.quit())
})
