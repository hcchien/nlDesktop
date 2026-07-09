package richtext

import (
	"strings"
	"testing"
)

func doc(nodes ...map[string]any) map[string]any {
	content := make([]any, len(nodes))
	for i, n := range nodes {
		content[i] = n
	}
	return map[string]any{"type": "doc", "content": content}
}

func para(text string, marks ...string) map[string]any {
	t := map[string]any{"type": "text", "text": text}
	if len(marks) > 0 {
		ms := make([]any, len(marks))
		for i, m := range marks {
			ms[i] = map[string]any{"type": m}
		}
		t["marks"] = ms
	}
	return map[string]any{"type": "paragraph", "content": []any{t}}
}

func TestValidate(t *testing.T) {
	valid := doc(
		para("hello", "bold", "link"),
		map[string]any{"type": "slideshow", "attrs": map[string]any{"photoIds": []any{1.0, 2.0}}},
		map[string]any{"type": "infoBox", "content": []any{para("nested")}},
	)
	if err := Validate(valid); err != nil {
		t.Errorf("valid doc rejected: %v", err)
	}
	if err := Validate(nil); err != nil {
		t.Errorf("nil doc rejected: %v", err)
	}
	if err := Validate(map[string]any{"type": "paragraph"}); err == nil {
		t.Error("non-doc root accepted")
	}
	if err := Validate(doc(map[string]any{"type": "script"})); err == nil {
		t.Error("unknown node type accepted")
	}
	if err := Validate(doc(para("x", "blink"))); err == nil {
		t.Error("unknown mark accepted")
	}
}

func TestPlaintext(t *testing.T) {
	d := doc(
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 2.0},
			"content": []any{map[string]any{"type": "text", "text": "標題"}}},
		para("第一段"),
		para("第二段"),
	)
	got := Plaintext(d, 0)
	for _, want := range []string{"標題", "第一段", "第二段"} {
		if !strings.Contains(got, want) {
			t.Errorf("Plaintext = %q, missing %q", got, want)
		}
	}
	if lim := Plaintext(d, 3); len([]rune(lim)) != 3 {
		t.Errorf("limit not applied: %q", lim)
	}
}
