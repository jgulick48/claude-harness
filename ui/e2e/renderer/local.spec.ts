import { join } from 'node:path'
import { expect, shot, runInTerminal, test, useEnv } from '../support/harness'

// tmux has no Windows build; these scenarios drive a real local tmux.
test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv({ homeDirs: ['proj/alpha/.git', 'proj/beta', 'src/gamma/.git'] })

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

test('app loads and the local host is connected', async ({ page }) => {
  await expect(page.getByText(/^Core:/)).toHaveText(/Core: connected · v\S+ · protocol 7/)
  await expect(local(page).getByLabel('connected', { exact: true })).toBeVisible()
  await expect(page.getByText('No sessions')).toBeVisible()
})

test('new terminal via the dialog: completion, drill-in, accept, create, attach, type', async ({ page }, info) => {
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New terminal' })
  const dir = dialog.getByRole('combobox', { name: 'Directory' })
  await expect(dir).toBeFocused()

  await dir.fill('~/pr')
  const list = dialog.getByRole('listbox', { name: 'Directory suggestions' })
  const options = list.getByRole('option')
  await expect(options).toHaveCount(1)
  await expect(options.first()).toContainText('~/proj/')

  // Highlight the entry, then Tab drills into it; typing narrows to alpha, a git repo.
  await page.keyboard.press('ArrowDown')
  await expect(options.first()).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('Tab')
  await expect(dir).toHaveValue('~/proj/')
  await expect(dir).toBeFocused()
  await page.keyboard.type('a')
  const alpha = list.getByRole('option').filter({ hasText: '~/proj/alpha/' })
  await expect(alpha).toHaveCount(1)
  await expect(alpha.getByText('git', { exact: true })).toBeVisible()
  await page.keyboard.press('ArrowDown')
  await expect(alpha).toHaveAttribute('aria-selected', 'true')
  await shot(page, info, 'new-terminal-dropdown')

  await page.keyboard.press('Enter')
  await expect(dir).toHaveValue('~/proj/alpha/')

  await dialog.getByLabel('Name (optional)').fill('alpha-work')
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog).toBeHidden()

  const row = page.getByRole('button', { name: /alpha-work/ })
  await expect(row).toBeVisible()
  await expect(row).toHaveAttribute('aria-current', 'true')
  await runInTerminal(page, 'echo harness-$((6*7))', 'harness-42')
  await shot(page, info, 'main-session')
  // The session really lives in the fixture HOME.
  await runInTerminal(page, 'pwd', join(env().core.home, 'proj/alpha'))
})

test('suggestions list recent and repositories', async ({ page }) => {
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New terminal' })
  const dir = dialog.getByRole('combobox', { name: 'Directory' })
  await dir.fill('')
  const list = dialog.getByRole('listbox', { name: 'Directory suggestions' })
  await expect(list).toContainText('Recent')
  await expect(list).toContainText('Repositories')
  await expect(list.getByText('Recent')).toBeVisible()
  const text = (await list.innerText()).replace(/\s+/g, ' ')
  expect(text).toMatch(/recent.*~\/proj\/alpha.*repositories/i)
  expect(text).toContain('~/src/gamma/')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
})

