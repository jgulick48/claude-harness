import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, useEnv } from '../support/harness'
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
