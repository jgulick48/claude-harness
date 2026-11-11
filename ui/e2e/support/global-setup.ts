import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import { coreBin, repoRoot, sshtestdBin, uiRoot } from './paths'

// Builds everything the e2e suites run against. Set E2E_SKIP_BUILD=1 to reuse
// existing binaries and ui/out.
export default function globalSetup(): void {
  delete process.env.TMUX
  delete process.env.TMUX_PANE
  if (process.env.E2E_SKIP_BUILD === '1') return
  mkdirSync(`${repoRoot}/bin`, { recursive: true })
  const run = (cmd: string, args: string[], cwd: string): void => {
    execFileSync(cmd, args, { cwd, stdio: 'inherit', shell: process.platform === 'win32' })
  }
  run('go', ['build', '-o', coreBin, './cmd/harness-core'], repoRoot)
  run('go', ['build', '-o', sshtestdBin, './internal/sshconn/sshtest/cmd/sshtestd'], repoRoot)
  // Renderer (served over HTTP by the renderer suite) plus main and preload (launched by the electron suite).
  run('npx', ['electron-vite', 'build'], uiRoot)
}
