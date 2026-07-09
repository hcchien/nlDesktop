// nl CMS 的 rich text schema —— 唯一真相來源。
// 轉換服務與（未來的）Admin UI Tiptap 編輯器共用這份 extension 清單；
// Go 端的 node 白名單（richtext package）必須與此保持同步。
//
// Custom nodes 對照 e-info readr draft-editor 的 custom buttons，
// HTML 形態採 data-type 慣例，讓 HTML ingest（Word/GDoc/LLM）也能表達。
import { Node } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import Image from '@tiptap/extension-image'
import Link from '@tiptap/extension-link'
import Subscript from '@tiptap/extension-subscript'
import Superscript from '@tiptap/extension-superscript'
import TextAlign from '@tiptap/extension-text-align'
import Underline from '@tiptap/extension-underline'
import Youtube from '@tiptap/extension-youtube'

// 輪播（e-info: slideshow）。attrs 存 Photo list 的 id。
export const Slideshow = Node.create({
  name: 'slideshow',
  group: 'block',
  atom: true,
  addAttributes() {
    return {
      photoIds: {
        default: [],
        parseHTML: (el) =>
          (el.getAttribute('data-photo-ids') || '')
            .split(',')
            .filter(Boolean)
            .map(Number),
        renderHTML: (attrs) => ({ 'data-photo-ids': (attrs.photoIds || []).join(',') }),
      },
      caption: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-caption') || '',
        renderHTML: (attrs) => (attrs.caption ? { 'data-caption': attrs.caption } : {}),
      },
    }
  },
  parseHTML: () => [{ tag: 'div[data-type="slideshow"]' }],
  renderHTML: ({ HTMLAttributes }) => ['div', { 'data-type': 'slideshow', ...HTMLAttributes }],
})

// 嵌入碼（e-info: embed）。raw HTML 存在 data-html attr（escaped、lossless），
// 渲染端負責 sandbox（iframe）。
export const Embed = Node.create({
  name: 'embed',
  group: 'block',
  atom: true,
  addAttributes() {
    return {
      html: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-html') || '',
        renderHTML: (attrs) => ({ 'data-html': attrs.html }),
      },
      caption: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-caption') || '',
        renderHTML: (attrs) => (attrs.caption ? { 'data-caption': attrs.caption } : {}),
      },
    }
  },
  parseHTML: () => [{ tag: 'div[data-type="embed"]' }],
  renderHTML: ({ HTMLAttributes }) => ['div', { 'data-type': 'embed', ...HTMLAttributes }],
})

// 資訊盒（e-info: info-box）。巢狀 block 內容是 ProseMirror 原生能力。
export const InfoBox = Node.create({
  name: 'infoBox',
  group: 'block',
  content: 'block+',
  addAttributes() {
    return {
      title: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-title') || '',
        renderHTML: (attrs) => (attrs.title ? { 'data-title': attrs.title } : {}),
      },
    }
  },
  parseHTML: () => [{ tag: 'div[data-type="info-box"]' }],
  renderHTML: ({ HTMLAttributes }) => ['div', { 'data-type': 'info-box', ...HTMLAttributes }, 0],
})

// 圖片：官方 Image 加上 Photo list 關聯 id。
const PhotoImage = Image.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      photoId: {
        default: null,
        parseHTML: (el) => {
          const v = el.getAttribute('data-photo-id')
          return v ? Number(v) : null
        },
        renderHTML: (attrs) => (attrs.photoId ? { 'data-photo-id': attrs.photoId } : {}),
      },
    }
  },
})

export const extensions = [
  StarterKit,
  PhotoImage,
  Link.configure({ openOnClick: false }),
  Subscript,
  Superscript,
  TextAlign.configure({ types: ['heading', 'paragraph'] }),
  Underline,
  Youtube.configure({ inline: false }),
  Slideshow,
  Embed,
  InfoBox,
]
