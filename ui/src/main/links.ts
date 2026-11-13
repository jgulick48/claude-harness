// Links from the terminal open in the user's browser. Only web links: a
// terminal program controls the URL (OSC 8 hyperlinks can show any text for
// any target), so file:, javascript:, custom app schemes and so on never
// reach shell.openExternal.

/** The URL to open for a terminal link, or null if it isn't an http(s) URL. */
export function externalLink(uri: unknown): string | null {
  if (typeof uri !== 'string' || uri.length > 8192) return null
  let u: URL
  try {
    u = new URL(uri)
  } catch {
    return null
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
  if (!u.hostname) return null
  return u.href
}
