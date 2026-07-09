// Admin UI 的前端 bundle 入口：Tiptap 編輯器 + 關聯欄位 picker。
// 關鍵：extensions 與轉換服務、Go 白名單共用同一份 schema（schema.mjs），
// 編輯器產生的 doc 一定通過 server 端驗證。
//
// build：npm run build → ../admin/static/admin.js（committed，由 nl-server 內嵌服務）
import { Editor } from '@tiptap/core'
import { extensions } from './schema.mjs'

// ---- Rich text 編輯器 ----
// 容器約定：
//   <div class="rt" data-input="<textarea id>">…</div>
//   textarea 內容為 PM doc JSON（空字串 = 空文件），submit 前保持同步。

const TOOLBAR = [
  { label: 'B', title: '粗體', cmd: (e) => e.chain().focus().toggleBold().run(), active: (e) => e.isActive('bold') },
  { label: 'I', title: '斜體', cmd: (e) => e.chain().focus().toggleItalic().run(), active: (e) => e.isActive('italic') },
  { label: 'S', title: '刪除線', cmd: (e) => e.chain().focus().toggleStrike().run(), active: (e) => e.isActive('strike') },
  { label: 'H2', title: '標題 2', cmd: (e) => e.chain().focus().toggleHeading({ level: 2 }).run(), active: (e) => e.isActive('heading', { level: 2 }) },
  { label: 'H3', title: '標題 3', cmd: (e) => e.chain().focus().toggleHeading({ level: 3 }).run(), active: (e) => e.isActive('heading', { level: 3 }) },
  { label: '••', title: '項目清單', cmd: (e) => e.chain().focus().toggleBulletList().run(), active: (e) => e.isActive('bulletList') },
  { label: '1.', title: '編號清單', cmd: (e) => e.chain().focus().toggleOrderedList().run(), active: (e) => e.isActive('orderedList') },
  { label: '❝', title: '引用', cmd: (e) => e.chain().focus().toggleBlockquote().run(), active: (e) => e.isActive('blockquote') },
  {
    label: '🔗', title: '連結',
    cmd: (e) => {
      const prev = e.getAttributes('link').href || ''
      const url = window.prompt('連結網址（留空移除）', prev)
      if (url === null) return
      if (url === '') e.chain().focus().unsetLink().run()
      else e.chain().focus().setLink({ href: url }).run()
    },
    active: (e) => e.isActive('link'),
  },
  {
    label: '▶', title: 'YouTube',
    cmd: (e) => {
      const url = window.prompt('YouTube 網址')
      if (url) e.chain().focus().setYoutubeVideo({ src: url }).run()
    },
    active: () => false,
  },
]

function initEditor(container) {
  const input = document.getElementById(container.dataset.input)
  let content = null
  try {
    content = input.value.trim() ? JSON.parse(input.value) : null
  } catch {
    content = null
  }

  const bar = document.createElement('div')
  bar.className = 'rt-toolbar'
  const mount = document.createElement('div')
  mount.className = 'rt-body'
  container.append(bar, mount)

  const editor = new Editor({
    element: mount,
    extensions,
    content,
    onUpdate({ editor }) {
      input.value = editor.isEmpty ? '' : JSON.stringify(editor.getJSON())
      refresh()
    },
    onSelectionUpdate: () => refresh(),
  })

  const buttons = TOOLBAR.map((item) => {
    const b = document.createElement('button')
    b.type = 'button'
    b.textContent = item.label
    b.title = item.title
    b.addEventListener('click', () => item.cmd(editor))
    bar.appendChild(b)
    return { b, item }
  })
  function refresh() {
    for (const { b, item } of buttons) b.classList.toggle('on', item.active(editor))
  }
  refresh()
}

// ---- 關聯欄位 picker ----
// 容器約定：
//   <div class="relpicker" data-name="tags" data-list="Tag" data-many="true">
//     <div class="chips"></div>
//     <input type="text" class="relsearch" placeholder="搜尋…">
//   </div>
// 現值以 <input type="hidden" name="tags" value="id"> 存於容器內（可多個），
// 與原本 multi-select 的表單協定相同。

function initPicker(container) {
  const name = container.dataset.name
  const list = container.dataset.list
  const many = container.dataset.many === 'true'
  const chips = container.querySelector('.chips')
  const search = container.querySelector('.relsearch')
  const results = document.createElement('div')
  results.className = 'relresults'
  container.appendChild(results)

  function ids() {
    return [...container.querySelectorAll(`input[name="${name}"]`)].map((i) => i.value)
  }

  function addChip(id, label) {
    if (ids().includes(String(id))) return
    if (!many) removeAll()
    const chip = document.createElement('span')
    chip.className = 'chip'
    chip.innerHTML = `${escapeHTML(label)} <button type="button" aria-label="移除">×</button>`
    const hidden = document.createElement('input')
    hidden.type = 'hidden'
    hidden.name = name
    hidden.value = id
    chip.querySelector('button').addEventListener('click', () => {
      chip.remove()
      hidden.remove()
    })
    chips.appendChild(chip)
    container.appendChild(hidden)
  }

  function removeAll() {
    chips.innerHTML = ''
    container.querySelectorAll(`input[name="${name}"]`).forEach((i) => i.remove())
  }

  // 初始 chips（server 已渲染 data-selected JSON）
  try {
    for (const { id, label } of JSON.parse(container.dataset.selected || '[]')) addChip(id, label)
  } catch { /* noop */ }

  let timer = null
  search.addEventListener('input', () => {
    clearTimeout(timer)
    timer = setTimeout(async () => {
      const q = search.value.trim()
      results.innerHTML = ''
      if (q === '') return
      const resp = await fetch(`/admin/api/options?list=${encodeURIComponent(list)}&q=${encodeURIComponent(q)}`)
      if (!resp.ok) return
      const options = await resp.json()
      for (const opt of options) {
        const row = document.createElement('button')
        row.type = 'button'
        row.className = 'relopt'
        row.textContent = opt.label
        row.addEventListener('click', () => {
          addChip(opt.id, opt.label)
          search.value = ''
          results.innerHTML = ''
        })
        results.appendChild(row)
      }
      if (!options.length) results.innerHTML = '<div class="relempty">（無符合項目）</div>'
    }, 200)
  })
  search.addEventListener('blur', () => setTimeout(() => (results.innerHTML = ''), 200))
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]))
}

export function init() {
  document.querySelectorAll('.rt').forEach(initEditor)
  document.querySelectorAll('.relpicker').forEach(initPicker)
}

init()
