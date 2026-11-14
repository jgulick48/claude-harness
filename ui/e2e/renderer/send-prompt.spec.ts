import { expect, shot, test, useEnv } from '../support/harness'
import { privateTmux, privateTmuxOutput } from '../support/isolated'

// tmux has no Windows build; these scenarios drive a real local tmux.
test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv()
const mod = process.platform === 'darwin' ? 'Meta' : 'Control'

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

/** tmux against the core's private server only (never the user's). */
const tmux = (...args: string[]): void => privateTmux(['-L', env().core.tmuxSocket, '-f', '/dev/null', ...args])
const pane = (name: string): string =>
  privateTmuxOutput(['-L', env().core.tmuxSocket, '-f', '/dev/null', 'capture-pane', '-p', '-t', name])

// Every test starts from an empty server: they count selected sessions.
test.afterEach(() => {
  try {
    tmux('kill-server')
  } catch {
    /* no server */
  }
})

// A session whose pane runs `cat` (not a shell, so its status counts) and
// whose Claude status is set by hand, as a hook would.
function session(name: string, status: string): void {
  tmux('new-session', '-d', '-s', name, '-c', '/')
  tmux('set-option', '-t', name, '@harness_name', name)
  tmux('send-keys', '-t', name, 'exec cat', 'Enter')
  tmux('set-option', '-t', name, '@claude_status', status)
  tmux('set-option', '-t', name, '@claude_status_at', String(Math.floor(Date.now() / 1000)))
}

test('Ctrl+click selects sessions; the dialog sends to idle ones only', async ({ page }, info) => {
  session('bc-idle', 'idle')
  session('bc-wait', 'waiting')
  session('bc-work', 'working')
  const row = (n: string) => local(page).locator('[data-row]').filter({ hasText: n })
  await expect(row('bc-idle')).toContainText('Idle')
  await expect(row('bc-wait')).toContainText('Waiting')
  await expect(row('bc-work')).toContainText('Working')

  for (const n of ['bc-idle', 'bc-wait', 'bc-work']) await row(n).click({ modifiers: [mod] })
  await expect(row('bc-idle')).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByTestId('selection-bar')).toContainText('3 selected')
  // Selecting didn't attach anything.
  await expect(row('bc-idle')).not.toHaveAttribute('aria-current', 'true')
  // Ctrl+click again deselects, Esc clears.
  await row('bc-work').click({ modifiers: [mod] })
  await expect(page.getByTestId('selection-bar')).toContainText('2 selected')
  await row('bc-work').click({ modifiers: [mod] })
  await expect(page.getByTestId('selection-bar')).toContainText('3 selected')

  await page.getByTestId('selection-bar').getByRole('button', { name: 'Send prompt…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Send prompt to sessions' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByTestId('prompt-text')).toBeFocused()
  const target = (n: string) => dialog.getByTestId('prompt-target').filter({ hasText: n })
  await expect(target('bc-idle')).toHaveAttribute('data-state', 'send')
  await expect(target('bc-wait')).toHaveAttribute('data-state', 'skip')
  await expect(target('bc-wait')).toContainText('waiting for input, not included')
  await expect(target('bc-work')).toHaveAttribute('data-state', 'skip')
  await expect(target('bc-work')).toContainText('Claude is working')
  const send = dialog.getByTestId('prompt-send')
  await expect(send).toBeDisabled() // no text yet

  const cb = dialog.getByRole('checkbox', { name: 'Include sessions waiting for input' })
  await expect(cb).not.toBeChecked()
  await dialog.getByTestId('prompt-text').fill('hello-from-broadcast')
  await expect(send).toHaveText('Send to 1 session')
  await cb.check()
  await expect(target('bc-wait')).toHaveAttribute('data-state', 'send')
  await expect(send).toHaveText('Send to 2 sessions')
  await cb.uncheck()
  await expect(send).toHaveText('Send to 1 session')
  await shot(page, info, 'send-prompt')

  await send.click()
  await expect(target('bc-idle')).toHaveAttribute('data-state', 'sent')
  await expect(target('bc-idle')).toContainText('Sent')
  await expect(dialog.getByTestId('prompt-summary')).toHaveText('Sent to 1 session.')
  await expect.poll(() => pane('bc-idle')).toContain('hello-from-broadcast')
  expect(pane('bc-wait')).not.toContain('hello-from-broadcast')
  expect(pane('bc-work')).not.toContain('hello-from-broadcast')

  // A fully successful send clears the selection.
  await dialog.getByRole('button', { name: 'Close' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByTestId('selection-bar')).toHaveCount(0)
})

test('Select waiting, the waiting opt-in and Ctrl+Enter to send', async ({ page }) => {
  session('bw-a', 'waiting')
  session('bw-b', 'idle')
  const row = (n: string) => local(page).locator('[data-row]').filter({ hasText: n })
  await expect(row('bw-a')).toContainText('Waiting')
  await expect(row('bw-b')).toContainText('Idle')

  await page.getByRole('button', { name: 'Select waiting' }).click()
  await expect(page.getByTestId('selection-bar')).toContainText('1 selected')
  await expect(row('bw-a')).toHaveAttribute('aria-selected', 'true')
  await expect(row('bw-b')).toHaveAttribute('aria-selected', 'false')

  await page.getByTestId('selection-bar').getByRole('button', { name: 'Send prompt…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Send prompt to sessions' })
  await dialog.getByRole('checkbox', { name: 'Include sessions waiting for input' }).check()
  await dialog.getByTestId('prompt-text').fill('answer-yes')
  await dialog.getByTestId('prompt-text').press(`${mod}+Enter`)
  await expect(dialog.getByTestId('prompt-target').filter({ hasText: 'bw-a' })).toHaveAttribute('data-state', 'sent')
  await expect.poll(() => pane('bw-a')).toContain('answer-yes')

  // Esc closes the dialog.
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
})

test('the settings menu offers every idle session when nothing is selected', async ({ page }) => {
  session('bs-a', 'idle')
  session('bs-b', 'idle')
  session('bs-c', 'working')
  await expect(local(page).locator('[data-row]').filter({ hasText: 'bs-b' })).toContainText('Idle')
  await page.getByRole('button', { name: 'Settings' }).click()
  await page.getByRole('menuitem', { name: 'Send prompt to sessions…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Send prompt to sessions' })
  await expect(dialog.getByTestId('prompt-target')).toHaveCount(2)
  await expect(dialog.getByTestId('prompt-target').filter({ hasText: 'bs-c' })).toHaveCount(0)
  await dialog.getByTestId('prompt-text').fill('to-all-idle')
  await dialog.getByTestId('prompt-send').click()
  await expect(dialog.getByTestId('prompt-summary')).toHaveText('Sent to 2 sessions.')
  await expect.poll(() => pane('bs-a')).toContain('to-all-idle')
  await expect.poll(() => pane('bs-b')).toContain('to-all-idle')
})
