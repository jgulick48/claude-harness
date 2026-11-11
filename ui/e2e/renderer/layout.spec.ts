import type { Locator, Page } from '@playwright/test'
import { expect, shot, test, useEnv, keys } from '../support/harness'

// Split panes: each pane is its own tmux attach stream. tmux has no Windows build.
test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv({ homeDirs: ['proj'] })

async function createSession(page: Page, name: string): Promise<void> {
  const { url, token } = env().core.core.info
  await page.evaluate(
    async ({ url, token, dir, name }) => {
      const ws = new WebSocket(`${url}?token=${token}`)
      await new Promise((r) => (ws.onopen = r))
      await new Promise<void>((resolve, reject) => {
        ws.onmessage = (ev) => {
          if (typeof ev.data !== 'string') return
          const m = JSON.parse(ev.data)
          if (m.type === 'result') resolve()
          else if (m.type === 'error') reject(new Error(JSON.stringify(m)))
        }
        ws.send(JSON.stringify({ type: 'sessions.create', id: 1, host: 'local', dir, name, command: 'shell' }))
      })
      ws.close()
    },
    { url, token, dir: env().core.home, name }
  )
  await expect(row(page, name)).toBeVisible()
}

const row = (page: Page, name: string): Locator => page.getByRole('button', { name: new RegExp(`^${name}(\\s|$)`) })
const panes = (page: Page): Locator => page.locator('.pane-leaf')
const pane = (page: Page, i: number): Locator => panes(page).nth(i)
const rows = (p: Locator): Locator => p.locator('.xterm-rows')

async function typeInto(page: Page, p: Locator, cmd: string, expected: string): Promise<void> {
  await p.locator('.xterm').click()
  await expect(p).toHaveAttribute('data-focused', 'true')
  await page.keyboard.type(cmd)
  await page.keyboard.press('Enter')
  await expect(rows(p)).toContainText(expected)
}

