import type { HarnessApi } from '../../../preload/index.d'
import { attentionTitle } from '../claudeStatus'
import { showTerminalMenu } from './terminalMenu'
import { isHttpLink, watchResume, wsUrlFor } from './webPure'

/** The browser implementation of the Electron bridge (see docs/WEB.md section 6). */
export function createWebBridge(): HarnessApi {
  return {
    // The session cookie authenticates /ws; there is no token.
    getCoreInfo: async () => ({
      url: wsUrlFor(location),
      port: Number(location.port) || (location.protocol === 'https:' ? 443 : 80),
      token: '',
      version: '',
      protocol: 0
    }),
    onCoreStatus: (cb) => {
      const t = setTimeout(() => cb({ state: 'ready' }), 0)
      return () => clearTimeout(t)
    },
    onUpdateStatus: (cb) => {
      const t = setTimeout(() => cb({ state: 'unsupported' }), 0)
      return () => clearTimeout(t)
    },
    onSystemResume: (cb) => watchResume(document, window, cb),
    checkForUpdates: async () => {},
    installUpdate: async () => {},
    downloadUpdate: async () => {},
    setAttentionCount: (n) => {
      document.title = attentionTitle(n)
    },
    focusWindow: () => window.focus(),
    readClipboard: async () => {
      try {
        return await navigator.clipboard.readText()
      } catch {
        return '' // insecure context or permission denied
      }
    },
    writeClipboard: async (text) => {
      try {
        await navigator.clipboard.writeText(text)
      } catch {
        /* insecure context or permission denied */
      }
    },
    setThemeSource: () => {},
    showTerminalMenu,
    openLink: async (url) => {
      if (isHttpLink(url)) window.open(url, '_blank', 'noopener')
    }
  }
}

/**
 * Wraps WebSocket so that whenever a socket closes, the session is re-checked;
 * a 401 from /api/me (expired cookie, restarted server) sends the browser to
 * /login. Leaves the core client untouched.
 */
export function redirectOnAuthLoss(check: () => void): void {
  const Native = window.WebSocket
  class Watched extends Native {
    constructor(url: string | URL, protocols?: string | string[]) {
      super(url, protocols)
      this.addEventListener('close', check)
    }
  }
  window.WebSocket = Watched
}
