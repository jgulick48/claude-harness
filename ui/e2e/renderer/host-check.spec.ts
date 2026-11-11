import { expect, shot, test, useEnv } from '../support/harness'

useEnv()

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

test('Check host… lists the setup items, copies a fix command and re-runs', async ({ page }, info) => {
  await local(page).getByRole('button', { name: 'Host actions for This computer' }).click()
  await page.getByRole('menuitem', { name: 'Check host…' }).click()

  const dialog = page.getByRole('dialog', { name: 'Setup check · This computer' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByTestId('check-summary')).toBeVisible()

  // Items arrive in the protocol's fixed order; each has a text status.
  const rows = dialog.locator('[data-testid^="check-"]:not([data-testid="check-summary"])')
  await expect(rows.first()).toBeVisible()
  const ids = (await rows.evaluateAll((els) => els.map((e) => e.getAttribute('data-testid')!.slice(6)))) as string[]
  const order = ['connection', 'tmux', 'shell', 'home', 'claude', 'config', 'hooks']
  expect(ids.length).toBeGreaterThan(0)
  expect(ids).toEqual(order.filter((o) => ids.includes(o)))
  for (const row of await rows.all()) await expect(row).toContainText(/OK|Warning|Problem/)

  // `claude` isn't on the fixture's PATH.
  const claude = dialog.getByTestId('check-claude')
  await expect(claude).toContainText('Warning')
  await expect(claude).toContainText('Claude Code')
  const cmd = await claude.locator('.check-cmd code').innerText()
  expect(cmd.length).toBeGreaterThan(0)
  await claude.getByRole('button', { name: 'Copy' }).click()
  await expect.poll(() => page.evaluate(() => window.__e2e.clipboard)).toBe(cmd)

  const hooks = dialog.getByTestId('check-hooks')
  await expect(hooks).toContainText('Warning')
  await expect(dialog.getByRole('button', { name: 'Install hooks…' })).toBeVisible()
  await expect(dialog).toContainText('never runs')
  await shot(page, info, 'host-check')

  // Re-run re-fetches and shows the same report.
  await dialog.getByRole('button', { name: 'Re-run' }).click()
  await expect(dialog.getByTestId('check-claude')).toContainText('Warning')
  await expect(dialog.getByRole('button', { name: 'Re-run' })).toBeEnabled()

  // Install hooks… opens the existing confirm dialog.
  await dialog.getByRole('button', { name: 'Install hooks…' }).click()
  await expect(page.getByRole('dialog', { name: 'Install Claude status hooks' })).toBeVisible()
  await page.keyboard.press('Escape')
})

test('Esc closes the setup check', async ({ page }) => {
  await local(page).getByRole('button', { name: 'Host actions for This computer' }).click()
  await page.getByRole('menuitem', { name: 'Check host…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Setup check · This computer' })
  await expect(dialog.getByTestId('check-summary')).toBeVisible()
  await dialog.getByRole('button', { name: 'Close' }).focus()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
})
