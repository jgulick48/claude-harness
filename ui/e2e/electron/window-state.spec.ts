import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { _electron as electron, expect, test, type ElectronApplication } from '@playwright/test'
import { envWithoutTmux, prepareIsolation, type Isolation } from '../support/isolated'
import { coreBin, uiRoot } from '../support/paths'

// The window comes back at the size and position it had when the app quit.
test.skip(process.platform === 'win32', 'needs tmux')
test.describe.configure({ mode: 'serial' })

let iso: Isolation
let app: ElectronApplication | null = null

async function launch(): Promise<ElectronApplication> {
  const a = await electron.launch({
    args: ['.', ...(process.platform === 'linux' ? ['--no-sandbox'] : [])],
    cwd: uiRoot,
    env: {
      ...envWithoutTmux(),
      ...iso.env,
      HARNESS_CORE_PATH: coreBin,
      // Keep Electron's own profile (and window-state.json) inside the fixture HOME.
      XDG_CONFIG_HOME: `${iso.home}/.config`
    } as Record<string, string>
  })
  await a.firstWindow()
  return a
}

const bounds = (a: ElectronApplication): Promise<{ x: number; y: number; width: number; height: number }> =>
  a.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].getBounds())

test.beforeAll(async () => {
  iso = await prepareIsolation({})
})

test.afterAll(async () => {
  await app?.close().catch(() => {})
  await iso?.cleanup()
})

test('size and position survive a restart', async () => {
  app = await launch()
  const want = { x: 60, y: 70, width: 900, height: 640 }
  await app.evaluate(({ BrowserWindow }, b) => BrowserWindow.getAllWindows()[0].setBounds(b), want)
  // Some window managers adjust the request slightly; restore whatever was applied.
  await expect.poll(() => bounds(app!).then((b) => b.width)).toBe(want.width)
  const applied = await bounds(app)
  const userData = await app.evaluate(({ app }) => app.getPath('userData'))
  await app.close()
  app = null

  const file = join(userData, 'window-state.json')
  expect(existsSync(file)).toBe(true)
  expect(JSON.parse(readFileSync(file, 'utf8'))).toMatchObject({ width: applied.width, height: applied.height, maximized: false })

  app = await launch()
  await expect.poll(() => bounds(app!)).toEqual(applied)
})
