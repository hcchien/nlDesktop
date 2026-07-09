# 專案計畫：Go 版 Keystone.js 式 Headless CMS 框架

> 狀態：v0.6（2026-07-09）—— 垂直切片 + Phase 1 + rich text + **MCP OAuth 2.1** 完成
>（見 README.md）：7 個 lists、GraphQL API、三層權限、nl DSL + codegen、
> migrate plan/apply、Tiptap rich text（converter + 白名單 + MCP ingest）、
> 整合測試 + CI，以及 **OAuth 2.1**：CMS 兼任 authorization server
>（RFC 8414 metadata、RFC 7591 DCR、PKCE S256、登入頁），nl-mcp 為 resource server
>（RFC 9728 protected-resource metadata、401 挑戰、token 逐請求轉發）。
> e2e 驗證「兩個不同權限使用者連同一 nl-mcp、各自登入、權限視圖不同」。
>
> v0.7（2026-07-09）新增：
> - **Versioned migration**：`migrate diff <name>`（ent NamedDiff/Atlas 引擎 →
>   golang-migrate 格式 up/down SQL 檔）+ `migrate up`（內嵌 applier，
>   schema_migrations 記錄）。SQL 檔進 git 接受 review，可手動編輯（資料遷移、
>   USING 轉型等 diff 推不出來的操作）。
> - **OAuth refresh token**：rotation（使用即換發，重放必失敗），access token 縮至
>   1 小時、refresh 30 天。
> - **Admin UI v1**：/admin，server-rendered + schema-driven（表單由 meta 生成），
>   所有操作經 in-process GraphQL 帶登入者 token —— 權限與 API/MCP 同一關卡。
>   richText 暫以 JSON textarea 呈現；內嵌 Tiptap、CSRF token 列後續。
>
> 設計註記：
> - 巢狀關聯展開跟隨目標 list 權限，無權限時降級 null/空（Keystone 語意）；
>   top-level 查詢才報 access denied。
> - Relay `node(id)` 需要 global-unique-ID（ent_types 表），本框架採 per-table id，
>   單筆查詢一律用 `<list>(where:{id})`；node/nodes 欄位保留但不使用。
> 下一步：draft.js 遷移腳本、Admin UI 內嵌 Tiptap、token revocation UI。
> 目標：一個以 Go 撰寫的 headless CMS / admin framework，開發者用宣告式的方式定義
> lists 與 fields，框架透過 **codegen** 產生型別安全的程式碼，自動處理 DB migration、
> GraphQL API、權限控管，並提供**獨立部署的 MCP server**。

> v0.2 變更（依討論決議）：
> 1. Schema 改採 **CodeGen**（棄 runtime schema）→ 連動改為 ent + gqlgen 技術棧
> 2. API 改為 **GraphQL-first**（對 MCP 的資料 fetch 更適合）
> 3. MCP server **獨立部署**
> 4. MCP 後端**只接 CMS 的 GraphQL API**，不直接碰 DB
> 5. Agent UI 列為 Phase 8 選配（現成 MCP clients 已可作為操作介面）

---

## 1. 需求整理

### 功能性需求

1. **宣告式 Schema**：以 list（對應資料表）+ field（對應欄位）宣告資料模型；
   增減成本低：改宣告 → `nl generate`（dev mode 由 watcher 自動觸發）→ 重啟即生效。
2. **自動 Migration**：比對宣告 schema 與 DB 實際結構，dev 自動套用；
   prod 走 plan/apply 兩段式確認。
3. **權限控管（Access Control）**：
   - List 層級：query / create / update / delete 各操作獨立控制。
   - Field 層級：read / create / update 各操作獨立控制。
   - 支援靜態規則（role-based）與動態規則（row-level filter，例如「作者只能改自己的文章」）。
4. **完整 API**：GraphQL（CRUD、filter、排序、分頁、關聯、introspection）；
   REST/OpenAPI 列為後續選配。
5. **MCP Server**：獨立部署的 service，讓 LLM agent 探索 schema 並操作資料；
   透過 CMS API 存取，**權限與一般使用者完全一致**。
