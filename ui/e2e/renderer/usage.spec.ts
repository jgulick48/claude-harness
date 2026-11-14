import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, shot, test, useEnv } from '../support/harness'
import { privateTmux, privateTmuxOutput } from '../support/isolated'

// tmux has no Windows build; these scenarios drive a real local tmux.
test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv()

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

/** tmux against the core's private server only (never the user's). */
const tmux = (...args: string[]): void => privateTmux(['-L', env().core.tmuxSocket, '-f', '/dev/null', ...args])
const tmuxOut = (...args: string[]): string =>
  privateTmuxOutput(['-L', env().core.tmuxSocket, '-f', '/dev/null', ...args]).trim()

const cfgDir = (): string => env().core.env.CLAUDE_CONFIG_DIR
const readSettings = (): { statusLine?: { command?: string } } =>
  JSON.parse(readFileSync(join(cfgDir(), 'settings.json'), 'utf8'))

// The core reads the usage files at most every 10 s.
const SLOW = { timeout: 25_000 }

test('installing the hooks with the usage box records usage via a status line wrapper', async ({ page }) => {
  await local(page).getByRole('button', { name: /^Install/ }).click()
  const dialog = page.getByRole('dialog')
  const box = dialog.getByRole('checkbox', { name: /Also record Claude usage/ })
  await expect(box).toBeChecked()
  await expect(dialog).toContainText('Your current status line is restored when this is turned off.')
  await dialog.getByRole('button', { name: 'Install', exact: true }).click()
  await expect(dialog).toBeHidden()

  expect(cfgDir().startsWith(env().core.home)).toBe(true)
  await expect.poll(() => readSettings().statusLine?.command ?? '').toContain('statusline.sh')
  expect(existsSync(join(cfgDir(), 'claude-harness', 'statusline.sh'))).toBe(true)
})

test('usage shows in the host indicator, the Usage dialog and the pane header', async ({ page }, info) => {
  tmux('new-session', '-d', '-s', 'us', '-c', env().core.home)
  tmux('set-option', '-t', 'us', '@harness_name', 'usage-s')
  tmux('send-keys', '-t', 'us', 'exec sleep 600', 'Enter')
  const row = local(page).locator('[data-row]').filter({ hasText: 'usage-s' })
  await expect(row).toBeVisible()
  await expect(local(page).getByTestId('usage-indicator')).toBeHidden()

  const now = Math.floor(Date.now() / 1000)
  tmux('set-option', '-t', 'us', '@claude_status', 'working')
  tmux('set-option', '-t', 'us', '@claude_status_at', String(now))

  // What the wrapper writes: <unix seconds> TAB <status line JSON>, one file per tmux server pid and pane.
  const pid = tmuxOut('display', '-p', '-t', 'us', '#{pid}')
  const pane = tmuxOut('display', '-p', '-t', 'us', '#{pane_id}').replace(/^%/, '')
  const dir = join(cfgDir(), 'claude-harness', 'usage')
  mkdirSync(dir, { recursive: true })
  const json = {
    model: { display_name: 'Opus' },
    cost: { total_cost_usd: 0.0123 },
    context_window: { used_percentage: 42.5 },
    rate_limits: {
      five_hour: { used_percentage: 23.5, resets_at: now + 2 * 3600 + 14 * 60 + 30 },
      seven_day: { used_percentage: 74.2, resets_at: now + 3 * 86400 + 4 * 3600 + 30 }
    }
  }
  writeFileSync(join(dir, `${pid}-${pane}`), `${now}\t${JSON.stringify(json)}\n`)

  const ind = local(page).getByTestId('usage-indicator')
  await expect(ind).toBeVisible(SLOW)
  await expect(ind).toContainText('5h')
  await expect(ind).toContainText('23%')
  await expect(ind).toContainText('7d')
  await expect(ind).toContainText('74%')
  await expect(ind).toHaveAttribute('title', /5-hour limit: 23% used, resets in 2h 14m\nWeekly limit: 74% used, resets in 3d 4h\nAs of /)
  // 74% is a warning, not yet high.
  await expect(ind.locator('.usage-fill.warn')).toHaveCount(1)
  await expect(ind.locator('.usage-fill.high')).toHaveCount(0)
  await shot(page, info, 'usage-indicator')

  // The indicator opens the dialog.
  await ind.click()
  const dialog = page.getByRole('dialog', { name: 'Claude usage' })
  await expect(dialog).toBeVisible()
  const fiveHour = dialog.getByTestId('usage-limit-5h')
  await expect(fiveHour).toContainText('23%')
  await expect(fiveHour).toContainText('resets in 2h 14m')
  await expect(dialog.getByTestId('usage-limit-7d')).toContainText('74%')
  await expect(dialog).toContainText('As of')
  const srow = dialog.getByRole('row').filter({ hasText: 'usage-s' })
  await expect(srow).toContainText('Opus')
  await expect(srow).toContainText('42%')
  await expect(srow).toContainText('$0.01')
  await expect(dialog).not.toContainText("Usage recording isn't installed")
  await shot(page, info, 'usage-dialog')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()

  // Also from the Settings menu.
  await page.getByRole('button', { name: 'Settings' }).click()
  await page.getByRole('menuitem', { name: 'Claude usage…' }).click()
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Close' }).click()
  await expect(dialog).toBeHidden()

  // Attached, the pane header shows context and cost.
  await row.click()
  const header = page.getByTestId('pane-usage')
  await expect(header).toContainText('ctx 42%')
  await expect(header).toContainText('$0.01')
  await expect(header).toHaveAttribute('title', 'Context window 42% used · estimated cost $0.0123 (Opus)')
  await expect(header).not.toHaveClass(/warn/)
  await shot(page, info, 'usage-pane-header')

  // The last known limits survive a reload, flagged when Claude isn't reporting them.
  tmux('set-option', '-t', 'us', '-u', '@claude_status')
  await page.reload()
  await expect(local(page).getByTestId('usage-indicator')).toBeVisible(SLOW)
  await expect(local(page).getByTestId('usage-indicator')).toHaveAttribute('title', /\(Claude not running\)$/)
})

test('Stop recording Claude usage restores the status line', async ({ page }) => {
  await local(page).getByRole('button', { name: 'Host actions for This computer' }).click()
  await page.getByRole('menuitem', { name: 'Stop recording Claude usage' }).click()
  await expect.poll(() => readSettings().statusLine?.command ?? '').not.toContain('statusline.sh')
  expect(existsSync(join(cfgDir(), 'claude-harness', 'statusline.sh'))).toBe(false)
  // The hooks stay; recording can be turned on again from the same menu.
  expect(JSON.stringify(JSON.parse(readFileSync(join(cfgDir(), 'settings.json'), 'utf8')).hooks)).toContain('claude-status.sh')
  await local(page).getByRole('button', { name: 'Host actions for This computer' }).click()
  await expect(page.getByRole('menuitem', { name: 'Record Claude usage…' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Stop recording Claude usage' })).toBeHidden()
})
