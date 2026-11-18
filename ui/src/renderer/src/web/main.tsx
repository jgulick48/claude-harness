import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createWebBridge, redirectOnAuthLoss } from './webBridge'
import type { WebMe } from './harnessWeb'

function toLogin(): void {
  location.assign('/login?next=' + encodeURIComponent(location.pathname + location.search))
}

async function fetchMe(): Promise<WebMe | null> {
  const res = await fetch('/api/me', { credentials: 'same-origin', headers: { accept: 'application/json' } })
  if (res.status === 401) return null
  if (!res.ok) throw new Error(`/api/me: ${res.status}`)
  return (await res.json()) as WebMe
}

function logout(): void {
  const f = document.createElement('form')
  f.method = 'POST'
  f.action = '/logout'
  f.style.display = 'none'
  document.body.appendChild(f)
  f.submit()
}

async function boot(): Promise<void> {
  let me: WebMe | null
  try {
    me = await fetchMe()
  } catch (e) {
    document.body.textContent = `Cannot reach the server: ${(e as Error).message}`
    return
  }
  if (!me) {
    toLogin()
    return
  }
  window.harness = createWebBridge()
  window.harnessWeb = { me, logout }
  // A socket that closes may mean the session ended; /api/me tells.
  let checking = false
  redirectOnAuthLoss(() => {
    if (checking) return
    checking = true
    fetchMe()
      .then((m) => m === null && toLogin())
      .catch(() => {})
      .finally(() => setTimeout(() => (checking = false), 2000))
  })
  const { App } = await import('../App')
  await Promise.all([
    import('../styles.css'),
    import('../sidebar.css'),
    import('../layout.css'),
    import('../appearance.css')
  ])
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>
  )
}

void boot()
