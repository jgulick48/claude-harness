import { expect, runInTerminal, shot, test, useEnv } from '../support/harness'

test.skip(process.platform === 'win32', 'needs tmux')

// sshtestd starts first so the core inherits its agent socket as SSH_AUTH_SOCK.
const env = useEnv({ ssh: true })

test('add an SSH host, verify its key, connect, type, survive a dropped connection', async ({ page }, info) => {
  test.setTimeout(60_000)
  const { ssh, sshUser } = env().core
  if (!ssh) throw new Error('sshtestd missing')
  await expect(page.getByText(/^Core:/)).toHaveText(/connected/)

  await page.getByRole('button', { name: 'Add host', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Add host' })
  await dialog.getByLabel('Target').fill(`${sshUser}@127.0.0.1:${ssh.port}`)
  await dialog.getByLabel('Name (optional)').fill('testbox')
  await dialog.getByRole('button', { name: 'Add host', exact: true }).click()
  await expect(dialog).toBeHidden()

  const box = page.getByTestId('host-group').filter({ hasText: 'testbox' })
  const trust = box.getByRole('group', { name: /Verify host key for testbox/ })
  await expect(trust).toBeVisible()
  // The exact fingerprint sshtestd printed, not merely something fingerprint-shaped.
  await expect(trust).toContainText(ssh.hostKeyFingerprint)
  await shot(page, info, 'trust-panel')

  await trust.getByRole('button', { name: 'Trust and connect' }).click()
  await expect(box.getByLabel('connected', { exact: true })).toBeVisible()

  await box.getByRole('button', { name: 'New terminal on testbox' }).click()
  const nt = page.getByRole('dialog', { name: 'New terminal' })
  await expect(nt.getByRole('combobox', { name: 'Host' }).or(nt.getByLabel('Host'))).toHaveValue(/.+/)
  await nt.getByLabel('Name (optional)').fill('remote-sess')
  await nt.getByRole('button', { name: 'Create' }).click()
  await expect(nt).toBeHidden()
  await expect(box.getByRole('button', { name: /remote-sess/ })).toBeVisible()
  await runInTerminal(page, 'echo remote-$((6*7))', 'remote-42')

  // Cut the connection and keep the server unreachable so the countdown is observable.
  ssh.block()
  ssh.drop()
  await expect(box.getByText(/Reconnecting in \d+s…|Connecting…|refused|EOF|closed|lost/).first()).toBeVisible()
  await expect(box.getByLabel('connected', { exact: true })).toHaveCount(0)
  await expect(box.getByText(/Reconnecting in \d+s…/)).toBeVisible({ timeout: 20_000 })
  await shot(page, info, 'host-reconnecting')

  // Back online: the host and the open terminal recover with no click.
  ssh.unblock()
  await expect(box.getByLabel('connected', { exact: true })).toBeVisible({ timeout: 40_000 })
  await expect(page.getByText(/connection lost/i)).toHaveCount(0, { timeout: 20_000 })
  await runInTerminal(page, 'echo again-$((6*7))', 'again-42')
})
