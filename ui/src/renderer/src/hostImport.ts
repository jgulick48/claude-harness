// Pure logic for importing hosts from ~/.ssh/config.

export interface ConfigHost {
  alias: string
  hostName?: string
  user?: string
  port?: number
  added: boolean
}

/** `user@hostName:port`, leaving out whatever is not set. */
export function describeConfigHost(h: ConfigHost): string {
  const host = h.hostName || ''
  const base = h.user ? `${h.user}@${host}` : host
  return h.port ? `${base}:${h.port}` : base
}

export const hasImportable = (list: readonly ConfigHost[]): boolean => list.some((h) => !h.added)

/** Toggle an alias; unknown or already-added aliases are ignored. */
export function toggleAlias(sel: ReadonlySet<string>, alias: string, list: readonly ConfigHost[]): Set<string> {
  const next = new Set(sel)
  const h = list.find((x) => x.alias === alias)
  if (!h || h.added) return next
  if (next.has(alias)) next.delete(alias)
  else next.add(alias)
  return next
}

/** Selected aliases in list order, dropping any that are no longer importable. */
export function selectedAliases(sel: ReadonlySet<string>, list: readonly ConfigHost[]): string[] {
  return list.filter((h) => !h.added && sel.has(h.alias)).map((h) => h.alias)
}

export interface ImportResult {
  alias: string
  error?: string
}

/** Add each alias in turn; a failure does not stop the rest. */
export async function importAliases(
  aliases: readonly string[],
  add: (alias: string) => Promise<unknown>
): Promise<ImportResult[]> {
  const out: ImportResult[] = []
  for (const alias of aliases) {
    try {
      await add(alias)
      out.push({ alias })
    } catch (e) {
      out.push({ alias, error: (e as Error).message || 'failed' })
    }
  }
  return out
}

/** Mark successfully added aliases as added; returns the remaining selection and error map. */
export function applyImportResults(
  list: readonly ConfigHost[],
  sel: ReadonlySet<string>,
  results: readonly ImportResult[]
): { list: ConfigHost[]; selection: Set<string>; errors: Record<string, string> } {
  const ok = new Set(results.filter((r) => !r.error).map((r) => r.alias))
  const errors: Record<string, string> = {}
  for (const r of results) if (r.error) errors[r.alias] = r.error
  const selection = new Set([...sel].filter((a) => !ok.has(a)))
  return { list: list.map((h) => (ok.has(h.alias) ? { ...h, added: true } : h)), selection, errors }
}
