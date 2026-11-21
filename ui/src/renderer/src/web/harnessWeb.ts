/** Web-only extras, present only in the browser build (`window.harnessWeb`). */
export interface WebMe {
  user: string
  method: 'local' | 'oidc'
  sshPublicKey: string
  version: string
}

export interface HarnessWeb {
  me: WebMe
  /** POSTs /logout (clears the session cookie) and lands on /login. */
  logout(): void
}

declare global {
  interface Window {
    harnessWeb?: HarnessWeb
  }
}
