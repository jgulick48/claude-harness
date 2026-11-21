import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { uiRoot } from '../support/paths'
import { serveStatic, type StaticServer } from '../support/staticServer'

// The browser build of the UI (npm run build:web) with /api/me mocked; no core.
const KEY = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForTests harness@server'
let server: StaticServer

test.beforeAll(async () => {
  server = await serveStatic(join(uiRoot, 'out', 'web'))
})
test.afterAll(async () => {
  await server.close()
})

test('shows the signed-in user, Sign out and the server SSH key', async ({ page }) => {
  await page.route('**/api/me', (r) =>
    r.fulfill({ json: { user: 'alice', method: 'local', sshPublicKey: KEY, version: 'test' } })
  )
  await page.goto(server.url)
  await page.getByRole('button', { name: 'Settings' }).click()
  await expect(page.getByTestId('web-user')).toHaveText('Signed in as alice')
  await expect(page.getByRole('menuitem', { name: 'Sign out' })).toBeVisible()
  await page.getByRole('menuitem', { name: 'Server SSH key…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Server SSH key' })
  await expect(dialog.getByTestId('server-key')).toHaveValue(KEY)
  await expect(dialog.getByTestId('server-key-cmd')).toHaveValue(`echo '${KEY}' >> ~/.ssh/authorized_keys`)
})

test('sends the browser to /login on 401', async ({ page }) => {
  await page.route('**/api/me', (r) => r.fulfill({ status: 401, json: { error: 'unauthorized' } }))
  await page.goto(server.url)
  await page.waitForURL('**/login?next=%2F')
})
