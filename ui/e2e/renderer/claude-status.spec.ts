import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, keys, test, useEnv } from '../support/harness'
import { privateTmux } from '../support/isolated'

// tmux has no Windows build; these scenarios drive a real local tmux.
test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv()

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

/** tmux against the core's private server only (never the user's). */
const tmux = (...args: string[]): void => privateTmux(['-L', env().core.tmuxSocket, '-f', '/dev/null', ...args])

test('Claude status shows in the row, title and dock badge', async ({ page }) => {
  // A session started outside the app, with a non-shell foreground process
  // (a shell would trip the "Claude exited" guard and hide the status).
  tmux('new-session', '-d', '-s', 'cs', '-c', env().core.home)
  tmux('send-keys', '-t', 'cs', 'exec sleep 600', 'Enter')

  // Unnamed sessions are labelled by their directory (the fixture HOME), and
  // this one was made outside the app, so it carries the "external" tag.
  const row = local(page).getByRole('button', { name: /external/ })
  await expect(row).toBeVisible()
  await expect(row).not.toContainText(/Working|Waiting|Idle/)

  // What a Claude Code hook would do (see internal/claudehooks).
  tmux('set-option', '-t', 'cs', '@claude_status', 'waiting')
  tmux('set-option', '-t', 'cs', '@claude_status_at', String(Math.floor(Date.now() / 1000)))
  await expect(row).toContainText('Waiting')
  await expect(page).toHaveTitle('(1) Claude Harness')
  await expect.poll(() => page.evaluate(() => window.__e2e.attention)).toBe(1)

  tmux('set-option', '-t', 'cs', '@claude_status', 'working')
  await expect(row).toContainText('Working')
  await expect(page).toHaveTitle('Claude Harness')
  await expect.poll(() => page.evaluate(() => window.__e2e.attention)).toBe(0)

  tmux('set-option', '-t', 'cs', '@claude_status', 'idle')
  await expect(row).toContainText('Idle')
})

test('the row shows how long Claude has been in its state', async ({ page }) => {
  tmux('new-session', '-d', '-s', 'age', '-c', env().core.home)
  tmux('set-option', '-t', 'age', '@harness_name', 'age-test')
  tmux('send-keys', '-t', 'age', 'exec sleep 600', 'Enter')
  const row = local(page).locator('[data-row]').filter({ hasText: 'age-test' })
  const now = Math.floor(Date.now() / 1000)

  tmux('set-option', '-t', 'age', '@claude_status_at', String(now - 12 * 60 - 30))
  tmux('set-option', '-t', 'age', '@claude_status', 'waiting')
  await expect(row.locator('.status')).toHaveText(/Waiting\s+12m/)

  // A fresh state shows no age (under a minute).
  tmux('set-option', '-t', 'age', '@claude_status_at', String(now))
  tmux('set-option', '-t', 'age', '@claude_status', 'working')
  await expect(row.locator('.status')).toHaveText(/^\W*Working$/)

  // The rail tooltip carries the age too.
  tmux('set-option', '-t', 'age', '@claude_status_at', String(now - 3 * 3600 - 60))
  tmux('set-option', '-t', 'age', '@claude_status', 'idle')
  await expect(row.locator('.status')).toHaveText(/Idle\s+3h/)
  await page.keyboard.press(keys.rail)
  await expect(page.locator('[data-testid="rail-item"][aria-label*="age-test"]')).toHaveAttribute(
    'aria-label',
    /age-test — Idle 3h/
  )
  await page.keyboard.press(keys.rail)
})

test('the next-waiting shortcut visits waiting sessions, longest-waiting first', async ({ page }) => {
  const now = Math.floor(Date.now() / 1000)
  // Three sessions running a non-shell process, named so their rows are easy to find.
  for (const n of ['nw-a', 'nw-b', 'nw-c']) {
    tmux('new-session', '-d', '-s', n, '-c', env().core.home)
    tmux('set-option', '-t', n, '@harness_name', n)
    tmux('send-keys', '-t', n, 'exec sleep 600', 'Enter')
  }
  const row = (n: string) => local(page).getByRole('button', { name: n, exact: false }).and(page.locator('[data-row]'))
  for (const n of ['nw-a', 'nw-b', 'nw-c']) await expect(row(n)).toBeVisible()

  // Nothing waiting yet: a toast says so and nothing is attached.
  await page.locator('body').click()
  await page.keyboard.press(keys.nextWaiting)
  await expect(page.getByRole('status').filter({ hasText: 'No sessions are waiting' })).toBeVisible()

  // c has waited longest, then a; b is working.
  const wait = (n: string, at: number): void => {
    tmux('set-option', '-t', n, '@claude_status', 'waiting')
    tmux('set-option', '-t', n, '@claude_status_at', String(at))
  }
  wait('nw-c', now - 300)
  wait('nw-a', now - 100)
  tmux('set-option', '-t', 'nw-b', '@claude_status', 'working')
  await expect(row('nw-c')).toContainText('Waiting')
  await expect(row('nw-a')).toContainText('Waiting')

  await page.keyboard.press(keys.nextWaiting)
  await expect(row('nw-c')).toHaveAttribute('aria-current', 'true')
  // The attached terminal takes focus; the shortcut must still work from there.
  await expect(page.locator('.xterm-helper-textarea').first()).toBeFocused()
  await page.keyboard.press(keys.nextWaiting)
  await expect(row('nw-a')).toHaveAttribute('aria-current', 'true')
  await page.keyboard.press(keys.nextWaiting) // wraps around
  await expect(row('nw-c')).toHaveAttribute('aria-current', 'true')

  // Once c is answered, only a is left; from a there is nowhere else to go.
  tmux('set-option', '-t', 'nw-c', '@claude_status', 'working')
  await expect(row('nw-c')).toContainText('Working')
  await page.keyboard.press(keys.nextWaiting)
  await expect(row('nw-a')).toHaveAttribute('aria-current', 'true')
  await page.keyboard.press(keys.nextWaiting)
  await expect(page.getByRole('status').filter({ hasText: 'No other session is waiting' })).toBeVisible()

  // Also in the settings menu.
  tmux('set-option', '-t', 'nw-c', '@claude_status', 'waiting')
  await expect(row('nw-c')).toContainText('Waiting')
  await page.getByRole('button', { name: 'Settings' }).click()
  await page.getByRole('menuitem', { name: /Next waiting session/ }).click()
  await expect(row('nw-c')).toHaveAttribute('aria-current', 'true')
})

test('installing the Claude status hooks writes only into the fixture config', async ({ page }) => {
  const notice = local(page).getByText(/Claude status hooks aren.t installed/)
  await expect(notice).toBeVisible()
  await local(page).getByRole('button', { name: /^Install/ }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('settings.json')
  await dialog.getByRole('button', { name: 'Install', exact: true }).click()
  await expect(notice).toBeHidden()

  // The core's CLAUDE_CONFIG_DIR is pinned to the fixture HOME (isolated.ts).
  const cfg = env().core.env.CLAUDE_CONFIG_DIR
  expect(cfg.startsWith(env().core.home)).toBe(true)
  const settings = JSON.parse(readFileSync(join(cfg, 'settings.json'), 'utf8'))
  const notification = JSON.stringify(settings.hooks.Notification)
  expect(notification).toContain(join(cfg, 'claude-harness', 'claude-status.sh'))
})
