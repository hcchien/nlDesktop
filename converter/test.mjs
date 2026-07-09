// 轉換 smoke test：HTML（含 custom blocks）→ PM JSON → HTML roundtrip。
import assert from 'node:assert'
import { generateHTML, generateJSON } from '@tiptap/html'
import { extensions } from './schema.mjs'

const html = `
<h2>氣候法案三讀</h2>
<p>立法院<strong>今日</strong>三讀通過，<a href="https://e-info.org.tw">詳見報導</a>。</p>
<div data-type="slideshow" data-photo-ids="1,2,3" data-caption="現場照片"></div>
<div data-type="info-box" data-title="背景補充"><p>本法案歷經<em>三年</em>討論。</p></div>
<div data-type="embed" data-html="&lt;iframe src='https://example.com'&gt;&lt;/iframe&gt;"></div>
`

const doc = generateJSON(html, extensions)
assert.equal(doc.type, 'doc')

const types = doc.content.map((n) => n.type)
assert.deepEqual(types, ['heading', 'paragraph', 'slideshow', 'infoBox', 'embed'])

const slideshow = doc.content[2]
assert.deepEqual(slideshow.attrs.photoIds, [1, 2, 3])
assert.equal(slideshow.attrs.caption, '現場照片')

const infoBox = doc.content[3]
assert.equal(infoBox.attrs.title, '背景補充')
assert.equal(infoBox.content[0].type, 'paragraph')

const embed = doc.content[4]
assert.ok(embed.attrs.html.includes('<iframe'))

const out = generateHTML(doc, extensions)
assert.ok(out.includes('data-photo-ids="1,2,3"'))
assert.ok(out.includes('data-title="背景補充"'))

console.log('converter smoke test: OK')
console.log(JSON.stringify(doc.content[2], null, 2))
