import { AddHostDialog } from './AddHostDialog'
import { CoreProvider, useCore } from './CoreContext'
import { NewTerminalDialog } from './NewTerminalDialog'
import { Sidebar } from './Sidebar'
import { TerminalPane } from './TerminalPane'

function Main(): React.JSX.Element {
  const { state } = useCore()
  const { active } = state
  return (
    <div className="app">
      <Sidebar />
      <main className="main-pane">
        {active ? (
          <TerminalPane
            key={`${active.host}/${active.session}`}
            host={active.host}
            sessionId={active.session}
            hostName={state.hosts.find((h) => h.id === active.host)?.name ?? active.host}
            hostConnected={state.hosts.find((h) => h.id === active.host)?.state === 'connected'}
            session={state.sessions[active.host]?.find((s) => s.id === active.session)}
          />
        ) : (
          <p className="muted">Select or create a session</p>
        )}
      </main>
      {state.addHostOpen && <AddHostDialog />}
      {state.dialog && <NewTerminalDialog preferredHost={state.dialog.host} />}
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
