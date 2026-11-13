// OSC 52: a program sets the clipboard by printing ESC ] 52 ; <targets> ;
// <base64> BEL. Claude Code copies its own mouse selections this way: while it
// runs full-screen it has mouse tracking on, so a drag selects inside Claude
// rather than in the terminal, and the copy arrives as OSC 52. tmux's control
// mode passes the sequence through in %output.
//
// Only writes are honored. A "?" payload asks the terminal to send the
// clipboard back to the program; that would let anything printing to the pane
// read the user's clipboard, so it is ignored.

/** Largest clipboard write accepted, in decoded bytes. */
export const OSC52_MAX_BYTES = 1 << 20

/**
 * The text an OSC 52 payload ("<targets>;<base64>") asks to put on the
 * clipboard, or null if it is a query, malformed, or too large. An empty
 * base64 part clears the clipboard ("").
 */
export function osc52Text(data: string): string | null {
  const semi = data.indexOf(';')
  if (semi < 0) return null
  const targets = data.slice(0, semi)
  const b64 = data.slice(semi + 1)
  // Targets: c clipboard, p primary, q secondary, s select, 0-7 cut buffers.
  if (!/^[cpqs0-7]*$/.test(targets)) return null
  if (b64 === '?') return null
  if (b64.length > Math.ceil(OSC52_MAX_BYTES / 3) * 4) return null
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(b64)) return null
  let bin: string
  try {
    bin = atob(b64)
  } catch {
    return null
  }
  const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0))
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return null
  }
}
