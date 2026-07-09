// nl rich text 轉換服務。
//
//	POST /to-json  {"html": "..."} 或 {"markdown": "..."} → {"doc": {...PM JSON}}
//	POST /to-html  {"doc": {...}}                         → {"html": "..."}
//	GET  /healthz
//
// 環境變數：RICHTEXT_PORT（預設 8082）
import http from 'node:http'
import { generateHTML, generateJSON } from '@tiptap/html'
import { marked } from 'marked'
import { extensions } from './schema.mjs'

// Cloud Run 等平台以 PORT 指定；本機開發用 RICHTEXT_PORT（預設 8082）
const port = Number(process.env.PORT || process.env.RICHTEXT_PORT || 8082)

function readBody(req) {
  return new Promise((resolve, reject) => {
    let data = ''
    req.on('data', (c) => {
      data += c
      if (data.length > 10 * 1024 * 1024) reject(new Error('body too large'))
    })
    req.on('end', () => resolve(data))
    req.on('error', reject)
  })
}

const server = http.createServer(async (req, res) => {
  const respond = (code, obj) => {
    res.writeHead(code, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify(obj))
  }
  try {
    // /healthz 在 Cloud Run 的 *.run.app 網域會被 Google Frontend 攔截，
    // 因此以 /health 為主（/healthz 保留給本機相容）
    if (req.method === 'GET' && (req.url === '/health' || req.url === '/healthz')) {
      return respond(200, { ok: true })
    }
    if (req.method !== 'POST') return respond(405, { error: 'method not allowed' })
    const body = JSON.parse((await readBody(req)) || '{}')

    if (req.url === '/to-json') {
      let html = body.html
      if (!html && body.markdown) html = marked.parse(body.markdown)
      if (typeof html !== 'string') return respond(400, { error: 'html or markdown required' })
      return respond(200, { doc: generateJSON(html, extensions) })
    }
    if (req.url === '/to-html') {
      if (!body.doc || typeof body.doc !== 'object') return respond(400, { error: 'doc required' })
      return respond(200, { html: generateHTML(body.doc, extensions) })
    }
    return respond(404, { error: 'not found' })
  } catch (err) {
    return respond(400, { error: String(err && err.message ? err.message : err) })
  }
})

server.listen(port, () => {
  console.log(`nl richtext converter listening on http://localhost:${port}`)
})