6. **自動化 Integration Test**：以真實 PostgreSQL 跑 integration test，CI 自動執行。

### 非功能性需求

- **效能**：Go + codegen（無 reflection/map 開銷）、單一 binary 部署。
- **DB**：預設 PostgreSQL；storage 抽象由 ent dialect 層提供。
- **安全**：權限檢查集中在 CMS API 這一個關卡，MCP 與未來任何 client 都無法繞過。

### 刻意排除（v1 不做）

- Admin UI、REST/OpenAPI（Phase 8）
- 多租戶（multi-tenancy）、水平分片

---

## 2. 關鍵技術決策與取捨

### 2.1 語言：Go ✅（不變）

I/O bound workload，Go 效能綽綽有餘，開發維護成本低，codegen 生態成熟。

### 2.2 Schema：CodeGen（決議），基底採 ent

**決議**：採 codegen，換取型別安全與極致效能；增減 field 需要 generate + rebuild，
以 `nl dev` watch mode（改 schema → 自動 generate → rebuild → auto migrate）
把迭代循環壓到數秒，保住「快速增減」的體驗。

**基底選 [ent](https://entgo.io)** 而非全自製 codegen：

| | ent（採用） | 全自製 codegen |
|---|---|---|
| Schema DSL | schema-as-code，Go 定義 | 要自己設計 |
| Codegen | 成熟（typed client、hooks、interceptors） | 全部自己寫 |
| Migration | 內建（Atlas 引擎，支援 plan/apply） | 全部自己寫 |
| GraphQL | entgql 直接生成 gqlgen schema/resolvers | 全部自己寫 |
| 權限 | privacy policy 覆蓋 list 層級 | 全部自己寫 |
| 風險 | 依賴上游、field-level access 需自建 | 工程量 3–5 倍 |

框架（暫名 `nl`）= ent 之上的一層：更高階的 list/field DSL、field-level access、
auth/session、CLI、MCP server、慣例與腳手架。開發者體驗示意：

```go
var Post = nl.List("Post",
    nl.Fields{
        "title":       field.Text().Required().MaxLen(200),
        "status":      field.Select("draft", "published").Default("draft"),
        "content":     field.JSON(),
        "author":      field.Relationship("User").ManyToOne(),
        "publishedAt": field.Timestamp().Optional(),
    },
    nl.Access{
        Query:  access.Anyone(),
        Create: access.Role("editor"),
        Update: access.OwnRows("author"),        // row-level：只能改自己的
        Delete: access.Role("admin"),
    },
    nl.FieldAccess{
        "status": {Update: access.Role("editor")},
    },
)
```

`nl generate` 將上述 DSL 轉為 ent schema → 觸發 ent/entgql codegen →
產出 typed client、GraphQL schema/resolvers、權限中介層、MCP 用的 schema metadata。

### 2.3 API：GraphQL-first（決議）✅

改採 GraphQL 為主要 API，理由：

1. **對 MCP/agent 的資料 fetch 更適合**：selection set 讓 agent 只取需要的欄位
   （省 token）、introspection 自我描述、關聯查詢單次來回。
2. codegen 決議使 gqlgen 成為可行且自然的選擇（原 runtime schema 方案做動態
   GraphQL 在 Go 生態不成熟，這個顧慮已消失）。
3. 與 Keystone 對齊（GraphQL-first），生態工具（client codegen、playground）齊全。

entgql 自動生成：per-list 的 query（含 Relay-style connection 分頁、filter、
orderBy）與 mutation（create/update/delete），加上 auth mutations。
REST/OpenAPI 若日後需要，可用 entoas 生成（Phase 8）。

### 2.4 Migration：Atlas 引擎，Terraform 式 plan/apply（不變，改用現成引擎）

- **dev**：`nl dev` 啟動時自動 diff & 套用非破壞性變更；破壞性變更（drop、縮型別）警告並要求 flag。
- **prod**：`nl migrate plan`（輸出 SQL 供 review、可存入 git）→ `nl migrate apply`。
- 多 instance 併發以 advisory lock 防護；migration 歷史記錄於專用表。

### 2.5 權限控管：集中在 CMS API 單一關卡（不變，實作對應到 ent）

- **List 層級**：以 ent privacy policy 實作 `bool | Filter` 規則；
  filter 規則 merge 進查詢 WHERE（row-level security 的應用層實作）。
- **Field 層級**（自建層）：
  - read 不允許 → GraphQL resolver 層剔除欄位（回 null + error extension）。
  - create/update 不允許 → mutation input 驗證階段拒絕。
- **Session**：auth middleware 建立（user id、roles、claims），注入 context，
  一路傳到 privacy policy 與 field access 判斷。
- 所有 client（MCP、未來 Admin UI、第三方）都走 GraphQL API，**無第二條資料路徑**。

### 2.6 MCP Server：獨立部署、只接 CMS GraphQL API（決議）

```
MCP clients（Claude Desktop / Claude Code / claude.ai / 自建 agent UI）
        │  MCP protocol（stdio 或 streamable HTTP）
        ▼
┌─────────────────────┐
│   nl-mcp（獨立 binary）│   ← 獨立部署、獨立擴縮
│   GraphQL client      │
└──────────┬──────────┘
           │  GraphQL over HTTP + API key（綁定特定 user/role）
           ▼
      CMS GraphQL API（權限唯一關卡）
```

- **為何接 API 而非直連 DB**：權限關卡維持唯一（MCP 無法繞過）、部署解耦
  （MCP 可獨立升級/擴縮/放不同網段）、契約明確（GraphQL schema 即 contract）。
  代價是多一個網路 hop，可忽略。
- **使用者身分與認證**（關鍵：MCP 上的每個請求都必須對應到一個 CMS 使用者，
  權限才能沿用 CMS 的 authorization）：
  1. **OAuth 2.1（remote 連線的主要方式，MCP spec 標準）**：Claude Desktop /
     claude.ai connectors 對 remote MCP server 內建 OAuth 流程——使用者按「連接」
     → 跳轉 CMS 登入頁 → 核發綁定該使用者的 access token → nl-mcp 之後每個請求
     都以該使用者身分呼叫 CMS API。**多人共用同一個 nl-mcp URL，各自登入、
     權限各自獨立**。CMS 兼任 authorization server（authorize/token endpoint、
     PKCE、dynamic client registration），nl-mcp 為 resource server（驗 token）。
  2. **API key**（stdio 本機開發、service account / 自動化）：使用者登入 CMS 後
     簽發個人 key，綁定其身分與權限，可個別 revoke。屬長效憑證，建議僅限
     開發與 s2s 場景。
- **啟動時**以 introspection + `nl generate` 產出的 metadata 建立 tools。

Tools 設計（結構化便利 tools + 原生 GraphQL tool 並存）：

| Tool | 說明 |
|---|---|
| `list_schemas` / `describe_list` | schema 探索（依該 API key 的權限過濾） |
| `query_items` / `get_item` | 結構化參數（list、filter、orderBy、fields、limit），內部編譯成 GraphQL query |
| `create_item` / `update_item` / `delete_item` | 結構化 mutation |
| `graphql` | 進階：直接執行 GraphQL query/mutation（模型寫錯時回傳可自我修正的驗證錯誤） |

Resources：`schema://{list}` 提供各 list 的 schema 文件供 agent 快取。

### 2.7 Agent UI：v1 不做，列為選配（決議）

MCP 的操作介面由現成 MCP clients 承擔（Claude Desktop、Claude Code、
claude.ai connectors），接上 `nl-mcp` 即可用，v1 不需要自建 UI。

需要自建的時機：想讓 **CMS 終端使用者在產品內**用聊天介面操作資料
（內嵌 admin 的 AI 助理）。屆時的形態（Phase 8）：

```
Web chat 前端 ──▶ agent backend（Claude API + agent loop，MCP client）──▶ nl-mcp ──▶ CMS API
```

登入的 CMS session 直接换發短效 API key 傳給 agent backend，權限自然收斂到該使用者。

---

## 3. 系統架構（v0.2）

```
        你的 schema 宣告（nl DSL, Go）
                    │  nl generate（dev mode 自動 watch）
                    ▼
   generated code：ent client / GraphQL schema+resolvers / 權限層 / MCP metadata
                    │
     ┌──────────────┴────────────────────────────┐
     │                                           │
     ▼                                           ▼
┌──────────────────────┐   GraphQL + API key ┌──────────────────┐
│  CMS Server（binary）│◀────────────────────│ nl-mcp（binary） │◀── MCP clients
│  GraphQL API          │                    │  獨立部署          │
│  auth / session       │                    └──────────────────┘
│  ★ access control     │
│  hooks / validation   │
└──────────┬───────────┘
           │  typed SQL（ent + pgx）
           ▼
      PostgreSQL          ┌─ CLI（nl）：init / dev / generate / migrate plan|apply / start
```

### Repo 結構（monorepo，Go workspace）

```
nl/
├── cmd/
│   ├── nl/            # CLI：init, dev, generate, migrate, start
│   └── nl-mcp/        # MCP server（獨立 binary / 獨立 Docker image）
├── nl/                # 核心 DSL：List/Fields/Access 宣告
├── field/             # field type DSL（→ ent field 對應）
├── access/            # 權限規則型別；privacy policy / field-layer 產生器
├── codegen/           # nl DSL → ent schema → entgql 的 codegen pipeline
├── server/            # CMS server：GraphQL handler、auth middleware、session
├── auth/              # password（argon2id）、session、API key 簽發/驗證
├── mcpserver/         # MCP tools/resources 實作（官方 modelcontextprotocol/go-sdk）
├── nltest/            # testcontainers helper（框架使用者也可用）
└── examples/blog/     # 範例專案（每個 phase 的驗收載體）
```

### Field types v1

`text`、`integer`、`float`、`decimal`、`checkbox`、`timestamp`、`select`、`json`、
`password`（argon2id，永不可讀）、`relationship`（many-to-one；many-to-many 自動
join table）。每個 list 自動有 `id`（UUIDv7）、`createdAt`、`updatedAt`。
Hooks：`resolveInput` → `validate` → `beforeOperation` → `afterOperation`。

---

## 4. GraphQL API 形態（entgql 生成）

```graphql
type Query {
  post(id: ID!): Post
  posts(where: PostWhereInput, orderBy: [PostOrder!],
        first: Int, after: Cursor): PostConnection!   # Relay 分頁
  _schema: SchemaInfo!                                 # 權限過濾後的 introspection 摘要
}
type Mutation {
  createPost(input: CreatePostInput!): Post!
  updatePost(id: ID!, input: UpdatePostInput!): Post!
  deletePost(id: ID!): DeletePayload!
  login(email: String!, password: String!): AuthPayload!
  createApiKey(input: CreateApiKeyInput!): ApiKey!     # 供 MCP / s2s
}
```

- filter（`PostWhereInput`）與 orderBy 由 codegen 依 field type 生成，全參數化無 injection 面。
- 錯誤統一走 GraphQL error extensions：`{code: "FIELD_ACCESS_DENIED", field: "status"}`。

## 5. 測試策略

- **Unit**：DSL → ent schema 轉換（golden tests）、access 規則求值、field 驗證。
- **Integration（重點）**：testcontainers-go 起真實 Postgres，覆蓋：
  migration（diff→apply→再 diff 應為空）、CRUD、關聯、**權限矩陣**
  （每 list 操作 × 每種規則 × list/field 兩層）、GraphQL e2e、
  **MCP e2e**（起 CMS + nl-mcp 兩個 process，驗證權限與 GraphQL 直連完全一致）。
- **CI**：GitHub Actions —— golangci-lint + unit + integration，PR 必跑。

## 6. 開發里程碑

| Phase | 內容 | 驗收標準 |
|---|---|---|
| **0. 腳手架** | go workspace、CLI 骨架、CI、testcontainers 基礎 | `nl dev` 可跑、CI 綠燈 |
| **1. Schema DSL + Codegen** | nl DSL、field types、DSL→ent schema→entgql pipeline、`nl generate` | examples/blog 宣告 schema 可生成並編譯 |
| **2. Migration** | Atlas 整合、dev 自動套用、`migrate plan/apply`、破壞性變更防護 | 增減 field 後 `nl dev` 數秒內完成 generate+migrate |
| **3. GraphQL API + Auth** | entgql 生成的 query/mutation、login/session、API key | GraphQL e2e 通過；playground 可操作 |
| **4. Access Control** | list 層級（privacy policy）+ field 層級（resolver/input 層）、row-level filter | 權限矩陣測試全綠 |
| **5. MCP Server** | nl-mcp 獨立 binary、GraphQL client、tools/resources、stdio+HTTP、API key 認證 | Claude 經 MCP 完成 CRUD，權限與 GraphQL 直連一致 |
| **6. MCP OAuth + Hardening** | OAuth 2.1（CMS 作 authorization server、PKCE、DCR）、`nl init` 腳手架、文件、examples 完整化 | 兩個不同權限的使用者從 Claude Desktop 連同一個 nl-mcp，各自登入後權限視圖不同；新使用者 10 分鐘內從零跑起 CMS |
| **7.（選配）** | REST/OpenAPI（entoas）、Admin UI、YAML schema loader | — |
| **8.（選配）** | Agent UI（chat 前端 + agent backend + MCP client）、檔案上傳、plugin 生態 | — |

Phase 1–5 為 v1 範圍；每個 phase 均以 examples/blog + 整合測試作驗收。

## 7. 已決事項與開放問題

### Rich text（2026-07-09 決議）

棄用 draft.js（Facebook 已停止維護）。採 **Tiptap（ProseMirror 系）**：
- 儲存格式為 ProseMirror document JSON（JSONB），編輯器中立、未來可換編輯器不換資料。
- 後端提供 `richText` field type：JSONB + GraphQL JSON scalar + node 白名單驗證 +
  衍生純文字欄位（對照 e-info 的 contentPreview）。
- 未來 Admin UI 用 Tiptap headless；配 Yjs 可升級即時共編，取代編輯鎖（lockBy）機制。
- 遷移：draft.js ContentState → HTML → ProseMirror parser → doc JSON。
- 次選為 Lexical（Meta 官方繼承者，Payload CMS 採用），若未來 Admin UI 確定 React-only 可重評。

**Ingest 管線（2026-07-09 補充）**：主要寫入者是 LLM agent（Word 檔、Google Doc
連結、模型生成內容），通用中繼格式為 HTML/Markdown：
- docx → mammoth/pandoc → HTML；Google Doc → Drive API export（text/html）。
- 一個小型 Node 轉換服務（與 Admin UI 的 Tiptap 共用 schema 定義）負責
  `HTML/Markdown → PM JSON → sanitize → node/attrs 白名單驗證`；
  MCP 的 create/update 對 richText 欄位接受 HTML/Markdown in、JSON 落地。
- 衍生欄位：plaintext（搜尋/預覽）、HTML cache（前台渲染）。

**Custom blocks（e-info readr draft-editor 對應）**：
- 官方 extension 直接涵蓋：標準 inline/block、link、table、textAlign、fontColor、
  divider、youtube。
- Custom node（attrs 存關聯 id，HTML 形態如 `<div data-type="slideshow" data-photo-ids>`）：
  image、slideshow/carousel、video、audio、infoBox（PM 原生支援巢狀 block 內容）、
  embed（raw HTML + sanitize）。
- 最花工：annotation、footnote、sideIndex（inline 互動型，各約 2–3 天含編輯器 UI）。
- 遷移：atomic block 的 entity data 是結構化 JSON → 直接映射 node attrs（不經 HTML）；
  一般文字 block 走 HTML 路徑。


**已決**（2026-07-08）：codegen（ent 基底）、GraphQL-first、MCP 獨立部署、
MCP 只接 CMS API、agent UI 列選配。

**仍開放**：
1. 框架名稱（暫以 `nl` 佔位）。
2. ent 依賴的深度：DSL 是否允許直接透傳 ent 原生 schema（逃生口）？（傾向允許）
3. 部署型態：是否需要考慮多 instance / serverless（影響 session store 與 migration lock）？
4. 是否需要接既有 Postgres DB（brownfield introspect-first 模式）？