test('split panes: two sessions, isolation, no duplicates, resize, restore, close', async ({ page }, info) => {
  await expect(page.getByText(/^Core:/)).toHaveText(/connected/)
  await createSession(page, 'left-one')
  await createSession(page, 'right-two')
  await createSession(page, 'third-three')

  await row(page, 'left-one').click()
  await expect(panes(page)).toHaveCount(1)
  await expect(rows(pane(page, 0))).toBeVisible()
  await page.locator('.xterm').click()

  // Ctrl+Shift+D splits right while the terminal has focus; plain keys are not stolen.
  await page.keyboard.press(keys.splitRight)
  await expect(panes(page)).toHaveCount(2)
  await expect(page.getByTestId('empty-pane')).toBeVisible()
  await expect(page.getByTestId('empty-pane')).toContainText('Ctrl+Shift+D')
  await expect(pane(page, 1)).toHaveAttribute('data-focused', 'true')

  await row(page, 'right-two').click()
  await expect(rows(pane(page, 1))).toBeVisible()
  await expect(page.getByTestId('empty-pane')).toHaveCount(0)

  // Each pane's output stays in its own pane.
  await typeInto(page, pane(page, 1), 'echo pane-$((2+1))', 'pane-3')
  await typeInto(page, pane(page, 0), 'echo pane-$((1+1))', 'pane-2')
  await expect(rows(pane(page, 0))).not.toContainText('pane-3')
  await expect(rows(pane(page, 1))).not.toContainText('pane-2')

  // Attaching a session that is already shown focuses its pane (no duplicate).
  await pane(page, 1).locator('.xterm').click()
  await expect(pane(page, 1)).toHaveAttribute('data-focused', 'true')
  await row(page, 'left-one').click()
  await expect(panes(page)).toHaveCount(2)
  await expect(pane(page, 0)).toHaveAttribute('data-focused', 'true')
  await expect(page.locator('.xterm')).toHaveCount(2)

  // Focus by direction, from inside a terminal.
  await page.keyboard.press(keys.focusRight)
  await expect(pane(page, 1)).toHaveAttribute('data-focused', 'true')
  await page.keyboard.press(keys.focusLeft)
  await expect(pane(page, 0)).toHaveAttribute('data-focused', 'true')

  // Drag the divider.
  const sep = page.locator('.layout-root').getByRole('separator')
  await expect(sep).toHaveCount(1)
  const w0 = (await pane(page, 0).boundingBox())!.width
  const b = (await sep.boundingBox())!
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2)
  await page.mouse.down()
  await page.mouse.move(b.x + b.width / 2 + 120, b.y + b.height / 2, { steps: 6 })
  await page.mouse.up()
  await expect.poll(async () => (await pane(page, 0).boundingBox())!.width).toBeGreaterThan(w0 + 80)
  // ...and with the keyboard.
  const before = Number(await sep.getAttribute('aria-valuenow'))
  await sep.focus()
  await page.keyboard.press('ArrowLeft')
  await expect(sep).toHaveAttribute('aria-valuenow', String(before - 2))
  const dragged = await sep.getAttribute('aria-valuenow')

  // A third pane below the right one, for the screenshots.
  await pane(page, 1).locator('.xterm').click()
  await page.keyboard.press(keys.splitDown)
  await expect(panes(page)).toHaveCount(3)
  await row(page, 'third-three').click()
  await typeInto(page, pane(page, 2), 'echo third-$((3*3))', 'third-9')
  await shot(page, info, 'split-panes')

  // Reload: layout and attachments come back.
  await page.reload()
  await expect(page.getByText(/^Core:/)).toHaveText(/connected/)
  await expect(panes(page)).toHaveCount(3)
  await expect(page.locator('.xterm')).toHaveCount(3)
  await expect(rows(pane(page, 0))).toContainText('pane-2')
  await expect(rows(pane(page, 1))).toContainText('pane-3')
  await expect(rows(pane(page, 2))).toContainText('third-9')
  await expect(pane(page, 2)).toHaveAttribute('data-focused', 'true')
  await expect(page.locator('.layout-root').getByRole('separator').first()).toHaveAttribute('aria-valuenow', dragged!)

  // Named layouts: save, change, switch back.
  await page.getByRole('button', { name: /Layouts/ }).click()
  await page.getByRole('menuitem', { name: 'Save layout as…' }).click()
  await page.getByRole('textbox', { name: 'Layout name' }).fill('trio')
  await page.keyboard.press('Enter')
  await expect(page.getByRole('menuitem', { name: 'trio' })).toBeVisible()
  await page.keyboard.press('Escape')

  // Close a pane with the header button; the sibling takes the space.
  await pane(page, 2).getByRole('button', { name: 'Close pane' }).click()
  await expect(panes(page)).toHaveCount(2)
  // Ctrl+Shift+W closes the focused pane too.
  await pane(page, 0).locator('.xterm').click()
  await page.keyboard.press(keys.closePane)
  await expect(panes(page)).toHaveCount(1)
  await expect(rows(pane(page, 0))).toBeVisible()
  // The last pane empties instead of going away.
  await page.keyboard.press(keys.closePane)
  await expect(panes(page)).toHaveCount(1)
  await expect(page.getByTestId('empty-pane')).toBeVisible()

  await page.getByRole('button', { name: /Layouts/ }).click()
  await page.getByRole('menuitem', { name: 'trio' }).click()
  await expect(panes(page)).toHaveCount(3)
  await expect(page.locator('.xterm')).toHaveCount(3)
  await page.getByRole('button', { name: /Layouts/ }).click()
  await page.getByRole('button', { name: 'Delete layout trio' }).click()
  await expect(page.getByRole('menuitem', { name: 'trio' })).toHaveCount(0)
})

test('a restored layout whose session was killed shows a note', async ({ page }) => {
  await expect(page.getByText(/^Core:/)).toHaveText(/connected/)
  // Seeded on the next load (the page's own pagehide save would overwrite a direct write).
  await page.addInitScript(() => {
    if (sessionStorage.getItem('seeded')) return
    sessionStorage.setItem('seeded', '1')
    localStorage.setItem(
      'harness.layout',
      JSON.stringify({
        root: {
          kind: 'split', dir: 'row', sizes: [1, 1],
          children: [{ kind: 'pane', ref: { host: 'local', session: '$9999' } }, { kind: 'pane', ref: null }]
        },
        focus: 0
      })
    )
    localStorage.setItem('harness.layouts', '{broken')
  })
  await page.reload()
  await expect(page.getByText(/^Core:/)).toHaveText(/connected/)
  await expect(panes(page)).toHaveCount(2)
  await expect(page.getByText('Session no longer exists')).toBeVisible()
})

test('a corrupt saved layout is ignored', async ({ page }) => {
  await page.addInitScript(() => {
    if (sessionStorage.getItem('seeded')) return
    sessionStorage.setItem('seeded', '1')
    localStorage.setItem('harness.layout', '{"root":{"kind":"split"}}')
  })
  await page.reload()
  await expect(page.getByText(/^Core:/)).toHaveText(/connected/)
  await expect(panes(page)).toHaveCount(1)
})

test('the pane cap shows a message', async ({ page }) => {
  await expect(page.getByText(/^Core:/)).toHaveText(/connected/)
  await page.locator('.pane-leaf').click()
  for (let i = 0; i < 6; i++) await page.keyboard.press(keys.splitRight)
  await expect(panes(page)).toHaveCount(6)
  await expect(page.locator('.toast')).toContainText('At most 6 panes')
})
