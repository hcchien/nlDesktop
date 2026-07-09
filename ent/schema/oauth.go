package schema

import (
	"time"

	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// OAuthClient 是 dynamic client registration 註冊的 OAuth 2.1 public client
// （MCP clients：Claude Desktop、claude.ai 等）。內部實體，不暴露於 GraphQL。
type OAuthClient struct {
	ent.Schema
}

func (OAuthClient) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (OAuthClient) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_id").
			NotEmpty().
			Unique(),
		field.String("name").
			Default(""),
		field.JSON("redirect_uris", []string{}),
	}
}

func (OAuthClient) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.Skip(entgql.SkipAll),
	}
}

// OAuthCode 是 authorization code（單次使用、短效），綁定使用者、client、
// redirect_uri 與 PKCE code_challenge。內部實體。
type OAuthCode struct {
	ent.Schema
}

func (OAuthCode) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (OAuthCode) Fields() []ent.Field {
	return []ent.Field{
		// 只存 SHA-256 雜湊
		field.String("code_hash").
			NotEmpty().
			Unique(),
		field.String("client_id").
			NotEmpty(),
		field.String("redirect_uri").
			NotEmpty(),
		field.String("code_challenge").
			NotEmpty(),
		field.Time("expires_at").
			Default(func() time.Time { return time.Now().Add(5 * time.Minute) }),
	}
}

func (OAuthCode) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).
			Unique().
			Required(),
	}
}

func (OAuthCode) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.Skip(entgql.SkipAll),
	}
}

// OAuthRefresh 是 refresh token（rotation：每次使用即銷毀換發新的一組）。
// 內部實體。
type OAuthRefresh struct {
	ent.Schema
}

func (OAuthRefresh) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (OAuthRefresh) Fields() []ent.Field {
	return []ent.Field{
		// 只存 SHA-256 雜湊
		field.String("token_hash").
			NotEmpty().
			Unique(),
		field.String("client_id").
			NotEmpty(),
		field.Time("expires_at"),
	}
}

func (OAuthRefresh) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).
			Unique().
			Required(),
	}
}

func (OAuthRefresh) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.Skip(entgql.SkipAll),
	}
}
