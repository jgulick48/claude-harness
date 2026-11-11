import { execFileSync } from 'node:child_process'
import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test'
import { bridgeKeys } from '../support/bridge'
import { envWithoutTmux, prepareIsolation, privateTmuxOutput, type Isolation } from '../support/isolated'
import { coreBin, uiRoot } from '../support/paths'

// Drives the real Electron app (built into ui/out) with a real core. On Linux
// run under a display: xvfb-run -a npx playwright test --project electron
test.skip(process.platform === 'win32', 'needs tmux')
test.describe.configure({ mode: 'serial' })

let iso: Isolation
let app: ElectronApplication
let page: Page
let corePid = 0
let quit = false

/** Direct children of `pid` as {pid, command}, via ps (works on Linux and macOS). */
function childrenOf(parent: number): Array<{ pid: number; command: string }> {
  const out = execFileSync('ps', ['-A', '-o', 'pid=,ppid=,command='], { encoding: 'utf8' })
  const rows: Array<{ pid: number; ppid: number; command: string }> = []
  for (const line of out.split('\n')) {
    const m = /^\s*(\d+)\s+(\d+)\s+(.*)$/.exec(line)
    if (m) rows.push({ pid: Number(m[1]), ppid: Number(m[2]), command: m[3] })
  }
  return rows.filter((r) => r.ppid === parent)
}

function alive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

test.beforeAll(async () => {
  // sshtestd first, so the app's core inherits its agent as SSH_AUTH_SOCK.
  iso = await prepareIsolation({ ssh: true })
  app = await electron.launch({
    args: ['.', ...(process.platform === 'linux' ? ['--no-sandbox'] : [])],
    cwd: uiRoot,
    env: {
      ...envWithoutTmux(),
      ...iso.env,
      HARNESS_CORE_PATH: coreBin,
      // Keep Electron's own profile inside the fixture HOME.
      XDG_CONFIG_HOME: `${iso.home}/.config`
    } as Record<string, string>
  })
  page = await app.firstWindow()
  await page.setViewportSize({ width: 1280, height: 800 })
})

test.afterAll(async () => {
  // A failed test may leave the app running; close it, then clean everything.
  if (!quit) await app?.close().catch(() => {})
  await iso?.cleanup()
})

test('window opens, the real preload bridge works and the core connects', async () => {
  expect(await app.evaluate(({ app }) => app.isPackaged)).toBe(false)
  // The real bridge exposes exactly the keys the e2e fake covers.
  const keys = await page.evaluate(() => Object.keys(window.harness).sort())
  expect(keys).toEqual(Object.keys(bridgeKeys).sort())

  await expect(page.getByText(/^Core:/)).toHaveText(/Core: connected · v\S+ · protocol 5/)
  const host = page.getByTestId('host-group').filter({ hasText: 'This computer' })
  await expect(host.getByLabel('connected', { exact: true })).toBeVisible()

  // The core is a child of the Electron main process; remember it for the quit test.
  const core = childrenOf(app.process().pid!).filter((c) => c.command.includes('harness-core'))
  expect(core).toHaveLength(1)
  corePid = core[0].pid
  expect(alive(corePid)).toBe(true)
})

test('a session can be created and typed into', async () => {
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New terminal' })
  await dialog.getByLabel('Name (optional)').fill('electron-sess')
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: /electron-sess/ })).toBeVisible()
  await page.locator('.xterm').click()
  await page.keyboard.type('echo harness-$((6*7))')
  await page.keyboard.press('Enter')
  await expect(page.locator('.xterm-rows')).toContainText('harness-42')
})

test('Check for Updates… reports that updates are unavailable in an unpackaged build', async () => {
  const labels = await app.evaluate(({ Menu }) => {
    const found: string[] = []
    const walk = (items: Electron.MenuItem[]): void => {
      for (const it of items) {
        if (it.label) found.push(it.label)
        if (it.submenu) walk(it.submenu.items)
      }
    }
    walk(Menu.getApplicationMenu()?.items ?? [])
    return found
  })
  expect(labels).toContain('Check for Updates…')

  await app.evaluate(({ Menu }) => {
    const find = (items: Electron.MenuItem[]): Electron.MenuItem | undefined => {
      for (const it of items) {
        if (it.label === 'Check for Updates…') return it
        const r = it.submenu ? find(it.submenu.items) : undefined
        if (r) return r
      }
      return undefined
    }
    const item = find(Menu.getApplicationMenu()?.items ?? [])
    item?.click()
  })
  await expect(page.getByText('Updates are not available in this build')).toBeVisible()
})

test('powerMonitor resume makes a disconnected SSH host reconnect immediately', async () => {
  test.setTimeout(90_000)
  const ssh = iso.ssh!
  await page.getByRole('button', { name: 'Add host', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Add host' })
  await dialog.getByLabel('Target').fill(`${iso.sshUser}@127.0.0.1:${ssh.port}`)
  await dialog.getByLabel('Name (optional)').fill('testbox')
  await dialog.getByRole('button', { name: 'Add host', exact: true }).click()
  const box = page.getByTestId('host-group').filter({ hasText: 'testbox' })
  const trust = box.getByRole('group', { name: /Verify host key/ })
  await expect(trust).toContainText(ssh.hostKeyFingerprint)
  await trust.getByRole('button', { name: 'Trust and connect' }).click()
  await expect(box.getByLabel('connected', { exact: true })).toBeVisible()

  // Take it down and keep it down, so the core is waiting on a backoff timer.
  ssh.block()
  ssh.drop()
  await expect(box.getByLabel('connected', { exact: true })).toHaveCount(0)
  await expect(box.getByText(/Reconnecting in \d+s/)).toBeVisible({ timeout: 20_000 })

  // Record the requests the renderer sends from here on.
  await page.evaluate(() => {
    const w = window as unknown as { __sent: string[] }
    w.__sent = []
    const orig = WebSocket.prototype.send
    WebSocket.prototype.send = function (this: WebSocket, d: Parameters<WebSocket['send']>[0]): void {
      if (typeof d === 'string') w.__sent.push(d)
      return orig.call(this, d)
    }
  })
  await app.evaluate(({ powerMonitor }) => {
    powerMonitor.emit('resume')
  })
  await expect
    .poll(() => page.evaluate(() => (window as unknown as { __sent: string[] }).__sent.filter((m) => m.includes('"hosts.connect"')).length), {
      timeout: 5000
    })
    .toBeGreaterThan(0)
  // Bring the server back; the host recovers.
  ssh.unblock()
  await expect(box.getByLabel('connected', { exact: true })).toBeVisible({ timeout: 10_000 })
})

test('quitting the app stops the core; the tmux session survives', async () => {
  expect(corePid).toBeGreaterThan(0)
  expect(alive(corePid)).toBe(true)
  await app.close()
  quit = true
  await expect.poll(() => alive(corePid), { timeout: 10_000 }).toBe(false)
  // The session is still running in the private tmux server.
  const sessions = privateTmuxOutput(['-L', iso.tmuxSocket, 'list-sessions', '-F', '#{session_name}'])
  expect(sessions.trim().length).toBeGreaterThan(0)
})
