import { expect, keys, shot, test, useEnv } from '../support/harness'
import { privateTmux } from '../support/isolated'

// tmux has no Windows build; these scenarios drive a real local tmux.
test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv()

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

/** tmux against the core's private server only (never the user's). */
const tmux = (...args: string[]): void => privateTmux(['-L', env().core.tmuxSocket, '-f', '/dev/null', ...args])

function session(name: string, color?: string): void {
  tmux('new-session', '-d', '-s', name, '-c', env().core.home)
  tmux('set-option', '-t', name, '@harness_name', name)
  if (color) tmux('set-option', '-t', name, '@harness_color', color)
  tmux('send-keys', '-t', name, 'exec sleep 600', 'Enter')
}

test('a tagged session shows a colour dot in the row and in the rail', async ({ page }, info) => {
  session('col-a', 'blue')
  session('col-b')
  session('col-c', 'red')
  session('col-d', 'purple')
  const row = local(page).locator('[data-row]').filter({ hasText: 'col-a' })
  await expect(row).toBeVisible()
  await expect(row.getByTestId('session-color')).toHaveAttribute('data-color', 'blue')
  await expect(local(page).locator('[data-row]').filter({ hasText: 'col-b' }).getByTestId('session-color')).toHaveCount(0)
  await shot(page, info, 'session-colors')

  await page.keyboard.press(keys.rail)
  const item = page.locator('[data-testid="rail-item"][aria-label*="col-a"]')
  await expect(item.getByTestId('session-color')).toHaveAttribute('data-color', 'blue')
  await expect(item).toHaveAttribute('aria-label', /Blue/)
  await expect(page.locator('[data-testid="rail-item"][aria-label*="col-b"]').getByTestId('session-color')).toHaveCount(0)
  await shot(page, info, 'session-colors-rail')
  await page.keyboard.press(keys.rail)
})

test('the context menu sets and clears the colour; the filter matches it', async ({ page }) => {
  session('pick-a')
  session('pick-b')
  const row = local(page).locator('[data-row]').filter({ hasText: 'pick-a' })
  await expect(row).toBeVisible()

  await row.click({ button: 'right' })
  await expect(page.getByTestId('color-none')).toHaveAttribute('aria-checked', 'true')
  await expect(page.getByRole('menuitemradio', { name: 'Colour: Green' })).toHaveAttribute('aria-checked', 'false')
  await page.getByTestId('color-green').click()
  await expect(page.getByRole('menu')).toBeHidden()
  await expect(row.getByTestId('session-color')).toHaveAttribute('data-color', 'green')

  await row.click({ button: 'right' })
  await expect(page.getByTestId('color-green')).toHaveAttribute('aria-checked', 'true')
  await page.keyboard.press('Escape')

  const filter = page.getByRole('textbox', { name: 'Filter sessions' })
  await filter.fill('green')
  await expect(local(page).locator('[data-row]')).toHaveCount(1)
  await expect(row).toBeVisible()
  await filter.fill('')

  await row.click({ button: 'right' })
  await page.getByRole('menuitemradio', { name: 'No colour' }).click()
  await expect(row.getByTestId('session-color')).toHaveCount(0)
})
