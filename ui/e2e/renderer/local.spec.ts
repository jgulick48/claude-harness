import { join } from 'node:path'
import { expect, shot, runInTerminal, test, useEnv } from '../support/harness'

// tmux has no Windows build; these scenarios drive a real local tmux.
test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv({ homeDirs: ['proj/alpha/.git', 'proj/beta', 'src/gamma/.git'] })

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

test('app loads and the local host is connected', async ({ page }) => {
  await expect(page.getByText(/^Core:/)).toHaveText(/Core: connected · v\S+ · protocol 6/)
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
