import { useEffect } from 'react'
import { AddHostDialog } from './AddHostDialog'
import { AppearanceHost } from './AppearanceHost'
import { CoreProvider, useCore } from './CoreContext'
import { LayoutsMenu } from './LayoutsMenu'
import { LayoutView } from './LayoutView'
import { paneCount } from './layoutTree'
import { NewTerminalDialog } from './NewTerminalDialog'
import { layoutCommand } from './shortcuts'
import { Sidebar } from './Sidebar'

const NOTICE_MS = 4000

function Main(): React.JSX.Element {
  const { state, dispatch } = useCore()

  // Layout shortcuts on the window in the capture phase, so they work while a
  // terminal has focus and never reach it (or the browser, e.g. Cmd+W).
  useEffect(() => {
    const isMac = navigator.platform.toLowerCase().includes('mac')
    const onKey = (e: KeyboardEvent): void => {
      const c = layoutCommand(e, isMac)
      if (!c) return
      e.preventDefault()
      e.stopPropagation()
      if (e.repeat) return
      if (c.type === 'splitRight') dispatch({ type: 'split', direction: 'right' })
      else if (c.type === 'splitDown') dispatch({ type: 'split', direction: 'down' })
      else if (c.type === 'closePane') dispatch({ type: 'closePane' })
      else dispatch({ type: 'focusDirection', dir: c.dir })
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [dispatch])

  useEffect(() => {
    if (!state.notice) return
    const t = setTimeout(() => dispatch({ type: 'dismissNotice' }), NOTICE_MS)
    return () => clearTimeout(t)
  }, [state.notice, dispatch])

  return (
    <div className="app">
      <Sidebar />
      <main className="main-pane layout-main">
        <div className="layout-bar">
          <span className="muted layout-count">
            {paneCount(state.layout) > 1 ? `${paneCount(state.layout)} panes` : ''}
          </span>
          <LayoutsMenu />
        </div>
        <LayoutView />
        {state.notice && (
          <div className="toast" role="status">
            {state.notice}
          </div>
        )}
      </main>
      {state.addHostOpen && <AddHostDialog />}
      {state.dialog && <NewTerminalDialog preferredHost={state.dialog.host} />}
      <AppearanceHost />
    </div>
  )
}

export function App(): React.JSX.Element {
  return (
    <CoreProvider>
      <Main />
    </CoreProvider>
  )
}
