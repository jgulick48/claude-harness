import { join, resolve } from 'node:path'

export const uiRoot = resolve(__dirname, '../..')
export const repoRoot = resolve(uiRoot, '..')
export const coreBin = join(repoRoot, 'bin', 'harness-core')
export const sshtestdBin = join(repoRoot, 'bin', 'sshtestd')
export const rendererOut = join(uiRoot, 'out', 'renderer')
export const screenshotDir = resolve(uiRoot, 'e2e', 'screenshots')
