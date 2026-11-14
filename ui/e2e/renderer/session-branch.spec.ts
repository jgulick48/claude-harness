import { execFileSync } from 'node:child_process'
import { mkdtempSync, realpathSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect, shot, test, useEnv } from '../support/harness'
import { privateTmux } from '../support/isolated'

// tmux has no Windows build; these scenarios drive a real local tmux.
test.skip(process.platform === 'win32', 'needs tmux')

const env = useEnv()

const local = (page: import('@playwright/test').Page) =>
  page.getByTestId('host-group').filter({ hasText: 'This computer' })

/** tmux against the core's private server only (never the user's). */
const tmux = (...args: string[]): void => privateTmux(['-L', env().core.tmuxSocket, '-f', '/dev/null', ...args])

test('the row shows the git branch of the session directory; the filter matches it', async ({ page }, info) => {
  try {
    execFileSync('git', ['--version'], { stdio: 'ignore' })
  } catch {
    test.skip(true, 'needs git')
  }
  const repo = realpathSync(mkdtempSync(join(tmpdir(), 'harness-branch-')))
  // symbolic-ref works without a commit, so no user config is needed.
  execFileSync('git', ['-C', repo, 'init', '-q'])
  execFileSync('git', ['-C', repo, 'symbolic-ref', 'HEAD', 'refs/heads/feat/e2e'])
  const plain = realpathSync(mkdtempSync(join(tmpdir(), 'harness-nobranch-')))
  for (const [name, dir] of [
    ['br-repo', repo],
    ['br-plain', plain]
  ]) {
    tmux('new-session', '-d', '-s', name, '-c', dir)
    tmux('set-option', '-t', name, '@harness_name', name)
    tmux('send-keys', '-t', name, 'exec sleep 600', 'Enter')
  }

  const row = local(page).locator('[data-row]').filter({ hasText: 'br-repo' })
  await expect(row).toBeVisible()
  await expect(row.getByTestId('session-branch')).toHaveText('feat/e2e', { timeout: 15_000 })
  await expect(row.getByTestId('session-branch')).toHaveAttribute('title', 'feat/e2e')
  await expect(local(page).locator('[data-row]').filter({ hasText: 'br-plain' }).getByTestId('session-branch')).toHaveCount(0)
  await shot(page, info, 'session-branch')

  const filter = page.getByRole('textbox', { name: 'Filter sessions' })
  await filter.fill('feat/e2e')
  await expect(row).toBeVisible()
  await expect(local(page).locator('[data-row]').filter({ hasText: 'br-plain' })).toHaveCount(0)
  await filter.fill('')
})
