import { EventEmitter } from 'node:events'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { UpdateController, type AutoUpdaterLike, type UpdateState } from './updater'

class FakeUpdater extends EventEmitter {
  autoDownload = true
  autoInstallOnAppQuit = false
  checkForUpdates = vi.fn(() => Promise.resolve(null))
  downloadUpdate = vi.fn(() => Promise.resolve([]))
  quitAndInstall = vi.fn()
}

function make(platform: NodeJS.Platform, supported = true) {
  const updater = new FakeUpdater()
  const openExternal = vi.fn()
  const log = vi.fn()
  const beforeInstall = vi.fn(() => Promise.resolve())
  const onInstallFailed = vi.fn()
  const ctl = new UpdateController({
    updater: updater as unknown as AutoUpdaterLike,
    platform,
    supported,
    openExternal,
    log,
    beforeInstall,
    onInstallFailed
  })
  const states: UpdateState[] = []
  ctl.onState((s) => states.push(s))
  return { updater, ctl, openExternal, log, beforeInstall, onInstallFailed, states }
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('unsupported', () => {
  it('never checks, and a manual check reports unsupported', async () => {
    const { ctl, updater, states } = make('win32', false)
    expect(ctl.state.state).toBe('unsupported')
    ctl.start()
    await vi.advanceTimersByTimeAsync(5 * 60 * 60 * 1000)
    expect(updater.checkForUpdates).not.toHaveBeenCalled()
    await ctl.check({ manual: true })
    expect(updater.checkForUpdates).not.toHaveBeenCalled()
    expect(states.at(-1)).toEqual({ state: 'unsupported', manual: true })
  })
})

describe.each(['win32', 'linux'] as const)('%s', (platform) => {
  it('auto-downloads: available -> downloading -> ready, install only when ready', async () => {
    const { ctl, updater, beforeInstall } = make(platform)
    expect(updater.autoDownload).toBe(true)
    expect(updater.autoInstallOnAppQuit).toBe(true)

    await ctl.install()
    expect(updater.quitAndInstall).not.toHaveBeenCalled()

    await ctl.check({ manual: false })
    expect(ctl.state.state).toBe('checking')
    updater.emit('update-available', { version: '1.2.3' })
    expect(ctl.state).toMatchObject({ state: 'available', version: '1.2.3', manualDownload: false })
    await ctl.install()
    expect(updater.quitAndInstall).not.toHaveBeenCalled()

    updater.emit('download-progress', { percent: 41.6 })
    expect(ctl.state).toEqual({ state: 'downloading', version: '1.2.3', percent: 42 })
    await ctl.install()
    expect(updater.quitAndInstall).not.toHaveBeenCalled()

    updater.emit('update-downloaded', { version: '1.2.3' })
    expect(ctl.state).toEqual({ state: 'ready', version: '1.2.3' })
    await ctl.install()
    expect(beforeInstall).toHaveBeenCalledTimes(1)
    expect(updater.quitAndInstall).toHaveBeenCalledTimes(1)
    expect(beforeInstall.mock.invocationCallOrder[0]).toBeLessThan(updater.quitAndInstall.mock.invocationCallOrder[0])
  })

  it('recovers when the install fails (error event or throw)', async () => {
    const ready = async () => {
      const m = make(platform)
      await m.ctl.check({ manual: false })
      m.updater.emit('update-downloaded', { version: '1.2.3' })
      return m
    }

    const a = await ready()
    a.updater.quitAndInstall.mockImplementation(() => {
      a.updater.emit('error', new Error('installer missing'))
    })
    await a.ctl.install()
    expect(a.onInstallFailed).toHaveBeenCalledTimes(1)
    expect(a.ctl.state).toEqual({ state: 'error', message: 'Update install failed: installer missing' })

    const b = await ready()
    b.updater.quitAndInstall.mockImplementation(() => {
      throw new Error('boom')
    })
    await b.ctl.install()
    expect(b.onInstallFailed).toHaveBeenCalledTimes(1)
    expect(b.ctl.state).toEqual({ state: 'error', message: 'Update install failed: boom' })
  })

  it('openDownload does nothing', async () => {
    const { ctl, updater, openExternal } = make(platform)
    await ctl.check({ manual: false })
    updater.emit('update-available', { version: '1.2.3' })
    ctl.openDownload()
    expect(openExternal).not.toHaveBeenCalled()
  })
})

describe('darwin', () => {
  it('does not auto-download, and opens the release page', async () => {
    const { ctl, updater, openExternal } = make('darwin')
    expect(updater.autoDownload).toBe(false)
    expect(updater.autoInstallOnAppQuit).toBe(false)
    ctl.openDownload() // not available yet
    expect(openExternal).not.toHaveBeenCalled()

    await ctl.check({ manual: false })
    updater.emit('update-available', { version: '1.2.3' })
    expect(ctl.state).toMatchObject({ state: 'available', version: '1.2.3', manualDownload: true })
    ctl.openDownload()
    expect(openExternal).toHaveBeenCalledTimes(1)
    expect(openExternal).toHaveBeenCalledWith('https://github.com/jgulick48/claude-harness/releases/tag/v1.2.3')
    expect(updater.downloadUpdate).not.toHaveBeenCalled()
  })

  it('refuses suspicious versions', async () => {
    const { ctl, updater, openExternal } = make('darwin')
    await ctl.check({ manual: false })
    updater.emit('update-available', { version: '1.0.0/../../evil' })
    ctl.openDownload()
    expect(openExternal).not.toHaveBeenCalled()
  })
})

describe('errors and up-to-date', () => {
  it('does not surface a background error, but logs it', async () => {
    const { ctl, updater, log } = make('linux')
    await ctl.check({ manual: false })
    updater.emit('error', new Error('net::ERR_INTERNET_DISCONNECTED'))
    expect(ctl.state.state).toBe('idle')
    expect(log).toHaveBeenCalledWith(expect.stringContaining('ERR_INTERNET_DISCONNECTED'))
  })

  it('surfaces a manual error', async () => {
    const { ctl, updater } = make('linux')
    await ctl.check({ manual: true })
    updater.emit('error', new Error('boom'))
    expect(ctl.state).toEqual({ state: 'error', message: 'boom' })
  })

  it('surfaces a manual rejection without an error event', async () => {
    const { ctl, updater } = make('linux')
    updater.checkForUpdates.mockRejectedValueOnce(new Error('rejected'))
    await ctl.check({ manual: true })
    expect(ctl.state).toEqual({ state: 'error', message: 'rejected' })
  })

  it('manual check with nothing new -> up-to-date', async () => {
    const { ctl, updater } = make('win32')
    await ctl.check({ manual: true })
    updater.emit('update-not-available', { version: '0.1.0' })
    expect(ctl.state.state).toBe('up-to-date')
  })

  it('background check with nothing new -> idle', async () => {
    const { ctl, updater } = make('win32')
    await ctl.check({ manual: false })
    updater.emit('update-not-available', { version: '0.1.0' })
    expect(ctl.state.state).toBe('idle')
  })
})

describe('scheduling', () => {
  it('checks 10s after start, then every 4h, and stop() clears timers', async () => {
    const { ctl, updater } = make('win32')
    ctl.start()
    await vi.advanceTimersByTimeAsync(9_999)
    expect(updater.checkForUpdates).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(1)
    updater.emit('update-not-available', {})
    await vi.advanceTimersByTimeAsync(4 * 60 * 60 * 1000)
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2)
    updater.emit('update-not-available', {})

    ctl.stop()
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(24 * 60 * 60 * 1000)
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2)
  })

  it('stop() before the startup delay cancels the first check', async () => {
    const { ctl, updater } = make('win32')
    ctl.start()
    ctl.stop()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(updater.checkForUpdates).not.toHaveBeenCalled()
  })
})
