import type { Page } from '@playwright/test'
import { expect, shot, test, useEnv, keys } from '../support/harness'

// tmux has no Windows build; these scenarios drive a real local tmux.
test.skip(process.platform === 'win32', 'needs tmux')

useEnv({ homeDirs: ['proj/alpha', 'proj/beta', 'proj/gamma'] })

const NAMES = ['alpha-work', 'beta-build', 'gamma-docs']
const rows = (page: Page) => page.locator('[data-row]')
const filter = (page: Page) => page.getByRole('textbox', { name: 'Filter sessions' })
const sidebarWidth = async (page: Page): Promise<number> =>
  Math.round((await page.locator('aside.sidebar').boundingBox())!.width)

async function create(page: Page, name: string): Promise<void> {
  await page.getByRole('button', { name: 'New terminal', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New terminal' })
  await dialog.getByRole('combobox', { name: 'Directory' }).fill(`~/proj/${name.split('-')[0]}/`)
  await dialog.getByLabel('Name (optional)').fill(name)
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name })).toHaveAttribute('aria-current', 'true')
}

/** Labels of the session rows, in sidebar order. */
async function order(page: Page): Promise<string[]> {
  return (await rows(page).locator('.row-label').allInnerTexts()).map((t) => t.trim())
}

/** Each test loads a fresh page: attach the first session so a terminal has focus. */
async function focusTerminal(page: Page): Promise<void> {
  await expect(rows(page)).toHaveCount(3)
  await rows(page).first().click()
  await page.locator('.xterm').click()
  // The attached terminal focuses itself asynchronously; settle before keys.
  await expect(page.locator('.xterm-helper-textarea').first()).toBeFocused()
}

test('create three sessions', async ({ page }) => {
  for (const n of NAMES) await create(page, n)
  await expect(rows(page)).toHaveCount(3)
})

