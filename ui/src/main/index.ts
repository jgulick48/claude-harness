import { app, BrowserWindow, ipcMain, shell } from 'electron'
import { join } from 'node:path'
import { resolveCorePath, startCore, type CoreInfo, type RunningCore } from './core'

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
  mainWindow.webContents.on('did-finish-load', () => mainWindow?.webContents.send('core:status', status))

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

ipcMain.handle('core:info', () => coreInfo ?? { error: 'core is not started yet' })

app.whenReady().then(() => {
  createWindow()
  void launchCore()
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})

app.on('before-quit', (event) => {
  if (quitting || !core) return
  event.preventDefault()
  quitting = true
  const c = core
  core = null
  c.stop()
    .catch(() => {})
    .finally(() => app.quit())
})
