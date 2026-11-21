import type { TerminalMenuChoice } from '../../../preload/index.d'
import { isHttpLink } from './webPure'

/** Last pointer position, tracked so the menu opens where the right-click happened. */
const last = { x: 0, y: 0 }
let tracking = false
function trackPointer(): void {
  if (tracking) return
  tracking = true
  const set = (e: MouseEvent): void => {
    last.x = e.clientX
    last.y = e.clientY
  }
  window.addEventListener('contextmenu', set, true)
  window.addEventListener('pointerdown', set, true)
  window.addEventListener('pointermove', set, true)
}

/** In-page terminal context menu, styled with the app's ctx-menu classes. */
export function showTerminalMenu(hasSelection: boolean, link?: string | null): Promise<TerminalMenuChoice> {
  trackPointer()
  return new Promise((resolve) => {
    const items: { label: string; choice: TerminalMenuChoice; disabled?: boolean }[] = [
      { label: 'Copy', choice: 'copy', disabled: !hasSelection },
      { label: 'Paste', choice: 'paste' }
    ]
    if (isHttpLink(link)) {
      items.push({ label: 'Open Link', choice: 'openLink' }, { label: 'Copy Link Address', choice: 'copyLink' })
    }
    const menu = document.createElement('div')
    menu.className = 'ctx-menu'
    menu.setAttribute('role', 'menu')
    menu.dataset.testid = 'terminal-menu'
    let done = false
    const finish = (c: TerminalMenuChoice): void => {
      if (done) return
      done = true
      window.removeEventListener('pointerdown', onOutside, true)
      window.removeEventListener('keydown', onKey, true)
      window.removeEventListener('blur', onBlur)
      menu.remove()
      resolve(c)
    }
    const onOutside = (e: Event): void => {
      if (!menu.contains(e.target as Node)) finish(null)
    }
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        finish(null)
      }
    }
    const onBlur = (): void => finish(null)
    for (const it of items) {
      const b = document.createElement('button')
      b.type = 'button'
      b.className = 'ctx-item'
      b.setAttribute('role', 'menuitem')
      b.textContent = it.label
      b.disabled = !!it.disabled
      b.addEventListener('click', () => finish(it.choice))
      menu.appendChild(b)
    }
    document.body.appendChild(menu)
    // Keep it inside the viewport.
    const r = menu.getBoundingClientRect()
    menu.style.left = `${Math.max(0, Math.min(last.x, window.innerWidth - r.width - 4))}px`
    menu.style.top = `${Math.max(0, Math.min(last.y, window.innerHeight - r.height - 4))}px`
    window.addEventListener('pointerdown', onOutside, true)
    window.addEventListener('keydown', onKey, true)
    window.addEventListener('blur', onBlur)
  })
}
