import { execFileSync, spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { startCore, type RunningCore } from '../../src/main/core'
import { coreBin, sshtestdBin } from './paths'

/** The in-process SSH server helper (internal/sshconn/sshtest/cmd/sshtestd). */
export interface SshTestd {
  addr: string
  port: number
  hostKeyFingerprint: string
  agentSocket: string
  /** Abruptly close every open client connection. */
  drop(): void
  /** Refuse new connections (existing ones are untouched). */
  block(): void
  unblock(): void
  stop(): Promise<void>
}

export function startSshtestd(home: string, tmuxTmpDir: string): Promise<SshTestd> {
  return new Promise((resolve, reject) => {
    const child: ChildProcessWithoutNullStreams = spawn(sshtestdBin, ['-home', home, '-tmux-tmpdir', tmuxTmpDir], {
      stdio: ['pipe', 'pipe', 'pipe'],
      env: envWithoutTmux()
    })
    let err = ''
    child.stderr.setEncoding('utf8')
    child.stderr.on('data', (c: string) => (err += c))
    child.stdin.on('error', () => {})
    let exited = false
    const exitWaiters: Array<() => void> = []
    child.on('exit', () => {
      exited = true
      exitWaiters.splice(0).forEach((w) => w())
    })
    const timer = setTimeout(() => {
      child.kill()
      reject(new Error('sshtestd did not print its ready line'))
    }, 10_000)
    child.on('error', (e) => reject(e))
    let buf = ''
    child.stdout.setEncoding('utf8')
    child.stdout.on('data', (c: string) => {
      buf += c
      const i = buf.indexOf('\n')
      if (i < 0) return
      clearTimeout(timer)
      try {
        const j = JSON.parse(buf.slice(0, i))
        const send = (l: string): void => void child.stdin.write(l + '\n')
        resolve({
          addr: j.addr,
          port: j.port,
          hostKeyFingerprint: j.hostKeyFingerprint,
          agentSocket: j.agentSocket,
          drop: () => send('drop'),
          block: () => send('block'),
          unblock: () => send('unblock'),
          stop: () =>
            new Promise<void>((res) => {
              if (exited) return res()
              exitWaiters.push(res)
              const k = setTimeout(() => child.kill(), 3000)
              exitWaiters.push(() => clearTimeout(k))
              child.stdin.end()
            })
        })
      } catch (e) {
        child.kill()
        reject(new Error(`bad sshtestd ready line: ${buf.slice(0, 200)} ${err}`))
      }
    })
    child.on('exit', (code) => {
      clearTimeout(timer)
      reject(new Error(`sshtestd exited early (${code}): ${err}`))
    })
  })
}

export interface IsolatedCore {
  core: RunningCore
  /** Fixture HOME of the core (`~` for the local host). */
  home: string
  /** Name of the private tmux socket (-L). */
  tmuxSocket: string
  /** The core's isolated environment. */
  env: Record<string, string>
  ssh?: SshTestd
  /** Remote login name to use in `user@127.0.0.1:port` targets. */
  sshUser: string
  stop(): Promise<void>
}

export interface IsolatedOpts {
  /** Start sshtestd first and give the core its agent socket. */
  ssh?: boolean
  /** Extra directories to create under HOME, e.g. `proj/alpha/.git`. */
  homeDirs?: string[]
}

/**
 * Builds the isolated environment every e2e core runs in: private tmux socket
 * and no tmux config, fresh hosts/state/ssh-config/known_hosts files, a
 * fixture HOME, and either no SSH agent or sshtestd's.
 */
export function isolatedEnv(dir: string, home: string, socket: string, agentSock: string): Record<string, string> {
  const sshConfig = join(dir, 'ssh_config')
  const knownHosts = join(dir, 'known_hosts')
  writeFileSync(sshConfig, '')
  writeFileSync(knownHosts, '')
  return {
    HARNESS_TMUX_SOCKET: socket,
    HARNESS_TMUX_CONFIG: '/dev/null',
    SHELL: '/bin/sh',
    HARNESS_HOSTS_FILE: join(dir, 'hosts.json'),
    HARNESS_STATE_FILE: join(dir, 'state.json'),
    HARNESS_SSH_CONFIG: sshConfig,
    HARNESS_KNOWN_HOSTS: knownHosts,
    SSH_AUTH_SOCK: agentSock,
    HOME: home,
    // Pin Claude Code's config dir to the fixture: if the developer's shell
    // exports CLAUDE_CONFIG_DIR, the hook installer would otherwise write to
    // their real Claude settings.
    CLAUDE_CONFIG_DIR: join(home, '.claude')
  }
}

/**
 * process.env without TMUX/TMUX_PANE. With TMUX set (tests run from inside a
 * tmux session), a tmux command without -L/-S targets the user's own server.
 */
export function envWithoutTmux(extra: Record<string, string> = {}): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env, ...extra }
  delete env.TMUX
  delete env.TMUX_PANE
  return env
}

