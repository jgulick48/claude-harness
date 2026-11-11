// Never let an inherited tmux session variable point a test at the user's own server.
delete process.env.TMUX
delete process.env.TMUX_PANE

import { resolve } from 'node:path'
import { defineConfig, devices } from '@playwright/test'

const here = __dirname

// Two phases:
//   renderer*  the built renderer in headless Chromium against a real Go core
//   electron   the real Electron app (needs a display: xvfb-run -a on Linux)
export default defineConfig({
  testDir: here,
  outputDir: resolve(here, 'test-results'),
  globalSetup: resolve(here, 'support/global-setup.ts'),
  // Specs in a file share one core and run in order; files run in parallel workers.
  fullyParallel: false,
  workers: process.env.CI ? 2 : undefined,
  retries: 0,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  reporter: [
    ['list'],
    ['html', { outputFolder: resolve(here, 'playwright-report'), open: 'never' }]
  ],
  use: { trace: 'retain-on-failure', screenshot: 'only-on-failure' },
  projects: [
    {
      name: 'renderer',
      testDir: resolve(here, 'renderer'),
      use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 }, colorScheme: 'light' }
    },
    {
      name: 'renderer-dark',
      testDir: resolve(here, 'renderer'),
      use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 }, colorScheme: 'dark' }
    },
    {
      name: 'electron',
      testDir: resolve(here, 'electron'),
      // One Electron instance per file; keep them from fighting over the display.
      workers: 1
    }
  ]
})
