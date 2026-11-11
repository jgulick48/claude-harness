import { useEffect, useState } from 'react'
import type { UpdateStatus } from '../../preload/index.d'

const TRANSIENT_MS = 5000

export function UpdateBanner(): React.JSX.Element | null {
  const [status, setStatus] = useState<UpdateStatus>({ state: 'idle' })
  const [hidden, setHidden] = useState(false)

  useEffect(
    () =>
      window.harness.onUpdateStatus((s) => {
        setStatus(s)
        setHidden(false)
      }),
    []
  )

  const transient =
    status.state === 'up-to-date' ||
    status.state === 'error' ||
    (status.state === 'unsupported' && status.manual === true)

  useEffect(() => {
    if (!transient) return
    const t = setTimeout(() => setHidden(true), TRANSIENT_MS)
    return () => clearTimeout(t)
  }, [status, transient])

  if (hidden) return null

  switch (status.state) {
    case 'downloading':
      return (
        <div className="update-banner" role="status">
          <span>
            Downloading update v{status.version}… {status.percent}%
          </span>
        </div>
      )
    case 'available':
      // Windows/Linux download automatically (progress follows); macOS needs the user.
      return (
        <div className="update-banner" role="status">
          <span>{status.manualDownload ? `Update v${status.version} available` : `Update v${status.version} found`}</span>
          {status.manualDownload && (
            <button className="update-button" onClick={() => void window.harness.downloadUpdate()}>
              Download
            </button>
          )}
        </div>
      )
    case 'ready':
      return (
        <div className="update-banner" role="status">
          <span>Update v{status.version} ready</span>
          <button className="update-button" onClick={() => void window.harness.installUpdate()}>
            Restart to install
          </button>
        </div>
      )
    case 'up-to-date':
      return (
        <div className="update-banner muted" role="status">
          Up to date
        </div>
      )
    case 'error':
      return (
        <div className="update-banner error" role="status">
          Update check failed: {status.message}
        </div>
      )
    case 'unsupported':
      return status.manual ? (
        <div className="update-banner muted" role="status">
          Updates are not available in this build
        </div>
      ) : null
    default:
      return null
  }
}