/**
 * Runs tmux against an explicitly named private server. Refuses to run without
 * -L or -S: a bare "tmux kill-server" here once killed the developer's own
 * tmux session (and the Claude Code session in it).
 */
export function privateTmux(args: string[]): void {
  if (args[0] !== '-L' && args[0] !== '-S') {
    throw new Error(`refusing to run tmux without an explicit -L/-S socket: ${args.join(' ')}`)
  }
  if (!args[1]) throw new Error('empty tmux socket')
  execFileSync('tmux', args, { stdio: 'ignore', env: envWithoutTmux() })
}

/** Like privateTmux, but returns stdout. Same -L/-S requirement. */
export function privateTmuxOutput(args: string[]): string {
  if ((args[0] !== '-L' && args[0] !== '-S') || !args[1]) {
    throw new Error(`refusing to run tmux without an explicit -L/-S socket: ${args.join(' ')}`)
  }
  return execFileSync('tmux', args, { encoding: 'utf8', env: envWithoutTmux() })
}

/** Kills a private tmux server and removes its socket file ($TMUX_TMPDIR or /tmp, tmux-<uid>/<name>). */
export function cleanupTmux(socket: string): void {
  try {
    privateTmux(['-L', socket, 'kill-server'])
  } catch {
    /* no server */
  }
  if (process.getuid) {
    rmSync(join(process.env.TMUX_TMPDIR || '/tmp', `tmux-${process.getuid()}`, socket), { force: true })
  }
}

export interface Isolation {
  /** Fixture HOME (`~` for the local host). */
  home: string
  /** Name of the private tmux socket (-L). */
  tmuxSocket: string
  /** The isolated environment for a core (or an app that spawns one). */
  env: Record<string, string>
  ssh?: SshTestd
  /** Remote login name to use in `user@127.0.0.1:port` targets. */
  sshUser: string
  /** Kills the private tmux servers and removes every file and socket. */
  cleanup(): Promise<void>
}

/** Prepares the fixture HOME, files, private tmux socket and (optionally) sshtestd, without starting a core. */
export async function prepareIsolation(opts: IsolatedOpts = {}): Promise<Isolation> {
  const dir = mkdtempSync(join(tmpdir(), 'harness-e2e-'))
  const home = join(dir, 'home')
  mkdirSync(home, { recursive: true })
  for (const d of opts.homeDirs ?? []) mkdirSync(join(home, d), { recursive: true })
  const tmuxSocket = `harness-e2e-${process.pid}-${randomBytes(4).toString('hex')}`
  // The test SSH host's tmux socket is <sshTmux>/tmux-<uid>/<socket>. Unix
  // socket paths are capped (104 bytes on macOS, 108 on Linux), and macOS's
  // os.tmpdir() (/var/folders/.../T/) alone is ~50, so keep this dir short:
  // under the long tmpdir, tmux couldn't create the server and every
  // new-session on the SSH host failed.
  const sshTmux = process.platform === 'win32' ? join(dir, 'sshtmux') : mkdtempSync('/tmp/hs-')

  let ssh: SshTestd | undefined
  const cleanup = async (): Promise<void> => {
    cleanupTmux(tmuxSocket)
    if (ssh) {
      await ssh.stop().catch(() => {})
      // The "remote" tmux server (run by sshtestd with TMUX_TMPDIR=sshTmux and
      // the core's -L socket) lives at sshTmux/tmux-<uid>/<socket>. Name that
      // exact path with -S; never rely on TMUX_TMPDIR, which tmux ignores when
      // TMUX is set.
      if (process.getuid) {
        try {
          privateTmux(['-S', join(sshTmux, `tmux-${process.getuid()}`, tmuxSocket), 'kill-server'])
        } catch {
          /* no server */
        }
      }
    }
    rmSync(dir, { recursive: true, force: true })
    rmSync(sshTmux, { recursive: true, force: true })
  }

  try {
    if (opts.ssh) {
      const sshHome = join(dir, 'sshhome')
      mkdirSync(sshHome, { recursive: true })
      ssh = await startSshtestd(sshHome, sshTmux)
    }
    const env = isolatedEnv(dir, home, tmuxSocket, ssh?.agentSocket ?? '')
    return { home, tmuxSocket, env, ssh, sshUser: 'tester', cleanup }
  } catch (e) {
    await cleanup()
    throw e
  }
}

export async function startIsolatedCore(opts: IsolatedOpts = {}): Promise<IsolatedCore> {
  const iso = await prepareIsolation(opts)
  try {
    const core = await startCore(coreBin, { env: iso.env })
    return {
      core,
      home: iso.home,
      tmuxSocket: iso.tmuxSocket,
      env: iso.env,
      ssh: iso.ssh,
      sshUser: iso.sshUser,
      stop: async () => {
        await core.stop().catch(() => {})
        await iso.cleanup()
      }
    }
  } catch (e) {
    await iso.cleanup()
    throw e
  }
}
