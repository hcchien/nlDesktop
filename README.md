# nl — AI 時代的 Headless CMS 框架（Go）

Keystone.js 式的宣告式 CMS 框架：定義 lists/fields，框架處理 DB migration、
GraphQL API、三層權限控管，並附**獨立部署的 MCP server** 讓 LLM agent
以與人類使用者完全相同的權限操作資料。

Schema 參考 [e-info CMS](https://github.com/...)（Keystone 6）的核心 lists 實作：
`User`（四種角色）、`Post`、`Author`、`Section`、`Category`、`Tag`、`Photo`。

整體設計與 roadmap 見 [PLAN.md](PLAN.md)。

## 架構

```
schema 宣告（ent/schema/*.go）
   │  go generate ./ent（ent + entgql codegen）
   ▼
ent client / GraphQL schema / Relay connections / WhereInputs
   │
   ├── nl-server：GraphQL API + auth + auto migration（權限唯一關卡）
   │      └── access/：list 層級（interceptor）+ row-level filter + field 層級（hook/middleware）
   └── nl-mcp：MCP server（獨立 binary），經 GraphQL API + API key 操作，不碰 DB
```

## Quickstart

需求：Go 1.26+、PostgreSQL。

```bash
createdb nl_dev
go run ./cmd/nl-server seed     # migration + 範例資料
go run ./cmd/nl-server          # http://localhost:8080（/ 是 playground）
```

Seed 帳號（密碼皆為 `password123`）：

| 帳號 | 角色 | 權限摘要 |
|---|---|---|
| admin@example.com | admin | 全部，含 delete |
| moderator@example.com | moderator | 管理內容與使用者，不可 delete |
| editor@example.com | editor | 只能查詢/修改**自己建立**的 Post |
| contributor@example.com | contributor | 同上，且**不可修改 Post.state**（不能發佈） |

```bash
# 登入取得 token
curl -s localhost:8080/graphql -H 'Content-Type: application/json' \
  -d '{"query":"mutation{login(email:\"editor@example.com\",password:\"password123\"){token}}"}'

# 帶 token 查詢（editor 只會看到自己建立的文章）
curl -s localhost:8080/graphql -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"query":"{posts(first:10){edges{node{id title state}}}}"}'
```

## 權限模型（access/rules.go）

三層規則，全部在 ent 資料層裁決，GraphQL 與 MCP 共用同一關卡：

1. **List operation**：query/create/update/delete × 角色。
2. **Row-level filter**（OwnerScoped）：editor/contributor 對 Post 的 query/update
   自動加上 `createdBy = 自己` 的 WHERE 條件。
3. **Field-level**：
   - 寫入：mutation hook 檢查變更欄位（例：contributor 改 `Post.state` → 拒絕）。
   - 讀取：GraphQL field middleware（例：`User.email` 僅 admin/moderator，本人除外）。

## MCP server

兩種認證模式，權限視圖都等於操作者本人：

**stdio + API key**（本機開發、自動化）：

```bash
# 先在 CMS 簽發 API key：mutation { createApiKey(name: "my-agent") { key } }
NL_API_KEY=nlk_xxx go run ./cmd/nl-mcp
claude mcp add nl-cms --env NL_API_KEY=nlk_xxx -- go run ./cmd/nl-mcp
```

**HTTP + OAuth 2.1**（多人共用的 remote MCP）：

```bash
go run ./cmd/nl-mcp -http :8081    # 不需要 API key
```

使用者從 Claude Desktop / claude.ai 加上 `http://<host>:8081` 這個 remote MCP 時，
client 會依 MCP 授權規格自動走 OAuth：發現 CMS（authorization server）→
dynamic client registration → 跳轉 CMS 登入頁 → PKCE 換 token。
**多人共用同一個 nl-mcp URL，各自登入、權限各自獨立**。CMS 端 endpoints：
`/.well-known/oauth-authorization-server`、`/oauth/register`、`/oauth/authorize`、
`/oauth/token`（v1 未含 refresh token，access token 效期 7 天）。

Tools：`list_schemas`、`describe_list`（含欄位、權限規則、mutation input 慣例）、
`query_items`（filter/排序/cursor 分頁）、`get_item`、`create_item`、`update_item`、
`delete_item`，以及進階用的 `graphql`。

權限語意：巢狀關聯展開跟隨目標 list 的 query 權限，無權限時該欄位優雅降級為
null / 空列表（Keystone 語意）；top-level 查詢無權限則回傳 access denied。

## 增減 lists/fields

schema 的唯一真相來源是 [lists/lists.go](lists/lists.go) 的宣告——欄位、關聯、
三層權限都在同一個宣告裡：

```go
var Post = nl.List("Post",
    nl.Fields{
        "title": field.Text(field.Required()),
        "tags":  field.RelationshipMany("Tag", field.Inverse("posts")),
        ...
    },
    nl.Ops{
        Query:  managers.OwnedBy(RoleEditor, RoleContributor), // row-level filter
        Delete: admins,
        ...
    },
    nl.TrackCreator(),  // createdBy 自動追蹤
    nl.WithFieldAccess(map[string]nl.FieldRule{
        "state": {Update: []string{RoleAdmin, RoleModerator, RoleEditor}},
    }),
)
```

改完宣告後：

```bash
go run ./cmd/nl gen   # 展開 ent schema / GraphQL mutations / resolvers / owner helpers，
                      # 並自動執行 entc 與 gqlgen
```

重啟 `nl-server` 即自動套用 DB migration。權限規則與 MCP metadata 在執行期
直接從宣告導出，不需要另外維護。

## Rich text（Tiptap / ProseMirror）

`field.RichText()` 欄位以 **ProseMirror doc JSON** 存於 JSONB（編輯器中立，
未來換編輯器不換資料），GraphQL 介面為 `JSON` scalar。三個組件：

- **[converter/](converter/)**：Node 轉換服務（`npm start`，port 8082）。
  `HTML/Markdown → PM JSON`、`PM JSON → HTML`。schema（[converter/schema.mjs](converter/schema.mjs)）
  是唯一真相來源，未來 Admin UI 的 Tiptap 編輯器直接共用。
  已含 custom blocks：`slideshow`（輪播，attrs 存 Photo id）、`infoBox`（巢狀富文字）、
  `embed`（嵌入碼）、youtube（官方 extension）。HTML 形態用 `data-type` 慣例：
  `<div data-type="slideshow" data-photo-ids="1,2,3">`。
- **[richtext/](richtext/richtext.go)**（Go）：node/mark 白名單驗證（在 ent mutation hook
  對所有寫入路徑強制執行）、純文字抽取、converter client。
- **MCP ingest**：`create_item`/`update_item` 的 richText 欄位可直接給 HTML 或
  Markdown 字串（agent 丟 Word/GDoc 轉出的 HTML 即可），nl-mcp 自動經 converter
  轉成 PM JSON；也可直接給 doc JSON 物件。`NL_RICHTEXT_URL` 指定 converter 位址。

## Migration（prod）

dev 模式啟動時自動套用 migration；正式環境建議兩段式：

```bash
export NL_AUTO_MIGRATE=false          # server 啟動不再自動改 DB
nl-server migrate plan                 # 印出 pending DDL 供 review（不執行）
nl-server migrate apply                # review 過後套用（非破壞性；不會 drop column/table）
```

刪除欄位/資料表等破壞性變更不會自動執行，需另行處理（Atlas versioned
migration 整合在 roadmap 上）。

## 測試

```bash
createdb nl_test      # 一次性；或由測試自動建立
go test ./...
```

- [e2e/e2e_test.go](e2e/e2e_test.go)：對真實 Postgres 跑完整權限矩陣（三層 × 四角色），
  以及「MCP 操作與 GraphQL 直連權限一致」的驗證（in-memory MCP transport）。
- [nltest/](nltest/nltest.go)：測試 helper（連線、重置 schema、migration、權限掛載），
  框架使用者可重用來測自己的 schema。
- CI（[.github/workflows/ci.yml](.github/workflows/ci.yml)）：Postgres service container，
  驗證 codegen 無 drift + vet + build + 全部測試。

## 專案結構

```
lists/         ★ schema 宣告（唯一真相來源：欄位 + 關聯 + 權限）
nl/            DSL 型別與 registry；nl/field 欄位建構子
codegen/       nl gen 的生成邏輯（DSL → ent schema / graphqls / resolvers / owner helpers）
ent/schema/    生成的 ent schema（+ 手寫 TimeMixin、內部用 ApiKey）
ent/           ent codegen 產出的 typed client（committed）
graph/         GraphQL resolvers（生成的 CRUD + 手寫 auth）
access/        權限裁決引擎（ent interceptor/hook；規則執行期讀 DSL registry）
auth/          argon2id 密碼、HMAC session token、API key
server/        CMS HTTP handler 組裝（auth middleware、field-read middleware）
mcpserver/     MCP server 實作（tools、GraphQL client；metadata 執行期讀 DSL registry）
meta/          schema metadata 導出（MCP 探索用）
nltest/        整合測試 helper
e2e/           權限矩陣 + MCP 一致性整合測試
cmd/nl         框架 CLI（nl gen）
cmd/nl-server  CMS server 執行檔（migration + seed + serve）
cmd/nl-mcp     MCP server 執行檔（獨立部署，stdio / HTTP）
```