test('disconnect closes the pane but the session keeps running', async ({ page }) => {
  const row = page.getByRole('button', { name: /alpha-work/ })
  await row.click()
  await expect(row).toHaveAttribute('aria-current', 'true')
  await runInTerminal(page, 'export KEEP=still-$((40+2)); echo set-$((1+1))', 'set-2')
  await row.click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Disconnect' }).click()
  await expect(page.locator('.xterm')).toHaveCount(0)
  await expect(row).toBeVisible()
  await expect(row).not.toHaveAttribute('aria-current', 'true')
  // Not open in a pane: nothing to disconnect.
  await row.click({ button: 'right' })
  await expect(page.getByRole('menuitem', { name: 'Kill session' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Disconnect' })).toHaveCount(0)
  await page.keyboard.press('Escape')
  // Reattaching finds the same shell.
  await row.click()
  await runInTerminal(page, 'echo $KEEP', 'still-42')
})

test('rename via context menu, then kill with confirmation', async ({ page }) => {
  const row = page.getByRole('button', { name: /alpha-work/ })
  await row.click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Rename…' }).click()
  const input = page.getByRole('textbox', { name: 'Session name' })
  await input.fill('renamed-one')
  await input.press('Enter')
  const renamed = page.getByRole('button', { name: /renamed-one/ })
  await expect(renamed).toBeVisible()
  await expect(page.getByRole('button', { name: /alpha-work/ })).toHaveCount(0)

  await renamed.click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Kill session' }).click()
  await expect(page.getByText(/Kill “renamed-one”/)).toBeVisible()
  await page.getByRole('menuitem', { name: 'Kill', exact: true }).click()
  await expect(page.getByRole('button', { name: /renamed-one/ })).toHaveCount(0)
  await expect(page.getByText('No sessions')).toBeVisible()
})

test('host group collapse survives a reload', async ({ page }) => {
  // Make sure there is a session to hide.
  const res = await page.evaluate(
    async ({ url, token, dir }) => {
      const ws = new WebSocket(`${url}?token=${token}`)
      await new Promise((r) => (ws.onopen = r))
      const id = await new Promise<string>((resolve) => {
        ws.onmessage = (ev) => {
          if (typeof ev.data === 'string' && JSON.parse(ev.data).type === 'result') resolve(JSON.parse(ev.data).result.id)
        }
        ws.send(JSON.stringify({ type: 'sessions.create', id: 1, host: 'local', dir, name: 'fold-me', command: 'shell' }))
      })
      ws.close()
      return id
    },
    { url: env().core.core.info.url, token: env().core.core.info.token, dir: env().core.home }
  )
  expect(res).toBeTruthy()
  await expect(page.getByRole('button', { name: /fold-me/ })).toBeVisible()

  await page.getByRole('button', { name: 'Collapse This computer' }).click()
  await expect(page.getByRole('button', { name: 'Expand This computer' })).toHaveAttribute('aria-expanded', 'false')
  await expect(page.getByRole('button', { name: /fold-me/ })).toHaveCount(0)

  await page.reload()
  await expect(page.getByText(/^Core:/)).toHaveText(/connected/)
  await expect(page.getByRole('button', { name: 'Expand This computer' })).toBeVisible()
  await expect(page.getByRole('button', { name: /fold-me/ })).toHaveCount(0)

  await page.getByRole('button', { name: 'Expand This computer' }).click()
  await expect(page.getByRole('button', { name: /fold-me/ })).toBeVisible()
})

test('run as: an invalid user name is rejected before anything is created', async ({ page }) => {
  const rows = page.locator('.row-label')
  // Wait for the session list to load (either empty or with rows) before counting.
  await expect(page.getByText('No sessions').or(rows.first())).toBeVisible()
  const before = await rows.count()
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New terminal' })
  await dialog.getByLabel('Run as user (optional)').fill('bad name')
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog.getByRole('alert')).toHaveText('Invalid user name')
  await expect(dialog).toBeVisible()
  await expect(rows).toHaveCount(before)
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
})

test('run as: a session as another user (fake sudo) shows the tag and starts in that directory', async ({ page }, info) => {
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New terminal' })
  await dialog.getByRole('combobox', { name: 'Directory' }).fill('~/proj/beta')
  await dialog.getByLabel('Name (optional)').fill('as-other')
  await dialog.getByLabel('Run as user (optional)').fill('harness-e2e')
  await shot(page, info, 'new-terminal-run-as')
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog).toBeHidden()

  const row = page.getByRole('button', { name: /as-other/ })
  await expect(row).toBeVisible()
  await expect(row).toContainText('as harness-e2e')
  await expect(page.getByTestId('terminal-pane').locator('.tag')).toHaveText('as harness-e2e')
  await runInTerminal(page, 'pwd', join(env().core.home, 'proj/beta'))
  await shot(page, info, 'run-as-session')
})

test('terminal links: OSC 8 hyperlinks and plain URLs open, show their target, and can be copied', async ({ page }) => {
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New terminal' })
  await dialog.getByRole('combobox', { name: 'Directory' }).fill('~/proj/beta')
  await dialog.getByLabel('Name (optional)').fill('links')
  await dialog.getByLabel('Run as user (optional)').fill('') // remembered from another test
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: /links/ })).toHaveAttribute('aria-current', 'true')
  // The label is computed by the shell so the typed command line doesn't match it.
  await runInTerminal(
    page,
    `printf '\\033]8;;https://example.com/osc8?x=1\\033\\\\%s\\033]8;;\\033\\\\\\n' "osc-$((40+2))"`,
    'osc-42'
  )
  const rows = page.locator('.xterm-rows')
  // xterm redraws its rows (cursor blink), so the spans never count as stable
  // for Playwright's actionability checks: drive the mouse by position.
  // A redraw can catch the row between spans, so retry the lookup.
  const center = async (text: string): Promise<{ x: number; y: number }> => {
    let at: { x: number; y: number } | null = null
    const find = async (): Promise<boolean> => {
      const b = await rows.getByText(text, { exact: true }).boundingBox({ timeout: 1000 }).catch(() => null)
      at = b && b.width > 0 ? { x: b.x + b.width / 2, y: b.y + b.height / 2 } : null
      return at !== null
    }
    await expect.poll(find, { message: `${text} on screen` }).toBe(true)
    return at!
  }
  const box = page.locator('.xterm').locator('xpath=..')
  const osc = await center('osc-42')
  await page.mouse.move(osc.x, osc.y)
  // The tooltip shows the real target, whatever the link's text says.
  await expect(box).toHaveAttribute('title', 'https://example.com/osc8?x=1')
  await page.mouse.click(osc.x, osc.y)
  await expect.poll(() => page.evaluate(() => window.__e2e.opened)).toEqual(['https://example.com/osc8?x=1'])

  // Right-click on the link offers it; "Copy Link Address" puts it on the clipboard.
  await page.evaluate(() => (window.__e2e.menuChoice = 'copyLink'))
  await page.mouse.click(osc.x, osc.y, { button: 'right' })
  await expect.poll(() => page.evaluate(() => window.__e2e.clipboard)).toBe('https://example.com/osc8?x=1')
  expect(await page.evaluate(() => window.__e2e.menuLink)).toBe('https://example.com/osc8?x=1')

  // Plain URLs in the output work the same way.
  await runInTerminal(page, 'echo "https://example.com/plain-$((1+1))"', 'https://example.com/plain-2')
  const plain = await center('https://example.com/plain-2')
  await page.mouse.move(plain.x, plain.y)
  await expect(box).toHaveAttribute('title', 'https://example.com/plain-2')
  await page.mouse.click(plain.x, plain.y)
  await expect
    .poll(() => page.evaluate(() => window.__e2e.opened))
    .toEqual(['https://example.com/osc8?x=1', 'https://example.com/plain-2'])

  // Away from a link the menu gets no link.
  await page.evaluate(() => (window.__e2e.menuChoice = null))
  await page.mouse.move(osc.x + 400, osc.y)
  await expect(box).not.toHaveAttribute('title')
  await page.mouse.click(osc.x + 400, osc.y, { button: 'right' })
  await expect.poll(() => page.evaluate(() => window.__e2e.menuLink)).toBeNull()
})

