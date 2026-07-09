// nl-mcp：獨立部署的 MCP server。
// 不直接碰 DB —— 所有操作經 CMS 的 GraphQL API，權限視圖與操作者完全一致。
//
// 認證模式：
//
//	stdio（預設）     NL_API_KEY 必填（個人 API key，於 CMS 以 createApiKey 簽發）
//	-http :8081       MCP OAuth 2.1：使用者從 Claude Desktop / claude.ai 連接時
//	                  會被導向 CMS 登入，各自取得各自權限的 token（不需 API key）
//
// 環境變數：
//
//	NL_GRAPHQL_URL   CMS GraphQL endpoint（預設 http://localhost:8080/graphql）
//	NL_CMS_URL       CMS base URL，OAuth authorization server（預設由 NL_GRAPHQL_URL 推導）
//	NL_API_KEY       stdio 模式必填
//	NL_RICHTEXT_URL  richtext 轉換服務（預設 http://localhost:8082）
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hcchien/nl/mcpserver"

	// 註冊 lists 宣告（meta 於執行期從 nl registry 導出）
	_ "github.com/hcchien/nl/lists"
)

func main() {
	httpAddr := flag.String("http", "", "serve MCP over streamable HTTP with OAuth on this address (default: stdio)")
	flag.Parse()

	gqlURL := os.Getenv("NL_GRAPHQL_URL")
	if gqlURL == "" {
		gqlURL = "http://localhost:8080/graphql"
	}

	if *httpAddr != "" {
		cmsURL := os.Getenv("NL_CMS_URL")
		if cmsURL == "" {
			cmsURL = strings.TrimSuffix(gqlURL, "/graphql")
		}
		log.Printf("nl-mcp listening on %s (streamable HTTP + OAuth, authorization server: %s)", *httpAddr, cmsURL)
		log.Fatal(http.ListenAndServe(*httpAddr, mcpserver.NewHTTPHandler(gqlURL, cmsURL)))
	}

	apiKey := os.Getenv("NL_API_KEY")
	if apiKey == "" {
		log.Fatal("NL_API_KEY is required for stdio mode (在 CMS 用 createApiKey mutation 簽發)")
	}
	if err := mcpserver.New(gqlURL, apiKey).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
