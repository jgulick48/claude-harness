import { createReadStream, statSync } from 'node:fs'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { extname, join, normalize, sep } from 'node:path'

const types: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
  '.map': 'application/json'
}

export interface StaticServer {
  url: string
  close(): Promise<void>
}

/** Serves `root` over HTTP on 127.0.0.1 (Chromium will not load module scripts from file://). */
export function serveStatic(root: string, headers: Record<string, string> = {}): Promise<StaticServer> {
  const server: Server = createServer((req, res) => {
    const path = decodeURIComponent((req.url ?? '/').split('?')[0])
    let file = normalize(join(root, path === '/' ? 'index.html' : path))
    if (file !== root && !file.startsWith(root + sep)) {
      res.writeHead(403).end()
      return
    }
    try {
      if (statSync(file).isDirectory()) file = join(file, 'index.html')
      statSync(file)
    } catch {
      res.writeHead(404).end('not found')
      return
    }
    res.writeHead(200, { 'content-type': types[extname(file)] ?? 'application/octet-stream', ...headers })
    createReadStream(file).pipe(res)
  })
  return new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address() as AddressInfo
      resolve({
        url: `http://127.0.0.1:${port}/`,
        close: () =>
          new Promise<void>((r) => {
            server.closeAllConnections()
            server.close(() => r())
          })
      })
    })
  })
}