test('copying from a full-screen program: OSC 52 reaches the clipboard, Shift+drag still selects', async ({ page }) => {
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New terminal' })
  await dialog.getByRole('combobox', { name: 'Directory' }).fill('~/proj/beta')
  await dialog.getByLabel('Name (optional)').fill('osc52')
  await dialog.getByLabel('Run as user (optional)').fill('') // remembered from another test
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog).toBeHidden()

  // What Claude Code does after you select text with the mouse.
  await runInTerminal(page, `printf '\\033]52;c;%s\\007' "$(printf 'copied-%s' $((6*7)) | base64)"; echo sent-$((2+3))`, 'sent-5')
  await expect.poll(() => page.evaluate(() => window.__e2e.clipboard)).toBe('copied-42')
  // A clipboard query is not answered (nothing is typed back into the shell).
  await runInTerminal(page, `printf '\\033]52;c;?\\007'; echo q-$((1+1))`, 'q-2')
  // A reply would land at the new prompt as typed input; it stays empty.
  await page.waitForTimeout(300)
  await expect(page.locator('.xterm-rows')).toContainText(/q-2\$\s*$/)

  // Turn on any-motion mouse tracking (SGR), as Claude Code's full-screen mode
  // does; a plain drag then goes to the program, Shift+drag selects anyway.
  await runInTerminal(page, `printf '\\033[?1003h\\033[?1006h'; echo pick-$((5*5))`, 'pick-25')
  const target = page.locator('.xterm-rows').getByText('pick-25', { exact: true })
  let b: { x: number; y: number; width: number; height: number } | null = null
  await expect
    .poll(async () => (b = await target.boundingBox({ timeout: 1000 }).catch(() => null)) !== null)
    .toBe(true)
  const { x, y, width, height } = b!
  await page.keyboard.down('Shift')
  await page.mouse.move(x + 1, y + height / 2)
  await page.mouse.down()
  await page.mouse.move(x + width - 1, y + height / 2, { steps: 5 })
  await page.mouse.up()
  await page.keyboard.up('Shift')
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+c' : 'Control+Shift+C')
  await expect.poll(() => page.evaluate(() => window.__e2e.clipboard)).toBe('pick-25')
  // Leave the shell sane for anything after this.
  await runInTerminal(page, `printf '\\033[?1003l\\033[?1006l'; echo done-$((3+4))`, 'done-7')
})