test('Ctrl+K focuses the filter, typing filters, Enter attaches, Esc clears', async ({ page }, info) => {
  await focusTerminal(page)
  await page.keyboard.press(keys.filter)
  await expect(filter(page)).toBeFocused()

  await page.keyboard.type('beta')
  await expect(rows(page)).toHaveCount(1)
  await expect(rows(page).first()).toContainText('beta-build')
  await expect(page.getByTestId('host-group')).toHaveCount(1)
  await shot(page, info, 'sidebar-filter')

  await page.keyboard.press('Enter')
  await expect(page.getByRole('button', { name: 'beta-build' })).toHaveAttribute('aria-current', 'true')

  // The new terminal takes focus asynchronously (once its attach completes);
  // wait for that, or it can steal focus back from the filter afterwards.
  await expect(page.locator('.xterm-helper-textarea').first()).toBeFocused()
  // Ctrl/Cmd+K brings the filter back.
  await page.keyboard.press(keys.filter)
  await expect(filter(page)).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(filter(page)).toHaveValue('')
  await expect(rows(page)).toHaveCount(3)

  // No match shows a message and hides the host.
  await page.keyboard.type('zzzz')
  await expect(page.getByText('No matches')).toBeVisible()
  await expect(page.getByTestId('host-group')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(page.getByText('No matches')).toBeHidden()

  // Next Esc returns focus to the terminal.
  await page.keyboard.press('Escape')
  await expect(page.locator('.xterm-helper-textarea')).toBeFocused()
})

test('Ctrl+2 attaches the second session; arrows and Enter navigate rows', async ({ page }) => {
  const labels = await order(page)
  await focusTerminal(page)
  await page.keyboard.press(keys.jump(2))
  // The second session row itself: a name-based button lookup is a substring
  // match and can also hit other buttons (pane headers, menus) under load.
  await expect(rows(page).nth(1)).toHaveAttribute('aria-current', 'true')
  await expect(rows(page).nth(1)).toContainText(labels[1])
  // Same race as above: the new terminal focuses itself once attached; wait,
  // or it steals focus back from the filter and ArrowDown goes to the shell.
  await expect(page.locator('.xterm-helper-textarea').first()).toBeFocused()

  await page.keyboard.press(keys.filter)
  await page.keyboard.press('ArrowDown')
  await expect(rows(page).nth(0)).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('ArrowDown')
  await expect(rows(page).nth(2)).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(rows(page).nth(2)).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(rows(page).nth(2)).toHaveAttribute('aria-current', 'true')
})

// Regression: a terminal focused itself when its (async) attach finished,
// even if the user had moved on - typing in the filter right after Enter got
// cut off mid-word (seen on macOS CI, where attaches are a bit slower).
test('a finishing attach does not steal focus from the filter', async ({ page }) => {
  await focusTerminal(page)
  await page.keyboard.press(keys.filter)
  await page.keyboard.type('gamma')
  await page.keyboard.press('Enter') // attach starts; don't wait for it
  await page.keyboard.press(keys.filter)
  await page.keyboard.type('alp')
  // The gamma row is filtered out by now, so check the attached pane's header.
  await expect(page.locator('.pane-leaf').first()).toContainText('gamma-docs')
  await page.waitForTimeout(1500) // let the attach finish and any focus move happen
  await expect(filter(page)).toBeFocused()
  await expect(filter(page)).toHaveValue('alp')
  await page.keyboard.press('Escape')
})

test('context menu offers open in split', async ({ page }) => {
  await rows(page).first().click({ button: 'right' })
  await expect(page.getByRole('menuitem', { name: 'Open in split right' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Open in split down' })).toBeVisible()
  await page.keyboard.press('Escape')
})

test('settings menu has Appearance', async ({ page }) => {
  await page.getByRole('button', { name: 'Settings' }).click()
  await expect(page.getByRole('menuitem', { name: 'Appearance…' })).toBeVisible()
  await page.keyboard.press('Escape')
})

test('Ctrl+B collapses to a rail; clicking attaches; the setting survives a reload', async ({ page }, info) => {
  const labels = await order(page)
  await focusTerminal(page)
  await page.keyboard.press(keys.rail)
  const items = page.getByTestId('rail-item')
  await expect(items).toHaveCount(3)
  await expect(rows(page)).toHaveCount(0)
  expect(await sidebarWidth(page)).toBeLessThanOrEqual(52)
  await expect(items.nth(0)).toHaveAttribute('title', new RegExp(labels[0]))

  await items.nth(0).click()
  await expect(items.nth(0)).toHaveAttribute('aria-current', 'true')
  await shot(page, info, 'sidebar-rail')

  await page.reload()
  await expect(page.getByTestId('rail-item')).toHaveCount(3)
  expect(await sidebarWidth(page)).toBeLessThanOrEqual(52)

  await page.getByRole('button', { name: 'Expand sidebar' }).click()
  await expect(rows(page)).toHaveCount(3)
})

test('dragging the handle resizes; width survives reload; double-click resets', async ({ page }, info) => {
  const handle = page.getByRole('separator', { name: 'Resize sidebar' })
  const start = await sidebarWidth(page)
  expect(start).toBe(280)
  const box = (await handle.boundingBox())!
  const y = box.y + box.height / 2
  await page.mouse.move(box.x + box.width / 2, y)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2 + 60, y, { steps: 5 })
  await page.mouse.up()
  const wide = await sidebarWidth(page)
  expect(wide).toBeGreaterThan(start + 40)
  await shot(page, info, 'sidebar-wide')

  await page.reload()
  await expect(rows(page)).toHaveCount(3)
  expect(await sidebarWidth(page)).toBe(wide)

  // Keyboard: arrows resize, clamped at the bounds.
  await handle.focus()
  await page.keyboard.press('ArrowLeft')
  expect(await sidebarWidth(page)).toBe(wide - 16)
  await page.keyboard.press('Home')
  expect(await sidebarWidth(page)).toBe(200)
  await page.keyboard.press('End')
  expect(await sidebarWidth(page)).toBe(480)

  await handle.dblclick()
  expect(await sidebarWidth(page)).toBe(280)
})
