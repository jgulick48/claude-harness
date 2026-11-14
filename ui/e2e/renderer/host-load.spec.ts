import { expect, shot, test, useEnv } from '../support/harness'

// The local host's load is read with a POSIX shell script (Linux or macOS).
test.skip(process.platform === 'win32', 'needs a POSIX shell')

useEnv()

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

async function openMenu(page: import('@playwright/test').Page): Promise<void> {
  await local(page).locator('.host-header').click({ button: 'right' })
}

test('Show CPU and memory toggles the host load bars and persists', async ({ page }, info) => {
  const bars = local(page).getByTestId('host-load')
  await expect(bars).toBeHidden()

  await openMenu(page)
  const item = page.getByRole('menuitemcheckbox', { name: 'Show CPU and memory' })
  await expect(item).toHaveAttribute('aria-checked', 'false')
  await item.click()

  await expect(bars).toBeVisible({ timeout: 10_000 })
  await expect(bars).toContainText('CPU')
  await expect(bars).toContainText('Mem')
  await expect(bars).toContainText(/\d+ GB|\d+ MB/)
  // Memory fills at once; CPU needs a second reading (about 5 s).
  await expect(bars).toContainText(/CPU\s*\d+%/, { timeout: 15_000 })
  await expect(bars).toHaveAttribute('title', /Load average: .*\n\d+ cores?\nUpdated /)
  await shot(page, info, 'host-load')

  await openMenu(page)
  await expect(page.getByRole('menuitemcheckbox', { name: 'Show CPU and memory' })).toHaveAttribute('aria-checked', 'true')
  await page.keyboard.press('Escape')

  await page.reload()
  await expect(local(page).getByTestId('host-load')).toBeVisible({ timeout: 10_000 })

  await openMenu(page)
  await page.getByRole('menuitemcheckbox', { name: 'Show CPU and memory' }).click()
  await expect(local(page).getByTestId('host-load')).toBeHidden()

  await page.reload()
  await expect(local(page)).toBeVisible()
  await expect(local(page).getByTestId('host-load')).toBeHidden()
})
