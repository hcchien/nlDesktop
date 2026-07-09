// Package richtext 處理 rich text（ProseMirror doc JSON）的 Go 端邏輯：
// node/mark 白名單驗證、純文字抽取，以及呼叫 Node 轉換服務的 client。
//
// 白名單必須與 converter/schema.mjs 的 Tiptap extensions 保持同步。
package richtext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// 與 converter/schema.mjs 同步的白名單。
var allowedNodes = set(
	"doc", "paragraph", "text", "heading", "blockquote",
	"bulletList", "orderedList", "listItem", "codeBlock",
	"horizontalRule", "hardBreak",
	"image", "youtube", "slideshow", "embed", "infoBox",
)

var allowedMarks = set(
	"bold", "italic", "strike", "code", "link",
	"underline", "subscript", "superscript", "textStyle",
)

func set(ss ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// Validate 檢查 doc 是否為合法的 ProseMirror document，且僅使用白名單內的
// node/mark。空 doc（nil）視為合法。
func Validate(doc map[string]any) error {
	if doc == nil {
		return nil
	}
	if t, _ := doc["type"].(string); t != "doc" {
		return fmt.Errorf("richtext: root node must be \"doc\", got %q", doc["type"])
	}
	return validateNode(doc)
}

func validateNode(node map[string]any) error {
	t, _ := node["type"].(string)
	if _, ok := allowedNodes[t]; !ok {
		return fmt.Errorf("richtext: node type %q not allowed", t)
	}
	if marks, ok := node["marks"].([]any); ok {
		for _, m := range marks {
			mm, ok := m.(map[string]any)
			if !ok {
				return fmt.Errorf("richtext: malformed mark")
			}
			mt, _ := mm["type"].(string)
			if _, ok := allowedMarks[mt]; !ok {
				return fmt.Errorf("richtext: mark type %q not allowed", mt)
			}
		}
	}
	if content, ok := node["content"].([]any); ok {
		for _, c := range content {
			cm, ok := c.(map[string]any)
			if !ok {
				return fmt.Errorf("richtext: malformed content node")
			}
			if err := validateNode(cm); err != nil {
				return err
			}
		}
	}
	return nil
}

// Plaintext 抽取 doc 的純文字（供搜尋與預覽），超過 limit 截斷；limit <= 0 不截斷。
func Plaintext(doc map[string]any, limit int) string {
	var b strings.Builder
	walkText(doc, &b)
	s := strings.TrimSpace(b.String())
	if limit > 0 && len([]rune(s)) > limit {
		return string([]rune(s)[:limit])
	}
	return s
}

func walkText(node map[string]any, b *strings.Builder) {
	if t, _ := node["type"].(string); t == "text" {
		if s, _ := node["text"].(string); s != "" {
			b.WriteString(s)
		}
		return
	}
	content, _ := node["content"].([]any)
	for _, c := range content {
		if cm, ok := c.(map[string]any); ok {
			walkText(cm, b)
		}
	}
	// block 之間補空白，避免文字黏在一起
	if t, _ := node["type"].(string); t == "paragraph" || strings.HasPrefix(t, "heading") {
		b.WriteString(" ")
	}
}

// Client 呼叫 Node 轉換服務（converter/）。
type Client struct {
	URL  string // 例：http://localhost:8082
	HTTP *http.Client
}

// ToDoc 將 HTML 或 Markdown 轉為 PM doc JSON（以開頭是否為 '<' 判別格式）。
func (c *Client) ToDoc(ctx context.Context, input string) (map[string]any, error) {
	key := "markdown"
	if strings.HasPrefix(strings.TrimSpace(input), "<") {
		key = "html"
	}
	body, _ := json.Marshal(map[string]string{key: input})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/to-json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("richtext converter unreachable (%s): %w", c.URL, err)
	}
	defer resp.Body.Close()
	var out struct {
		Doc   map[string]any `json:"doc"`
		Error string         `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("richtext converter bad response: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("richtext converter: %s", out.Error)
	}
	return out.Doc, nil
}
