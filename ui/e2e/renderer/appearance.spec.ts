import { expect, shot, runInTerminal, test, useEnv, keys } from '../support/harness'

test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv({ homeDirs: ['proj/alpha'] })

async function openDialog(page: import('@playwright/test').Page) {
  await page.getByRole('button', { name: 'Settings', exact: true }).click()
  await page.getByRole('menuitem', { name: /Appearance/ }).click()
  const dialog = page.getByRole('dialog', { name: 'Appearance' })
  await expect(dialog).toBeVisible()
  return dialog
}

const bg = (page: import('@playwright/test').Page) =>
  page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--bg').trim())

test('app theme override persists and syncs the native theme', async ({ page }, info) => {
  let dialog = await openDialog(page)
  await expect(dialog.getByRole('button', { name: 'System' })).toBeFocused()
  await shot(page, info, 'appearance-dialog')

  await dialog.getByRole('button', { name: 'Light' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  expect(await bg(page)).toBe('#ffffff')

  await dialog.getByRole('button', { name: 'Dark' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  expect(await bg(page)).toBe('#14161a')
  expect(await page.evaluate(() => getComputedStyle(document.body).backgroundColor)).toBe('rgb(20, 22, 26)')
  expect(await page.evaluate(() => window.__e2e.themeSource)).toBe('dark')

  // Esc closes and focus returns to the Settings button.
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: 'Settings', exact: true })).toBeFocused()

  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  expect(await bg(page)).toBe('#14161a')
  await expect.poll(() => page.evaluate(() => window.__e2e.themeSource)).toBe('dark')

  // Back to System removes the attribute.
  dialog = await openDialog(page)
  await dialog.getByRole('button', { name: 'System' }).click()
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', /.*/)
  await expect.poll(() => page.evaluate(() => window.__e2e.themeSource)).toBe('system')
})

test('terminal theme preset and font zoom apply to a live terminal', async ({ page }, info) => {
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const nt = page.getByRole('dialog', { name: 'New terminal' })
  await nt.getByRole('combobox', { name: 'Directory' }).fill('~/proj/alpha/')
  await nt.getByLabel('Name (optional)').fill('appearance')
  await nt.getByRole('button', { name: 'Create' }).click()
  await expect(nt).toBeHidden()
  await runInTerminal(page, 'echo appearance-ok', 'appearance-ok')

  const dialog = await openDialog(page)
  await dialog.getByLabel('Terminal theme').selectOption('solarized-dark')
  await expect(dialog.getByTestId('term-swatch')).toBeVisible()
  await expect(page.locator('.xterm-scrollable-element').first()).toHaveCSS('background-color', 'rgb(0, 43, 54)')
  // The padding around the grid uses the preset too (no strip of the app color).
  await expect(page.locator('.term-body').first()).toHaveCSS('background-color', 'rgb(0, 43, 54)')
  await shot(page, info, 'appearance-solarized-dialog')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await shot(page, info, 'terminal-solarized-dark')

  // Zoom shortcuts work with the terminal focused.
  const rowHeight = () => page.locator('.xterm-rows > div').first().evaluate((el) => el.getBoundingClientRect().height)
  await page.locator('.xterm').click()
  const before = await rowHeight()
  await page.keyboard.press(keys.zoomIn)
  await page.keyboard.press(keys.zoomIn)
  await expect.poll(rowHeight).toBeGreaterThan(before)
  await page.keyboard.press(keys.zoomReset)
  await expect.poll(rowHeight).toBe(before)
  await page.keyboard.press(keys.zoomOut)
  await expect.poll(rowHeight).toBeLessThan(before)

  // Persists across reload; terminal is reattached by the app, preset stays.
  await page.reload()
  await page.getByRole('button', { name: /appearance/ }).first().click()
  await expect(page.locator('.xterm-scrollable-element').first()).toHaveCSS('background-color', 'rgb(0, 43, 54)')
})
