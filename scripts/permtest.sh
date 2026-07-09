#!/bin/bash
# 權限矩陣 e2e 驗證
GQL=http://localhost:8080/graphql

gql() { # $1 token, $2 query json
  curl -s -X POST "$GQL" -H "Content-Type: application/json" ${1:+-H "Authorization: Bearer $1"} -d "$2"
}

login() {
  gql "" "{\"query\":\"mutation{login(email:\\\"$1\\\",password:\\\"password123\\\"){token user{id name role}}}\"}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["login"]["token"])'
}

echo "== 1. login 四種角色 =="
ADMIN=$(login admin@example.com) && echo "admin ok"
EDITOR=$(login editor@example.com) && echo "editor ok"
CONTRIB=$(login contributor@example.com) && echo "contributor ok"
MOD=$(login moderator@example.com) && echo "moderator ok"

echo "== 2. 未登入查 posts（應被拒）=="
gql "" '{"query":"{posts(first:5){edges{node{id title}}}}"}'
echo
echo "== 3. admin 查 posts（應看到 2 篇）=="
gql "$ADMIN" '{"query":"{posts(first:5){edges{node{id title state createdBy{name}}}}}"}'
echo
echo "== 4. editor 查 posts（row filter：只看到自己那篇）=="
gql "$EDITOR" '{"query":"{posts(first:5){edges{node{id title}}}}"}'
echo
echo "== 5. contributor 查 posts（只看到自己那篇）=="
gql "$CONTRIB" '{"query":"{posts(first:5){edges{node{id title state}}}}"}'
echo
echo "== 6. contributor 建立 post（可以）=="
gql "$CONTRIB" '{"query":"mutation{createPost(input:{title:\"contributor 新草稿\"}){id title state}}"}'
echo
echo "== 7. contributor 嘗試發佈（改 state，應被拒：field-level）=="
CID=$(gql "$CONTRIB" '{"query":"{posts(first:1,orderBy:{field:CREATED_AT,direction:DESC}){edges{node{id}}}}"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["posts"]["edges"][0]["node"]["id"])')
gql "$CONTRIB" "{\"query\":\"mutation{updatePost(id:$CID,input:{state:published,publishTime:\\\"2026-07-08T12:00:00Z\\\"}){id state}}\"}"
echo
echo "== 8. contributor 改自己文章的標題（可以）=="
gql "$CONTRIB" "{\"query\":\"mutation{updatePost(id:$CID,input:{title:\\\"改過的標題\\\"}){id title}}\"}"
echo
echo "== 9. editor 嘗試改別人（contributor）的文章（row-level 應被拒）=="
gql "$EDITOR" "{\"query\":\"mutation{updatePost(id:$CID,input:{title:\\\"editor 亂改\\\"}){id}}\"}"
echo
echo "== 10. moderator 發佈但沒給 publishTime（驗證 hook 應擋）=="
MID=$(gql "$MOD" '{"query":"mutation{createPost(input:{title:\"mod 的文章\"}){id}}"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["createPost"]["id"])')
gql "$MOD" "{\"query\":\"mutation{updatePost(id:$MID,input:{state:published}){id state}}\"}"
echo
echo "== 11. moderator 正常發佈（給 publishTime，可以）=="
gql "$MOD" "{\"query\":\"mutation{updatePost(id:$MID,input:{state:published,publishTime:\\\"2026-07-08T12:00:00Z\\\"}){id state publishTime}}\"}"
echo
echo "== 12. contributor 刪文章（delete 僅 admin，應被拒）=="
gql "$CONTRIB" "{\"query\":\"mutation{deletePost(id:$CID)}\"}"
echo
echo "== 13. editor 查 users 的 email（field-level read 應為 null + error）=="
gql "$EDITOR" '{"query":"{users(first:2){edges{node{id name email}}}}"}'
echo
echo "== 14. admin 查 users 的 email（可以）=="
gql "$ADMIN" '{"query":"{users(first:2){edges{node{id name email role}}}}"}'
echo
echo "== 15. contributor 查 users（list-level 應被拒）=="
gql "$CONTRIB" '{"query":"{users(first:2){edges{node{id name}}}}"}'
echo
echo "== 16. editor 的 me + 自己的 email（本人例外，可讀）=="
gql "$EDITOR" '{"query":"{me{id name email role}}"}'
echo
echo "== 17. editor 簽發 API key =="
gql "$EDITOR" '{"query":"mutation{createApiKey(name:\"mcp-key\"){key name}}"}'
