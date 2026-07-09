package admin

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/hcchien/nl/meta"
)

var funcs = template.FuncMap{
	"cell": func(row map[string]any, key string) string {
		v, ok := row[key]
		if !ok || v == nil {
			return ""
		}
		switch tv := v.(type) {
		case string:
			if len(tv) > 60 {
				return tv[:60] + "…"
			}
			return tv
		default:
			return fmt.Sprintf("%v", v)
		}
	},
}

var pageTmpl = template.Must(template.New("page").Funcs(funcs).Parse(`<!DOCTYPE html>
<html lang="zh-Hant"><head><meta charset="utf-8"><title>{{.Title}} — nl CMS</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
:root{--fg:#1a1a1a;--muted:#6b7280;--line:#e5e7eb;--bg:#f8f8f7;--accent:#1a1a1a}
*{box-sizing:border-box}body{margin:0;font-family:system-ui,sans-serif;color:var(--fg);background:var(--bg)}
.layout{display:flex;min-height:100vh}
nav{width:200px;background:#fff;border-right:1px solid var(--line);padding:1rem}
nav h1{font-size:1rem;margin:0 0 1rem}
nav a{display:block;padding:.4rem .6rem;border-radius:6px;color:var(--fg);text-decoration:none;font-size:.9rem}
nav a:hover{background:var(--bg)}
nav .who{margin-top:1.5rem;font-size:.75rem;color:var(--muted)}
main{flex:1;padding:1.5rem;max-width:960px}
h2{font-size:1.2rem;margin:0 0 1rem}
table{width:100%;border-collapse:collapse;background:#fff;border:1px solid var(--line);border-radius:8px}
th,td{text-align:left;padding:.5rem .75rem;border-bottom:1px solid var(--line);font-size:.85rem}
th{color:var(--muted);font-weight:500}
a{color:inherit}
.btn{display:inline-block;padding:.45rem .9rem;background:var(--accent);color:#fff;border:0;border-radius:6px;font-size:.85rem;text-decoration:none;cursor:pointer}
.btn.danger{background:#b91c1c}
.toolbar{display:flex;justify-content:space-between;align-items:center;margin-bottom:1rem}
form.item label{display:block;font-size:.8rem;color:var(--muted);margin:.9rem 0 .25rem}
form.item input[type=text],form.item input[type=number],form.item input[type=password],
form.item input[type=datetime-local],form.item select,form.item textarea{
  width:100%;padding:.5rem;border:1px solid #ccc;border-radius:6px;font:inherit;background:#fff}
form.item textarea{min-height:8rem;font-family:ui-monospace,monospace;font-size:.8rem}
.err{background:#fef2f2;color:#b91c1c;padding:.6rem .9rem;border-radius:6px;margin-bottom:1rem;font-size:.85rem}
.note{font-size:.75rem;color:var(--muted)}
.count{font-size:.8rem;color:var(--muted)}
/* rich text 編輯器 */
.rt{border:1px solid #ccc;border-radius:6px;background:#fff}
.rt-toolbar{display:flex;gap:.15rem;padding:.35rem;border-bottom:1px solid var(--line);flex-wrap:wrap}
.rt-toolbar button{border:0;background:transparent;border-radius:4px;padding:.25rem .55rem;cursor:pointer;font-size:.85rem;min-width:2rem}
.rt-toolbar button:hover{background:var(--bg)}
.rt-toolbar button.on{background:#e8e8e6;font-weight:600}
.rt-body .ProseMirror{padding:.75rem;min-height:10rem;outline:none;font-size:.95rem;line-height:1.7}
.rt-body .ProseMirror p{margin:.4rem 0}
.rt-body .ProseMirror h2,.rt-body .ProseMirror h3{margin:.8rem 0 .3rem}
.rt-body .ProseMirror blockquote{border-left:3px solid var(--line);margin:.5rem 0;padding-left:.75rem;color:var(--muted)}
.rt-body .ProseMirror [data-type]{border:1px dashed #bbb;border-radius:6px;padding:.5rem;margin:.5rem 0;color:var(--muted);font-size:.8rem}
.rt-body iframe{max-width:100%}
/* 關聯 picker */
.relpicker{position:relative;border:1px solid #ccc;border-radius:6px;background:#fff;padding:.35rem}
.relpicker .chips{display:flex;gap:.3rem;flex-wrap:wrap}
.relpicker .chip{background:#eef1f4;border-radius:99px;padding:.15rem .3rem .15rem .7rem;font-size:.8rem;display:inline-flex;align-items:center;gap:.15rem}
.relpicker .chip button{border:0;background:transparent;cursor:pointer;color:var(--muted);font-size:.9rem}
.relpicker .relsearch{border:0;outline:none;padding:.3rem;font:inherit;font-size:.85rem;width:14rem;background:transparent}
.relresults{position:absolute;left:0;right:0;top:100%;z-index:10;background:#fff;border:1px solid var(--line);border-radius:6px;box-shadow:0 4px 12px rgba(0,0,0,.1);max-height:14rem;overflow:auto}
.relopt{display:block;width:100%;text-align:left;border:0;background:#fff;padding:.45rem .7rem;cursor:pointer;font-size:.85rem}
.relopt:hover{background:var(--bg)}
.relempty{padding:.45rem .7rem;color:var(--muted);font-size:.8rem}
</style></head><body>
<div class="layout">
<nav>
  <h1><a href="/admin" style="text-decoration:none">nl CMS</a></h1>
  {{range .Nav}}<a href="/admin/l/{{.Name}}">{{.Name}}</a>{{end}}
  <div class="who">{{.User}}（{{.Role}}）<br><a href="/admin/logout">登出</a></div>
</nav>
<main>{{.Body}}</main>
</div>
<script src="/admin/static/admin.js" defer></script>
</body></html>`))

var loginTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="zh-Hant"><head><meta charset="utf-8"><title>登入 — nl CMS</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body{font-family:system-ui,sans-serif;display:flex;justify-content:center;padding-top:10vh;background:#f8f8f7}
form{background:#fff;padding:2rem;border-radius:12px;box-shadow:0 1px 8px rgba(0,0,0,.08);width:320px}
h1{font-size:1.1rem;margin:0 0 1.25rem}label{display:block;font-size:.8rem;margin-bottom:.25rem;color:#444}
input{width:100%;box-sizing:border-box;padding:.5rem;margin-bottom:1rem;border:1px solid #ccc;border-radius:6px}
button{width:100%;padding:.6rem;background:#1a1a1a;color:#fff;border:0;border-radius:6px;cursor:pointer}
.err{color:#b91c1c;font-size:.85rem;margin-bottom:1rem}</style></head><body>
<form method="post" action="/admin/login">
  <h1>nl CMS 管理後台</h1>
  {{if .}}<div class="err">{{.}}</div>{{end}}
  <label>Email</label><input type="email" name="email" required autofocus>
  <label>密碼</label><input type="password" name="password" required>
  <button type="submit">登入</button>
</form></body></html>`))

func renderLogin(w http.ResponseWriter, errMsg string, code int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	loginTmpl.Execute(w, errMsg)
}

func renderPage(w http.ResponseWriter, title string, s *session, nav []meta.List, body template.HTML) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pageTmpl.Execute(w, map[string]any{
		"Title": title, "User": s.name, "Role": s.role, "Nav": nav, "Body": body,
	})
}

// ---- page bodies ----

var homeBodyTmpl = template.Must(template.New("home").Parse(`
<h2>Lists</h2>
<table><tr><th>List</th><th>說明</th></tr>
{{range .}}<tr><td><a href="/admin/l/{{.Name}}">{{.Name}}</a></td><td>{{.Description}}</td></tr>{{end}}
</table>`))

func homeBody(lists []meta.List) template.HTML {
	var b strings.Builder
	homeBodyTmpl.Execute(&b, lists)
	return template.HTML(b.String())
}

var listBodyTmpl = template.Must(template.New("list").Funcs(funcs).Parse(`
<div class="toolbar">
  <h2>{{.List.Name}} <span class="count">共 {{.Total}} 筆</span></h2>
  <a class="btn" href="/admin/l/{{.List.Name}}/new">＋ 新增</a>
</div>
<table>
<tr><th>id</th><th>{{.List.LabelField}}</th>{{range .Extra}}<th>{{.}}</th>{{end}}</tr>
{{$l := .List}}{{$extra := .Extra}}
{{range $row := .Rows}}<tr>
  <td>{{cell $row "id"}}</td>
  <td><a href="/admin/l/{{$l.Name}}/{{cell $row "id"}}">{{cell $row $l.LabelField}}</a></td>
  {{range $extra}}<td>{{cell $row .}}</td>{{end}}
</tr>{{end}}
</table>
{{if .Next}}<p><a class="btn" href="/admin/l/{{.List.Name}}?after={{.Next}}">下一頁 →</a></p>{{end}}`))

// extraColumns 挑幾個有代表性的欄位當表格欄。
func extraColumns(l *meta.List) []string {
	var out []string
	for _, f := range l.Fields {
		if f.Type == "select" || f.Name == "updatedAt" {
			out = append(out, f.Name)
		}
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func listBody(l *meta.List, rows []map[string]any, total int, next string) template.HTML {
	var b strings.Builder
	listBodyTmpl.Execute(&b, map[string]any{
		"List": l, "Rows": rows, "Total": total, "Next": next, "Extra": extraColumns(l),
	})
	return template.HTML(b.String())
}

var formBodyTmpl = template.Must(template.New("form").Parse(`
<div class="toolbar">
  <h2>{{if .ID}}{{.List.Name}} #{{.ID}}{{else}}新增 {{.List.Name}}{{end}}</h2>
  {{if .ID}}<form method="post" action="/admin/l/{{.List.Name}}/{{.ID}}/delete"
    onsubmit="return confirm('確定刪除？')"><button class="btn danger">刪除</button></form>{{end}}
</div>
{{if .Err}}<div class="err">{{.Err}}</div>{{end}}
<form class="item" method="post">
{{range .Fields}}
  <label>{{.Name}}{{if .Required}} *{{end}}{{if .Note}} <span class="note">（{{.Note}}）</span>{{end}}</label>
  {{if eq .Type "boolean"}}
    <input type="checkbox" name="{{.Name}}" {{if .Checked}}checked{{end}}>
  {{else if eq .Type "integer"}}
    <input type="number" name="{{.Name}}" value="{{.Value}}">
  {{else if eq .Type "timestamp"}}
    <input type="datetime-local" name="{{.Name}}" value="{{.Value}}">
  {{else if eq .Type "select"}}
    <select name="{{.Name}}">{{$v := .Value}}{{if not .Required}}<option value=""></option>{{end}}
      {{range .Enum}}<option value="{{.}}" {{if eq . $v}}selected{{end}}>{{.}}</option>{{end}}
    </select>
  {{else if eq .Type "richText"}}
    <textarea id="rt-{{.Name}}" name="{{.Name}}" hidden>{{.JSON}}</textarea>
    <div class="rt" data-input="rt-{{.Name}}"></div>
  {{else if eq .Type "password"}}
    <input type="password" name="{{.Name}}" placeholder="{{if $.ID}}留空表示不變更{{end}}">
  {{else if eq .Type "relationship"}}
    {{if .Denied}}<div class="note">（無權瀏覽 {{.Ref}}，此欄位唯讀）</div>
    {{else}}
    <input type="hidden" name="_present_{{.Name}}" value="1">
    <div class="relpicker" data-name="{{.Name}}" data-list="{{.Ref}}" data-many="{{.Many}}" data-selected="{{.SelectedJSON}}">
      <div class="chips"></div>
      <input type="text" class="relsearch" placeholder="搜尋 {{.Ref}}…" autocomplete="off">
    </div>
    {{end}}
  {{else}}
    <input type="text" name="{{.Name}}" value="{{.Value}}">
  {{end}}
{{end}}
  <p><button class="btn" type="submit">儲存</button> <a href="/admin/l/{{.List.Name}}">返回列表</a></p>
</form>`))

func formBody(l *meta.List, id string, fields []formField, errMsg string) template.HTML {
	var b strings.Builder
	formBodyTmpl.Execute(&b, map[string]any{"List": l, "ID": id, "Fields": fields, "Err": errMsg})
	return template.HTML(b.String())
}

var errorBodyTmpl = template.Must(template.New("err").Parse(
	`{{range .}}<div class="err">{{.}}</div>{{end}}`))

func errorBody(errs []string) template.HTML {
	var b strings.Builder
	errorBodyTmpl.Execute(&b, errs)
	return template.HTML(b.String())
}
