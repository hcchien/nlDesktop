package main

import (
	"context"
	"log"
	"time"

	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/user"
)

// seed 建立範例使用者（四種角色）與 sections/categories/tags/authors/posts。
// 已 seed 過（admin 帳號存在）則跳過。
func seed(ctx context.Context, client *ent.Client) error {
	exists, err := client.User.Query().Where(user.EmailEQ("admin@example.com")).Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		log.Println("seed: already seeded, skipping")
		return nil
	}

	users := map[string]*ent.User{}
	for _, u := range []struct{ name, email, role string }{
		{"Admin", "admin@example.com", "admin"},
		{"Moderator", "moderator@example.com", "moderator"},
		{"Editor", "editor@example.com", "editor"},
		{"Contributor", "contributor@example.com", "contributor"},
	} {
		// 密碼由 mutation hook 雜湊
		created, err := client.User.Create().
			SetName(u.name).SetEmail(u.email).SetPassword("password123").
			SetRole(user.Role(u.role)).Save(ctx)
		if err != nil {
			return err
		}
		users[u.role] = created
	}

	env, err := client.Section.Create().SetSlug("environment").SetName("環境").Save(ctx)
	if err != nil {
		return err
	}
	climate, err := client.Category.Create().SetSlug("climate").SetName("氣候變遷").SetSection(env).Save(ctx)
	if err != nil {
		return err
	}
	tagNames := []string{"淨零", "生物多樣性", "再生能源"}
	tags := make([]*ent.Tag, 0, len(tagNames))
	for _, n := range tagNames {
		t, err := client.Tag.Create().SetName(n).Save(ctx)
		if err != nil {
			return err
		}
		tags = append(tags, t)
	}
	writer, err := client.Author.Create().SetName("王小明").SetBio("環境線記者").Save(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	if _, err := client.Post.Create().
		SetTitle("氣候法案三讀通過").
		SetState("published").
		SetPublishTime(now).
		SetContent(pmDoc("立法院今日三讀通過氣候法案。")).
		SetSection(env).
		AddCategories(climate).
		AddTags(tags[0]).
		AddWriters(writer).
		SetCreatedBy(users["editor"]).
		Save(ctx); err != nil {
		return err
	}
	if _, err := client.Post.Create().
		SetTitle("志工投稿：河川清理紀實").
		SetContent(pmDoc("上週末的河川清理活動紀實草稿。")).
		SetSection(env).
		AddTags(tags[1]).
		SetCreatedBy(users["contributor"]).
		Save(ctx); err != nil {
		return err
	}

	log.Println("seed: done. 帳號 admin/moderator/editor/contributor@example.com，密碼 password123")
	return nil
}

// pmDoc 產生單段落的 ProseMirror document。
func pmDoc(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "doc",
		"content": []interface{}{
			map[string]interface{}{
				"type": "paragraph",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": text},
				},
			},
		},
	}
}
