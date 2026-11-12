import { test as base, expect, type Page, type TestInfo } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { installFakeBridge } from './bridge'
import { startIsolatedCore, type IsolatedCore, type IsolatedOpts } from './isolated'
import { rendererOut, screenshotDir } from './paths'
import { serveStatic, type StaticServer } from './staticServer'

export { expect }
export type { IsolatedCore }

export interface Env {
  core: IsolatedCore
  server: StaticServer
}

export const test = base

const pageProblems = new WeakMap<Page, string[]>()

/**
 * One real core and one static server per spec file. Call at the top level of
 * a file; the returned getter is valid inside tests. Also fails any test that
 * produced a CSP violation or an uncaught page error.
 */
export function useEnv(opts: IsolatedOpts = {}): () => Env {
  let env: Env | undefined
  base.describe.configure({ mode: 'serial' })
  base.beforeAll(async () => {
    const core = await startIsolatedCore(opts)
    try {
      // Served from http://127.0.0.1:<port>; the page's own CSP meta tag is used unmodified.
      env = { core, server: await serveStatic(rendererOut) }
    } catch (e) {
      await core.stop()
      throw e
    }
  })
  base.afterAll(async () => {
    await env?.server.close()
    await env?.core.stop()
    env = undefined
  })
  base.beforeEach(async ({ page }) => {
    if (!env) throw new Error('env not started')
    const problems: string[] = []
    page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`))
    page.on('console', (m) => {
      if (/content security policy|refused to/i.test(m.text())) problems.push(`csp: ${m.text()}`)
    })
    pageProblems.set(page, problems)
    await page.addInitScript(installFakeBridge, env.core.core.info)
    await page.goto(env.server.url)
  })
  base.afterEach(async ({ page }) => {
    const problems = pageProblems.get(page) ?? []
    expect(problems, 'page errors / CSP violations').toEqual([])
  })
  return () => {
    if (!env) throw new Error('env not started')
    return env
  }
}

/** Saves a 1280x800 screenshot as ui/e2e/screenshots/<scheme>-<name>.png. */
export async function shot(page: Page, info: TestInfo, name: string): Promise<string> {
  mkdirSync(screenshotDir, { recursive: true })
  const scheme = info.project.use.colorScheme ?? 'light'
  const path = join(screenshotDir, `${scheme}-${name}.png`)
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.screenshot({ path })
  return path
}

/** Types into the focused terminal and waits for `text` in the rendered rows. */
export async function runInTerminal(page: Page, command: string, expected: string | RegExp): Promise<void> {
  await page.locator('.xterm').click()
  await page.keyboard.type(command)
  await page.keyboard.press('Enter')
  await expect(page.locator('.xterm-rows')).toContainText(expected)
}

/**
 * The app's own shortcuts for the platform the browser runs on. The renderer
 * picks Cmd on macOS and Ctrl elsewhere (navigator.platform), and the
 * Playwright browser runs on the same OS as the tests, so match that here.
 * Bindings: src/renderer/src/shortcuts.ts (layout), sidebarNav.ts (sidebar),
 * appearanceModel.ts (zoom).
 */
const isMac = process.platform === 'darwin'
const mod = isMac ? 'Meta' : 'Control'
export const keys = {
  filter: `${mod}+k`,
  rail: `${mod}+b`,
  jump: (n: number): string => `${mod}+${n}`,
  zoomIn: `${mod}+Equal`,
  zoomOut: `${mod}+Minus`,
  zoomReset: `${mod}+Digit0`,
  splitRight: isMac ? 'Meta+d' : 'Control+Shift+D',
  splitDown: isMac ? 'Meta+Shift+d' : 'Control+Shift+E',
  closePane: isMac ? 'Meta+w' : 'Control+Shift+W',
  focusLeft: `${mod}+Alt+ArrowLeft`,
  focusRight: `${mod}+Alt+ArrowRight`
}
