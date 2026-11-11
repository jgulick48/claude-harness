import { expect, test, useEnv } from '../support/harness'

// Regression test for the Windows scrollbar flicker: classic scrollbars take
// layout space, so anything overflowing the window showed a page scrollbar,
// which shrank the terminal box, which refit xterm, which removed the overflow,
// and so on, many times a second. Linux headless Chromium has classic
// (space-taking) scrollbars too, once Playwright's --hide-scrollbars is dropped.
test.skip(process.platform === 'win32', 'needs tmux')

test.use({
  viewport: { width: 1283, height: 797 },
  launchOptions: { ignoreDefaultArgs: ['--hide-scrollbars'] }
})

// Fractional display scaling (125%, 150%) plus window sizes that do not divide
// evenly are what make xterm's grid round past its box.
const scales = [1.25, 1.5]
const sizes: Array<[number, number]> = [
  [1283, 797],
  [1279, 803],
  [1301, 811],
  [1001, 701],
  [1417, 893],
  [1283, 797]
]

const env = useEnv({ homeDirs: ['proj'] })

for (const scale of scales) {
test.describe(`deviceScaleFactor ${scale}`, () => {
test.use({ deviceScaleFactor: scale })
  test('a tall sidebar causes no page scrollbars and no resize storm', async ({ page }) => {
  const prefix = `bulk${scale * 100}`
  // Make the sidebar far taller than the window.
  const { url, token } = env().core.core.info
  await page.evaluate(
    async ({ url, token, dir, prefix }) => {
      const ws = new WebSocket(`${url}?token=${token}`)
      await new Promise((r) => (ws.onopen = r))
      let nextId = 1
      const pending = new Map<number, () => void>()
      ws.onmessage = (ev) => {
        if (typeof ev.data !== 'string') return
        const m = JSON.parse(ev.data)
        if (m.type === 'result' || m.type === 'error') pending.get(m.id)?.()
      }
      const call = (params: Record<string, unknown>): Promise<void> =>
        new Promise((res) => {
          const id = nextId++
          pending.set(id, res)
          ws.send(JSON.stringify({ ...params, id }))
        })
      for (let i = 0; i < 40; i++) await call({ type: 'sessions.create', host: 'local', dir, name: `${prefix}-${i}`, command: 'shell' })
      ws.close()
    },
    { url, token, dir: env().core.home, prefix }
  )
  await expect(page.getByRole('button', { name: new RegExp(`^${prefix}-39(\\s|$)`) })).toBeVisible()
  await page.getByRole('button', { name: new RegExp(`^${prefix}-0(\\s|$)`) }).click()
  await expect(page.locator('.xterm-rows')).toBeVisible()
  await page.locator('.xterm').click()
  await page.keyboard.type('echo settled-$((6*7))')
  await page.keyboard.press('Enter')
  await expect(page.locator('.xterm-rows')).toContainText('settled-42')

  // The sidebar really does overflow, and it scrolls internally.
  const sidebar = await page.locator('.sidebar-body').evaluate((el) => ({ sh: el.scrollHeight, ch: el.clientHeight }))
  expect(sidebar.sh).toBeGreaterThan(sidebar.ch)

  // No page-level scrollbars: the document never overflows the window.
  const dims = () =>
    page.evaluate(() => ({
      sh: document.documentElement.scrollHeight,
      ih: window.innerHeight,
      sw: document.documentElement.scrollWidth,
      iw: window.innerWidth,
      bodyOverflowY: document.body.scrollHeight - document.body.clientHeight
    }))
  const d = await dims()
  expect(d.sh).toBeLessThanOrEqual(d.ih)
  expect(d.sw).toBeLessThanOrEqual(d.iw)

  // After each (awkward) window size, let the layout settle; then neither the
  // document may overflow nor may anything resize the terminal while idle.
  for (const [w, h] of sizes) {
    await page.setViewportSize({ width: w, height: h })
    await page.waitForTimeout(1000)
    const before = await page.evaluate(() => window.__e2e.resizes)
    await page.waitForTimeout(1500)
    const after = await page.evaluate(() => window.__e2e.resizes)
    expect(after - before, `term.resize requests while idle at ${w}x${h}`).toBe(0)
    const d2 = await dims()
    expect(d2.sh, `scrollHeight at ${w}x${h}`).toBeLessThanOrEqual(d2.ih)
    expect(d2.sw, `scrollWidth at ${w}x${h}`).toBeLessThanOrEqual(d2.iw)
  }
})
})
}
